package kurz.media

import androidx.compose.foundation.Image
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.toComposeImageBitmap
import androidx.compose.ui.layout.ContentScale
import dev.onvoid.webrtc.PeerConnectionFactory
import dev.onvoid.webrtc.media.FourCC
import dev.onvoid.webrtc.media.MediaDevices
import dev.onvoid.webrtc.media.MediaStreamTrack
import dev.onvoid.webrtc.media.audio.AudioDeviceModule
import dev.onvoid.webrtc.media.audio.AudioOptions
import dev.onvoid.webrtc.media.video.VideoBufferConverter
import dev.onvoid.webrtc.media.video.VideoCaptureCapability
import dev.onvoid.webrtc.media.video.VideoDesktopSource
import dev.onvoid.webrtc.media.video.VideoDevice
import dev.onvoid.webrtc.media.video.VideoDeviceSource
import dev.onvoid.webrtc.media.video.VideoFrame
import dev.onvoid.webrtc.media.video.VideoTrack
import dev.onvoid.webrtc.media.video.VideoTrackSink
import dev.onvoid.webrtc.media.video.desktop.DesktopCaptureCallback
import dev.onvoid.webrtc.media.video.desktop.DesktopCapturer
import dev.onvoid.webrtc.media.video.desktop.ScreenCapturer
import dev.onvoid.webrtc.media.video.desktop.WindowCapturer
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.delay
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.jetbrains.skia.ColorAlphaType
import org.jetbrains.skia.ColorType
import org.jetbrains.skia.Image as SkImage
import org.jetbrains.skia.ImageInfo
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicInteger

@Composable
actual fun rememberMediaCapture(): MediaCapture {
    val c = remember { DesktopCapture() }
    DisposableEffect(c) { onDispose(c::release) }
    return c
}

/** Microfones que não são microfones de ninguém (dispositivos virtuais de apps de chamada e agregados): ficam fora da lista. */
private val virtualAudio = Regex("aggregate device|microsoft teams audio|zoom ?audio ?device|blackhole|soundflower|loopback", RegexOption.IGNORE_CASE)
/** Câmaras virtuais: só entram como escolha automática se não houver nenhuma real. */
private val virtualCamera = Regex("virtual|obs", RegexOption.IGNORE_CASE)

/** O AudioDeviceModule é nosso (e não o interno da fábrica) para poder escolher o microfone com setRecordingDevice. */
internal val adm by lazy { AudioDeviceModule() }

/** Uma fábrica por processo: as tracks locais e a PeerConnection têm de vir da mesma. */
internal val rtc by lazy { PeerConnectionFactory.builder().setAudioDeviceModule(adm).build() }

/**
 * Captura com webrtc-java: a mesma track alimenta a pré-visualização (um [FrameSink]) e a PeerConnection da sala
 * (ver DesktopRooms), por isso a câmara abre uma vez só.
 * Sem API de permissões na JVM: no macOS a negação nota-se por câmara sem imagens (aviso ao fim de 4 s) ou ecrã só com o fundo.
 * ponytail: sem medidor do microfone (o áudio do AudioDeviceModule não passa por sinks Java); micLevel fica a 0.
 */
internal class DesktopCapture : MediaCapture() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    /** Tracks activas por fonte ("mic" | "camera" | "screen"); o id da track é o nome da fonte. */
    val tracks = mutableMapOf<String, MediaStreamTrack>()
    /** Avisado a cada track que liga (track) ou desliga (null). */
    var onTrack: ((source: String, track: MediaStreamTrack?) -> Unit)? = null
    private var cameraSource: VideoDeviceSource? = null
    private var screenSource: VideoDesktopSource? = null
    init { scope.launch { runCatching { rtc } } }  // aquece a fábrica (~0,5 s) antes do primeiro microfone ou da primeira sala
    private val cameraFrames = FrameSink()
    private val screenFrames = FrameSink(maxWidth = 960)

    // ponytail: tracks e fontes paradas não são libertadas (a PeerConnection pode ainda estar a largá-las noutra thread);
    // fica um objecto nativo pequeno por cada liga/desliga.
    private fun put(source: String, track: MediaStreamTrack?) {
        if (track == null) tracks.remove(source) else tracks[source] = track
        onTrack?.invoke(source, track)
    }

    // Escolhas lembradas entre arranques: o último microfone escolhido e a última câmara que deu imagem.
    private val prefs = java.util.prefs.Preferences.userNodeForPackage(DesktopCapture::class.java)
    private fun pref(key: String) = runCatching { prefs.get(key, null) }.getOrNull()
    private fun save(key: String, value: String?) { runCatching { if (value == null) prefs.remove(key) else prefs.put(key, value); prefs.flush() } }
    init { cameraDeviceId = pref("camera"); micDeviceId = pref("mic") }

    override fun refreshDevices() {
        scope.launch {
            micDefaultName = runCatching { MediaDevices.getDefaultAudioCaptureDevice()?.name }.getOrNull()
            micDevices = MediaDevices.getAudioCaptureDevices().filter { it.descriptor == micDeviceId || !virtualAudio.containsMatchIn(it.name) }.sortedBy { it.name }.map { Device(it.descriptor, it.name) }
            cameraDevices = videoDevices().map { Device(it.descriptor, it.name) }
            probeCameras()
        }
    }

    // O dispositivo fica no AudioDeviceModule, por isso vale também para o próximo startMic. Ligado, pára e volta a
    // arrancar a gravação à mão: a track e a publicação ficam como estão (sem renegociar).
    override fun selectMic(id: String?) {
        if (id == micDeviceId) return
        val previous = micDeviceId
        micDeviceId = id
        scope.launch {
            val dev = MediaDevices.getAudioCaptureDevices().firstOrNull { it.descriptor == id } ?: MediaDevices.getDefaultAudioCaptureDevice()
            val live = mic == Capture.On
            runCatching {
                if (live) adm.stopRecording()
                adm.setRecordingDevice(dev)
                if (live) { adm.initRecording(); adm.startRecording() }
            }.onSuccess { save("mic", id) }.onFailure { micDeviceId = previous; notice = Msg.NO_MIC_SWITCH }
        }
    }

    override suspend fun startMic(): Boolean {
        if (mic != Capture.Off) return mic == Capture.On
        mic = Capture.Starting
        val track = withContext(Dispatchers.IO) {
            runCatching {
                val devices = MediaDevices.getAudioCaptureDevices()
                check(devices.isNotEmpty())
                // o microfone lembrado (o ADM novo começa no padrão); se já não existe, volta ao padrão
                micDeviceId?.let { id -> devices.firstOrNull { it.descriptor == id }?.let(adm::setRecordingDevice) ?: run { micDeviceId = null } }
                val opts = AudioOptions().apply { echoCancellation = true; noiseSuppression = true; autoGainControl = true; highpassFilter = true }
                rtc.createAudioTrack("mic", rtc.createAudioSource(opts))
            }.getOrNull()
        }
        if (track == null) { mic = Capture.Off; notice = Msg.NO_MIC; return false }
        put("mic", track)
        mic = Capture.On
        return true
    }

    override fun stopMic() { if (mic == Capture.Off) return; put("mic", null); mic = Capture.Off }

    override suspend fun startCamera(): Boolean {
        if (camera != Capture.Off) return camera == Capture.On
        camera = Capture.Starting
        stopProbing()  // a câmara não pode estar aberta pela verificação
        triedCameras.clear()
        val src = openCamera()
        if (src == null) { camera = Capture.Off; notice = Msg.NO_CAMERA_DEVICE; return false }
        cameraSource = src
        val track = rtc.createVideoTrack("camera", src).also { it.addSink(cameraFrames) }
        put("camera", track)
        camera = Capture.On
        watchCamera(src)
        return true
    }

    /** Ordem estável (o sistema devolve-as em ordem diferente de cada vez), para o padrão e o menu não mudarem sozinhos. */
    private fun videoDevices() = MediaDevices.getVideoCaptureDevices().sortedBy { it.name }

    private var cameraName = ""
    private var liveCameraId: String? = null
    private val triedCameras = ConcurrentHashMap.newKeySet<String>()  // descritores já abertos neste arranque (para a vez seguinte não repetir)

    /** Por onde começar: as que já falharam ficam para o fim; depois a escolhida/lembrada, as reais antes das virtuais (OBS) e as vistas a funcionar antes das por verificar. */
    private fun candidates() = videoDevices().sortedWith(compareBy({ cameraReady[it.descriptor] == false }, { it.descriptor != cameraDeviceId }, { virtualCamera.containsMatchIn(it.name) }, { cameraReady[it.descriptor] != true }))

    /** Espera até 4 s por imagens: se chegam, lembra a câmara; se não (ocupada, tampa fechada, sem permissão) passa à seguinte, e só avisa quando já não há mais. */
    private fun watchCamera(src: VideoDeviceSource) {
        val id = liveCameraId
        scope.launch {
            repeat(8) {
                delay(500)
                if (cameraSource !== src) return@launch
                if (cameraFrames.image != null) { id?.let { cameraReady = cameraReady + (it to true); save("camera", it) }; return@launch }
            }
            if (cameraSource !== src) return@launch
            id?.let { cameraReady = cameraReady + (it to false) }
            if (candidates().any { it.descriptor !in triedCameras }) swapCamera { stopCamera() }
            else { stopCamera(); notice = "A câmara \"$cameraName\" não dá imagem (ocupada, tampa fechada ou sem permissão). Escolhe outra na seta ao lado do botão." }
        }
    }

    /** Abre a primeira câmara possível que ainda não foi tentada (ver [candidates]); null se não houver ou nenhuma abrir. */
    private suspend fun openCamera() = withContext(Dispatchers.IO) {
        for (dev in candidates()) {
            if (!triedCameras.add(dev.descriptor)) continue
            val src = runCatching {
                VideoDeviceSource().apply {
                    setVideoCaptureDevice(dev)
                    setVideoCaptureCapability(VideoCaptureCapability(1280, 720, 30))  // o módulo escolhe o formato mais próximo
                    start()
                }
            }.getOrNull() ?: continue
            cameraName = dev.name; liveCameraId = dev.descriptor; cameraDeviceId = dev.descriptor
            return@withContext src
        }
        null
    }

    // Troca em quente: abre a nova câmara antes de largar a velha e republica a track "camera" (o botão não passa por Off).
    override fun selectCamera(id: String) {
        if (id == cameraDeviceId) return
        val previous = cameraDeviceId
        cameraDeviceId = id
        cameraReady = cameraReady - id  // esquece uma falha antiga: a escolha do utilizador é tentada primeiro
        triedCameras.clear()
        if (camera == Capture.On) swapCamera { cameraDeviceId = previous }
    }

    private fun swapCamera(revert: () -> Unit) {
        scope.launch {
            stopProbing()
            val src = openCamera()
            if (src == null || camera != Capture.On) {  // não abriu, ou o utilizador desligou entretanto
                src?.stop()
                if (src == null) { revert(); notice = Msg.NO_CAMERA_DEVICE }
                return@launch
            }
            val old = cameraSource
            (tracks["camera"] as? VideoTrack)?.removeSink(cameraFrames)
            cameraFrames.clear()  // não mostra o último frame da câmara antiga enquanto a nova não dá imagem
            cameraSource = src
            put("camera", rtc.createVideoTrack("camera", src).also { it.addSink(cameraFrames) })
            old?.stop()
            watchCamera(src)
        }
    }

    override fun stopCamera() {
        if (camera == Capture.Off) return
        (tracks["camera"] as? VideoTrack)?.removeSink(cameraFrames)
        put("camera", null)
        cameraSource?.stop()
        cameraSource = null; camera = Capture.Off; cameraFrames.clear()
    }

    // Verificação: abre cada câmara um instante e vê se chegam imagens, para o menu deixar desactivadas as que não dão (a do MacBook
    // com a tampa fechada, por exemplo). Resultado vale 2 min; a câmara em uso conta como boa e não se mexe nela.
    private var probeJob: Job? = null
    private val probedAt = ConcurrentHashMap<String, Long>()

    private suspend fun stopProbing() { probeJob?.cancelAndJoin(); probeJob = null }

    private fun probeCameras() {
        probeJob?.cancel()
        probeJob = scope.launch {
            for (dev in videoDevices()) {
                ensureActive()
                val id = dev.descriptor
                if (camera != Capture.Off && id == liveCameraId) { cameraReady = cameraReady + (id to true); continue }
                if (System.currentTimeMillis() - (probedAt[id] ?: 0) < 120_000) continue
                val ok = probe(dev)
                probedAt[id] = System.currentTimeMillis()
                cameraReady = cameraReady + (id to ok)
            }
        }
    }

    private suspend fun probe(dev: VideoDevice): Boolean {
        val src = withContext(Dispatchers.IO) {
            runCatching { VideoDeviceSource().apply { setVideoCaptureDevice(dev); setVideoCaptureCapability(VideoCaptureCapability(640, 480, 15)); start() } }.getOrNull()
        } ?: return false
        val frames = AtomicInteger()
        val track = rtc.createVideoTrack("probe", src)
        val sink = VideoTrackSink { frames.incrementAndGet() }
        track.addSink(sink)
        try {
            repeat(10) { if (frames.get() == 0) delay(250) }
            return frames.get() > 0
        } finally {
            track.removeSink(sink); src.stop()
        }
    }

    override val canPickScreen get() = true

    private var thumbsJob: Job? = null

    override fun refreshScreenSources() {
        thumbsJob?.cancel()
        thumbsJob = scope.launch {
            screenThumbs = emptyMap()
            val screens = ScreenCapturer(); val windows = WindowCapturer()
            try {
                // o título vem null nos ecrãs; se a listagem falhar, fica o ecrã principal (id 0, o que o openScreen usa sem escolha) para a partilha ainda andar
                val found = runCatching {
                    screens.desktopSources.mapIndexed { i, s -> Triple(screens, s, ScreenSource("s${s.id}", s.title.orEmpty().ifBlank { "Ecrã ${i + 1}" }, false)) } +
                        windows.desktopSources.filter { !it.title.isNullOrBlank() }.map { Triple(windows, it, ScreenSource("w${it.id}", it.title, true)) }
                }.getOrElse { screenSources = listOf(ScreenSource("s0", "Ecrã principal", false)); return@launch }
                screenSources = found.map { it.third }
                var shot: ImageBitmap? = null
                val onFrame = DesktopCaptureCallback { result, frame -> if (result == DesktopCapturer.Result.SUCCESS) shot = frameToImage(frame, 480) }
                screens.start(onFrame); windows.start(onFrame)
                for ((capturer, source, ui) in found) {
                    ensureActive()
                    shot = null
                    runCatching {  // o primeiro frame pode vir com erro temporário
                        capturer.selectSource(source)
                        repeat(5) { if (shot == null) { capturer.captureFrame(); if (shot == null) delay(60) } }
                    }
                    shot?.let { screenThumbs = screenThumbs + (ui.id to it) }
                }
            } finally { screens.dispose(); windows.dispose() }
        }
    }

    /** Abre o ecrã ou a janela escolhidos (ou o primeiro ecrã); null se não abrir. */
    private suspend fun openScreen() = withContext(Dispatchers.IO) {
        runCatching {
            val pick = screenSourceId
            val id = pick?.drop(1)?.toLong() ?: ScreenCapturer().run { try { desktopSources.firstOrNull()?.id ?: 0L } finally { dispose() } }
            VideoDesktopSource().apply { setSourceId(id, pick?.startsWith("w") == true); setFrameRate(screenQuality.fps); setMaxFrameSize(screenQuality.maxWidth, screenQuality.maxHeight); start() }
        }.getOrNull()
    }

    override fun selectScreen(id: String) {
        if (id == screenSourceId) return
        val previous = screenSourceId
        screenSourceId = id
        if (screen == Capture.On) swapScreen { screenSourceId = previous }
    }

    override fun selectScreenQuality(q: ScreenQuality) {
        if (q == screenQuality) return
        val previous = screenQuality
        screenQuality = q
        if (screen == Capture.On) swapScreen { screenQuality = previous }
    }

    // Troca em quente, como na câmara: abre a nova fonte antes de largar a velha e republica a track "screen"; [revert] desfaz a escolha se não abrir.
    private fun swapScreen(revert: () -> Unit) {
        scope.launch {
            val src = openScreen()
            if (src == null || screen != Capture.On) {
                src?.stop()
                if (src == null) { revert(); notice = Msg.NO_SCREEN }
                return@launch
            }
            val old = screenSource
            (tracks["screen"] as? VideoTrack)?.removeSink(screenFrames)
            screenFrames.clear()
            screenSource = src
            put("screen", rtc.createVideoTrack("screen", src).also { it.addSink(screenFrames) })
            old?.stop()
        }
    }

    override suspend fun startScreen(): Boolean {
        if (screen != Capture.Off) return screen == Capture.On
        screen = Capture.Starting
        val src = openScreen()
        if (src == null) { screen = Capture.Off; screenSourceId = null; notice = Msg.NO_SCREEN; return false }  // a janela pode ter fechado: volta ao ecrã
        screenSource = src
        put("screen", rtc.createVideoTrack("screen", src).also { it.addSink(screenFrames) })
        screen = Capture.On
        return true
    }

    override fun stopScreen() {
        if (screen == Capture.Off) return
        (tracks["screen"] as? VideoTrack)?.removeSink(screenFrames)
        put("screen", null)
        screenSource?.stop()
        screenSource = null; screen = Capture.Off; screenFrames.clear()
    }

    @Composable
    override fun CameraPreview(modifier: Modifier) {
        // espelhada, como é habitual na pré-visualização própria
        cameraFrames.image?.let { Image(it, null, modifier.graphicsLayer { scaleX = -1f }, contentScale = ContentScale.Crop) }
    }

    @Composable
    override fun ScreenPreview(modifier: Modifier) {
        screenFrames.image?.let { Image(it, null, modifier, contentScale = ContentScale.Fit) }
    }

    override fun release() { onTrack = null; stopMic(); stopCamera(); stopScreen(); scope.cancel() }
}

/**
 * Guarda o último frame de uma VideoTrack como ImageBitmap para a UI, com teto de ~15 fps e [maxWidth] px de largura
 * (I420 → BGRA no libyuv; o resto do frame nem é convertido).
 */
class FrameSink(private val maxWidth: Int = 640) : VideoTrackSink {
    var image by mutableStateOf<ImageBitmap?>(null); private set
    /** Frames recebidos (incluindo os que o teto de fps descarta). */
    @Volatile var count = 0; private set
    @Volatile private var last = 0L

    override fun onVideoFrame(frame: VideoFrame) {
        count++
        val now = System.nanoTime()
        if (now - last < 66_000_000) return
        last = now
        image = frameToImage(frame, maxWidth) ?: return
    }

    fun clear() { image = null }
}

// O limite é uma caixa larga (32:9) para a altura mandar também nos ecrãs ultra-largos; Fonte = caixa de 8K (não reduz nada).
private val ScreenQuality.maxHeight get() = resolution.height.takeIf { it > 0 } ?: 4320
private val ScreenQuality.maxWidth get() = if (resolution.height > 0) resolution.height * 32 / 9 else 7680

/** Último frame de uma captura como ImageBitmap de no máximo [maxWidth] px de largura (I420 → BGRA no libyuv; o resto do frame nem é convertido); null se vier vazio. */
internal fun frameToImage(frame: VideoFrame, maxWidth: Int): ImageBitmap? {
    val b = frame.buffer
    val w = minOf(b.width, maxWidth) and 1.inv()
    if (w <= 0) return null
    val h = (b.height.toLong() * w / b.width).toInt() and 1.inv()
    if (h <= 0) return null
    val scaled = b.cropAndScale(0, 0, b.width, b.height, w, h)  // cópia em memória Java: não tem release
    val px = ByteArray(w * h * 4)
    VideoBufferConverter.convertFromI420(scaled, px, FourCC.ARGB)  // ARGB do libyuv = bytes B,G,R,A
    return SkImage.makeRaster(ImageInfo(w, h, ColorType.BGRA_8888, ColorAlphaType.OPAQUE), px, w * 4).toComposeImageBitmap()
}

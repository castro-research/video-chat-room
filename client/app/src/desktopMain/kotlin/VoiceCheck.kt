import androidx.compose.ui.graphics.toPixelMap
import dev.onvoid.webrtc.PeerConnectionFactory
import dev.onvoid.webrtc.media.MediaStreamTrack
import dev.onvoid.webrtc.media.audio.AudioTrack
import dev.onvoid.webrtc.media.audio.AudioTrackSink
import dev.onvoid.webrtc.media.audio.CustomAudioSource
import dev.onvoid.webrtc.media.audio.HeadlessAudioDeviceModule
import dev.onvoid.webrtc.media.video.CustomVideoSource
import dev.onvoid.webrtc.media.video.NativeI420Buffer
import dev.onvoid.webrtc.media.video.VideoFrame
import dev.onvoid.webrtc.media.video.VideoTrack
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kurz.media.FrameSink
import kurz.media.Manager
import kurz.media.MediaSession
import java.util.concurrent.atomic.AtomicInteger
import kotlin.math.sin
import kotlin.system.exitProcess

// Verificação ponta a ponta sem janela nem permissões (ver tarefa :app:voiceCheck), contra manager + SFU reais:
// cria uma sala, entra com A e B; A publica câmara e microfone sintéticos, B conta o que chega de `a:camera` e `a:mic`
// e publica um "ecrã" sintético ao mesmo tempo, que A conta.
// Depois A desliga e volta a ligar a câmara (renegociação nos dois sentidos) e C entra a meio. Sai com 1 se faltar vídeo.
fun main(args: Array<String>) { runBlocking {
    val manager = Manager(args.getOrElse(0) { "http://localhost:30080" })
    check(manager.healthy() && !Manager("http://localhost:1").healthy()) { "healthz: verde no manager e vermelho numa porta fechada" }
    val factory = PeerConnectionFactory(HeadlessAudioDeviceModule())  // sem abrir microfone nem altifalantes
    val room = manager.create("voiceCheck")
    println("sala ${room.id} criada")

    val video = FrameSink()
    val screen = FrameSink()
    val audioBytes = AtomicInteger()
    suspend fun client(id: String, onRemote: (MediaStreamTrack, Boolean) -> Unit) =
        MediaSession(manager.join(room.id, id, id.uppercase()), id, factory, onRemote) { println("$id: sessão caiu: $it"); exitProcess(1) }
    val a = client("a") { t, added -> if (added && t is VideoTrack && t.id == "b:screen") t.addSink(screen) }
    val b = client("b") { t, added ->
        println("B ${if (added) "+" else "-"} ${t.kind} ${t.id}")
        if (added && t is VideoTrack && t.id == "a:camera") t.addSink(video)
        if (added && t is AudioTrack && t.id == "a:mic") t.addSink(AudioTrackSink { data, _, _, _, _ -> audioBytes.addAndGet(data.size) })
    }
    a.connect(); b.connect()
    println("A e B ligados ao SFU")

    // vídeo: 640x360 vermelho com uma barra que anda (o I420 em BT.601: Y=81 U=90 V=240); áudio: 440 Hz, 10 ms por bloco
    val camera = CustomVideoSource()
    val mic = CustomAudioSource()
    val pushers = launch(Dispatchers.IO) {
        var n = 0
        val pcm = ByteArray(480 * 2)
        while (isActive) {
            val buf = NativeI420Buffer.allocate(640, 360)
            val y = buf.dataY
            fill(y, 81); fill(buf.dataU, 90); fill(buf.dataV, 240)
            val bar = (n * 8) % 640
            for (row in 0 until 360) for (x in bar until minOf(bar + 16, 640)) y.put(row * buf.strideY + x, 235.toByte())
            val frame = VideoFrame(buf, System.nanoTime())
            camera.pushFrame(frame); frame.release()
            repeat(3) { k ->
                for (i in 0 until 480) {
                    val s = (sin(2 * Math.PI * 440 * ((n * 3 + k) * 480 + i) / 48_000.0) * 8_000).toInt()
                    pcm[2 * i] = s.toByte(); pcm[2 * i + 1] = (s shr 8).toByte()
                }
                mic.pushAudio(pcm, 16, 48_000, 1, 480)
            }
            n++
            delay(30)
        }
    }
    a.publish("camera", factory.createVideoTrack("camera", camera))
    a.publish("mic", factory.createAudioTrack("mic", mic))
    b.publish("screen", factory.createVideoTrack("screen", camera))  // ao mesmo tempo: força ofertas cruzadas (rollback do lado polite)

    suspend fun report(label: String) {
        val p = manager.room(room.id).participants.joinToString { "${it.userId}(mic=${it.mic} camera=${it.camera} screen=${it.screen})" }
        val img = video.image
        val center = img?.toPixelMap()?.let { it[img.width / 4, img.height / 2] }
        println("$label: B recebeu ${video.count} frames de a:camera, último ${img?.width}x${img?.height}, cor ao centro-esquerda $center; áudio ${audioBytes.get()} bytes; A recebeu ${screen.count} frames de b:screen; participantes: $p")
    }
    delay(6_000); report("6 s")
    val first = video.count

    a.publish("camera", null)
    delay(3_000); report("câmara desligada")
    val off = video.count

    a.publish("camera", factory.createVideoTrack("camera", camera))
    delay(5_000); report("câmara religada")
    val again = video.count - off

    // C entra com a sala já a publicar e publica logo a seguir ao joined: a oferta dele cruza-se com a do SFU
    val late = FrameSink()
    val c = client("c") { t, added -> if (added && t is VideoTrack && t.id == "a:camera") t.addSink(late) }
    c.connect()
    c.publish("mic", factory.createAudioTrack("mic", mic))
    delay(4_000)
    println("C recebeu ${late.count} frames de a:camera")

    pushers.cancel(); a.close(); b.close(); c.close()
    val ok = first > 30 && again > 30 && screen.count > 30 && late.count > 30
    println(if (ok) "OK" else "FALHOU (frames: primeiro troço $first, depois de religar $again)")
    exitProcess(if (ok) 0 else 1)
} }

private fun fill(b: java.nio.ByteBuffer, v: Int) { for (i in 0 until b.capacity()) b.put(i, v.toByte()) }

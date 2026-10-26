import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.ui.ExperimentalComposeUiApi
import androidx.compose.ui.ImageComposeScene
import androidx.compose.ui.Modifier
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kurz.media.Capture
import kurz.media.MediaCapture
import kurz.media.ScreenQuality
import kurz.media.ScreenResolution
import kurz.media.rememberMediaCapture
import org.jetbrains.skia.EncodedImageFormat
import kotlin.system.exitProcess

// Verificação da troca de microfone e câmara (tarefa :app:deviceCheck). Abre mic e câmara reais, não o ecrã.
@OptIn(ExperimentalComposeUiApi::class)
fun main() {
    lateinit var cap: MediaCapture
    val scene = ImageComposeScene(640, 360) { cap = rememberMediaCapture(); cap.CameraPreview(Modifier.fillMaxSize()) }
    scene.render(0)
    var failures = 0
    fun check(ok: Boolean, what: String) { println((if (ok) "ok   " else "FALHA") + " $what"); if (!ok) failures++ }
    val screenScene by lazy { ImageComposeScene(640, 360) { cap.ScreenPreview(Modifier.fillMaxSize()) } }
    fun hasScreenFrame() = screenScene.render(System.nanoTime()).encodeToData(EncodedImageFormat.PNG)!!.bytes.size > 20_000
    // PNG de uma imagem em branco tem ~1 KB; com um frame de câmara passa dos 20 KB
    fun hasFrame() = scene.render(System.nanoTime()).encodeToData(EncodedImageFormat.PNG)!!.bytes.size > 20_000

    runBlocking {
        cap.refreshDevices(); delay(1_000)
        println("microfones: ${cap.micDevices.map { it.name }}")
        println("câmaras:    ${cap.cameraDevices.map { it.name }}")
        check(cap.micDevices.isNotEmpty(), "há microfones")
        check(cap.cameraDevices.isNotEmpty(), "há câmaras")

        check(cap.startMic(), "mic liga (notice=${cap.notice})")
        for (d in cap.micDevices + null) {
            cap.selectMic(d?.id); delay(1_000)
            check(cap.mic == Capture.On && cap.notice == null && cap.micDeviceId == d?.id, "mic -> ${d?.name ?: "padrão do sistema"} (estado=${cap.mic}, notice=${cap.notice})")
        }
        cap.stopMic()

        // 1) cada câmara ligada direto (sem troca em quente). Sem imagem = problema do dispositivo (aviso), e fica de fora da troca.
        val working = mutableListOf<kurz.media.Device>()
        for (d in cap.cameraDevices) {
            cap.notice = null
            cap.selectCamera(d.id)
            check(cap.startCamera(), "câmara ${d.name} liga direto (notice=${cap.notice})")
            delay(4_500)
            if (cap.camera == Capture.On && hasFrame()) { working += d; println("ok    câmara ${d.name} dá imagem") }
            else println("AVISO câmara ${d.name} não dá imagem — aviso na app: ${cap.notice}")
            cap.stopCamera(); delay(500)
        }
        check(working.isNotEmpty(), "pelo menos uma câmara dá imagem")

        // 2) troca em quente entre as que funcionam: o botão não pode passar por Off nem haver aviso, e a nova tem de dar imagem
        if (working.size >= 2) {
            cap.notice = null
            cap.selectCamera(working[0].id)
            check(cap.startCamera(), "câmara ${working[0].name} liga (notice=${cap.notice})")
            delay(4_000)
            for (d in working.drop(1) + working.take(1)) {
                cap.selectCamera(d.id); delay(1_000)
                check(cap.camera == Capture.On, "câmara -> ${d.name}: o estado fica On durante a troca")
                delay(3_500)
                check(cap.camera == Capture.On && cap.notice == null && cap.cameraDeviceId == d.id && hasFrame(), "câmara -> ${d.name} dá imagem (estado=${cap.camera}, notice=${cap.notice})")
            }
        } else println("AVISO só ${working.size} câmara(s) funciona(m): troca em quente não testada")
        cap.stopCamera()

        // 3) o que partilhar: ecrãs e janelas listados; cada um liga direto e a troca em quente dá imagem (no macOS pede a permissão de gravação do ecrã ao terminal)
        cap.refreshScreenSources(); delay(2_000)
        println("partilha:   ${cap.screenSources.map { (if (it.window) "janela " else "ecrã ") + it.name }}")
        check(cap.screenSources.any { !it.window }, "há ecrãs para partilhar")
        val picks = cap.screenSources.filter { !it.window }.take(1) + cap.screenSources.filter { it.window }.take(2)
        for ((i, src) in picks.withIndex()) {
            cap.notice = null
            cap.selectScreen(src.id)   // 1.ª: liga a partilha; as seguintes: troca em quente
            if (i == 0) check(cap.startScreen(), "partilha liga com ${src.name} (notice=${cap.notice})")
            delay(3_500)
            check(cap.screen == Capture.On && cap.notice == null && cap.screenSourceId == src.id, "partilha -> ${src.name}: fica On sem aviso (estado=${cap.screen}, notice=${cap.notice})")
            // sem imagem = falta a permissão de gravação do ecrã a quem lançou isto (no macOS o libwebrtc não dá frames nenhuns): aviso, não falha
            if (hasScreenFrame()) println("ok    partilha -> ${src.name} dá imagem") else println("AVISO partilha -> ${src.name} sem imagem (permissão de gravação do ecrã?)")
        }
        // qualidade: com a partilha ligada, mudar resolução/quadros reabre a fonte em quente (o estado não passa por Off)
        for (q in listOf(ScreenQuality.PairCoding, ScreenQuality(ScreenResolution.Source, 60), ScreenQuality(ScreenResolution.P720, 5), ScreenQuality.Normal)) {
            cap.notice = null
            cap.selectScreenQuality(q); delay(1_000)
            check(cap.screen == Capture.On, "qualidade -> ${q.label}: o estado fica On durante a troca")
            delay(2_500)
            check(cap.screen == Capture.On && cap.notice == null && cap.screenQuality == q, "qualidade -> ${q.label}: fica On sem aviso (estado=${cap.screen}, notice=${cap.notice})")
        }
        cap.stopScreen()
    }
    cap.release(); scene.close(); screenScene.close()
    println(if (failures == 0) "OK" else "$failures FALHA(S)")
    exitProcess(if (failures == 0) 0 else 1)
}

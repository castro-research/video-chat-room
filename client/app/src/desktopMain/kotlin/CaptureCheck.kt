import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.ExperimentalComposeUiApi
import androidx.compose.ui.ImageComposeScene
import androidx.compose.ui.Modifier
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kurz.media.MediaCapture
import kurz.media.rememberMediaCapture
import org.jetbrains.skia.EncodedImageFormat
import java.io.File
import kotlin.system.exitProcess

// Verificação manual da captura no desktop (ver tarefa :app:captureCheck).
@OptIn(ExperimentalComposeUiApi::class)
fun main(args: Array<String>) {
    val dir = File(args[0]).also { it.mkdirs() }
    lateinit var cap: MediaCapture
    var preview by mutableStateOf("camera")
    val scene = ImageComposeScene(640, 360) {
        cap = rememberMediaCapture()
        if (preview == "camera") cap.CameraPreview(Modifier.fillMaxSize()) else cap.ScreenPreview(Modifier.fillMaxSize())
    }
    fun snapshot(name: String) {
        val png = scene.render(System.nanoTime()).encodeToData(EncodedImageFormat.PNG)!!.bytes
        File(dir, "$name.png").writeBytes(png)
    }
    scene.render(0)
    runBlocking {
        println("mic    start=${cap.startMic()} state=${cap.mic} notice=${cap.notice}")
        var peak = 0f
        repeat(30) { delay(100); peak = maxOf(peak, cap.micLevel) }
        println("mic    peak level in 3s = $peak (0 = silêncio — fala ou verifica a permissão)")
        cap.stopMic()

        println("camera start=${cap.startCamera()} state=${cap.camera} notice=${cap.notice}")
        delay(4000)
        preview = "camera"; snapshot("camera")
        println("camera state=${cap.camera} -> ${File(dir, "camera.png")}")
        cap.stopCamera()

        println("screen start=${cap.startScreen()} state=${cap.screen} notice=${cap.notice}")
        delay(1500)
        preview = "screen"; snapshot("screen")
        println("screen state=${cap.screen} -> ${File(dir, "screen.png")}")
        cap.stopScreen()
    }
    cap.release(); scene.close()
    exitProcess(0)
}

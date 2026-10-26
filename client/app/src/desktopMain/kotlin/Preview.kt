import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.ui.ExperimentalComposeUiApi
import androidx.compose.ui.ImageComposeScene
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.Density
import kurz.core.AppState
import kurz.media.Capture
import kurz.media.MediaCapture
import kurz.ui.*
import org.jetbrains.skia.EncodedImageFormat
import java.io.File

/** Captura de mentira: as renderizações não abrem microfone/câmara/ecrã. */
private class FakeCapture : MediaCapture() {
    override suspend fun startMic() = true.also { mic = Capture.On; micLevel = 0.6f }
    override suspend fun startCamera() = true.also { camera = Capture.On }
    override suspend fun startScreen() = true.also { screen = Capture.On }
    override fun stopMic() { mic = Capture.Off }
    override fun stopCamera() { camera = Capture.Off }
    override fun stopScreen() { screen = Capture.Off }
    @Composable override fun CameraPreview(modifier: Modifier) = Box(modifier.background(Brush.linearGradient(listOf(Color(0xFF6C7FC9), Color(0xFF3A3F57)))))
    @Composable override fun ScreenPreview(modifier: Modifier) = Box(modifier.background(Brush.linearGradient(listOf(Color(0xFF2B2D31), Color(0xFF5865F2)))))
    override fun release() {}
}

// Renderiza um painel para PNG: ./gradlew renderPane -Ppane=chat -Pdark=true -Pw=800 -Ph=900 -Pout=/caminho/x.png
// panes: app (lista) | appchat | appvoice | appvoicecam | appmembers (ecrã inteiro, responsivo ao -Pw) | sidebar | chat | members | voice
@OptIn(ExperimentalComposeUiApi::class)
fun main(args: Array<String>) {
    val (pane, dark, w, h, out) = args
    val scale = args.getOrNull(5)?.toFloat() ?: 1f
    val state = AppState(dark.toBoolean())
    val capture = FakeCapture()
    if (pane == "voice" || pane == "appvoice" || pane == "appvoicecam") state.joinVoice("general-talk")
    if (pane == "appvoicecam") { state.camOn = true; state.sharing = true }
    if (pane == "appchat" || pane == "appmembers") state.openText("general-text")
    if (pane == "appmembers") state.membersOverlay = true
    val scene = ImageComposeScene((w.toInt() * scale).toInt(), (h.toInt() * scale).toInt(), Density(scale)) {
        if (pane.startsWith("app")) App(state, capture) else CompositionLocalProvider(LocalMediaCapture provides capture) { KurzTheme(state.dark) {
            val m = Modifier.fillMaxSize()
            Box(Modifier.fillMaxSize().background(p.main)) {
                when (pane) {
                    "sidebar" -> ChannelSidebar(state, m)
                    "chat" -> ChatPane(state, m)
                    "members" -> MembersPanel(state, m)
                    "voice" -> VoiceRoom(state, m)
                }
            }
        } }
    }
    repeat(3) { scene.render(it * 16_000_000L) }   // deixa efeitos/layout assentarem
    val png = scene.render(64_000_000L).encodeToData(EncodedImageFormat.PNG)!!.bytes
    File(out).also { it.absoluteFile.parentFile.mkdirs() }.writeBytes(png)
    scene.close()
    println("wrote $out")
}

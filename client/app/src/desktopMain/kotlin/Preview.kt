import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.ui.ExperimentalComposeUiApi
import androidx.compose.ui.ImageComposeScene
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.Density
import kurz.core.AppState
import kurz.ui.*
import org.jetbrains.skia.EncodedImageFormat
import java.io.File

// Renderiza um painel para PNG: ./gradlew renderPane -Ppane=chat -Pdark=true -Pw=800 -Ph=900 -Pout=/caminho/x.png
// panes: app (lista) | appchat | appvoice | appmembers (ecrã inteiro, responsivo ao -Pw) | sidebar | chat | members | voice
@OptIn(ExperimentalComposeUiApi::class)
fun main(args: Array<String>) {
    val (pane, dark, w, h, out) = args
    val scale = args.getOrNull(5)?.toFloat() ?: 1f
    val state = AppState(dark.toBoolean())
    if (pane == "voice" || pane == "appvoice") state.joinVoice("general-talk")
    if (pane == "appchat" || pane == "appmembers") state.openText("general-text")
    if (pane == "appmembers") state.membersOverlay = true
    val scene = ImageComposeScene((w.toInt() * scale).toInt(), (h.toInt() * scale).toInt(), Density(scale)) {
        if (pane.startsWith("app")) App(state) else KurzTheme(state.dark) {
            val m = Modifier.fillMaxSize()
            Box(Modifier.fillMaxSize().background(p.main)) {
                when (pane) {
                    "sidebar" -> ChannelSidebar(state, m)
                    "chat" -> ChatPane(state, m)
                    "members" -> MembersPanel(state, m)
                    "voice" -> VoiceRoom(state, m)
                }
            }
        }
    }
    repeat(3) { scene.render(it * 16_000_000L) }   // deixa efeitos/layout assentarem
    val png = scene.render(64_000_000L).encodeToData(EncodedImageFormat.PNG)!!.bytes
    File(out).also { it.absoluteFile.parentFile.mkdirs() }.writeBytes(png)
    scene.close()
    println("wrote $out")
}

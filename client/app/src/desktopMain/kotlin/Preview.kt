import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.ui.ExperimentalComposeUiApi
import androidx.compose.ui.ImageComposeScene
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.pointer.PointerEventType
import androidx.compose.ui.unit.Density
import kurz.core.AppState
import kurz.media.Capture
import kurz.media.Device
import kurz.media.MediaCapture
import kurz.media.Participant
import kurz.media.Room
import kurz.media.RoomClient
import kurz.media.ScreenSource
import kurz.ui.*
import org.jetbrains.skia.EncodedImageFormat
import kotlinx.coroutines.awaitCancellation
import java.io.File

/** Captura de mentira: as renderizações não abrem microfone/câmara/ecrã. */
private class FakeCapture : MediaCapture() {
    init {
        micDevices = listOf(Device("m1", "MacBook Pro Microphone"), Device("m2", "MateView GT"))
        cameraDevices = listOf(Device("c1", "MacBook Pro Camera"), Device("c2", "PC-LM1E Camera"))
        screenSources = listOf(ScreenSource("s1", "Ecrã 1", false), ScreenSource("w1", "Terminal — zsh", true), ScreenSource("w2", "Figma — Kurz", true))
    }
    override val canPickScreen get() = true
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

/** Salas de mentira: lista fixa, sem rede; o vídeo remoto é um gradiente. */
private class FakeRooms(private val slow: Boolean = false, empty: Boolean = false) : RoomClient() {
    private val demo = Room("k3x9q2", "Daily", listOf(
        Participant(userId, "Nainesh Selarka", mic = true), Participant("u2", "Vrinda", mic = true, camera = true, screen = true),
        Participant("u3", "Madhav"),
    ))
    init { rooms = if (empty) emptyList() else listOf(demo, Room("p0a1b2", "Design review")); if (slow) room = demo }  // slow: o SFU ainda diz que estamos na sala antiga
    override suspend fun list(base: String) {}
    override suspend fun ping(base: String) = !base.contains("kurz.fyi")  // o de fábrica do release ainda não responde
    override suspend fun create(base: String, name: String) = null
    override suspend fun join(base: String, id: String, name: String): Boolean { if (slow) awaitCancellation(); room = demo; return true }
    override suspend fun poll(base: String) {}
    override fun leave() {}
    @Composable override fun RemoteVideo(userId: String, source: String, modifier: Modifier) =
        Box(modifier.background(Brush.linearGradient(listOf(Color(0xFFF08F8F), Color(0xFF3F8FB5)))))
}

// Renderiza um painel para PNG: ./gradlew renderPane -Ppane=chat -Pdark=true -Pw=800 -Ph=900 -Pout=/caminho/x.png
// panes: app (lista) | appchat | appvoice | appvoicecam | approom (sala real de mentira) | approomfocus (partilha em destaque) | approomclick (clica num mosaico) | approomswitch (a trocar de sala, entrada pendente) | appservers (modal de servidores) | appcreate (modal de criar canal) | appshare (modal de partilha de ecrã, sem miniaturas) | appempty (servidor sem salas) | appmembers (ecrã inteiro, responsivo ao -Pw) | sidebar | chat | members | voice
@OptIn(ExperimentalComposeUiApi::class)
fun main(args: Array<String>) {
    val (pane, dark, w, h, out) = args
    val scale = args.getOrNull(5)?.toFloat() ?: 1f
    val state = AppState(dark.toBoolean())
    val capture = FakeCapture()
    val rooms = FakeRooms(slow = pane == "approomswitch", empty = pane == "appempty")
    if (pane.startsWith("approom") && pane != "approomswitch") state.joinRoom("k3x9q2")
    if (pane == "approomfocus") state.focus = "screen:u2"  // a partilha da Vrinda em destaque
    if (pane == "appservers") { state.addServer("cord.kurz.fyi"); state.selectServer(state.servers.first()); state.serversOpen = true }
    if (pane == "appcreate") state.createRoomOpen = true
    if (pane == "appshare") state.sharePickerOpen = true
    if (pane == "approomswitch") state.joinRoom("p0a1b2")  // clicou noutra sala e a entrada ainda não acabou
    if (pane == "voice" || pane == "appvoice" || pane == "appvoicecam") state.joinVoice("general-talk")
    if (pane == "appvoicecam") { state.camOn = true; state.sharing = true }
    if (pane == "appchat" || pane == "appmembers") state.openText("general-text")
    if (pane == "appmembers") state.membersOverlay = true
    val scene = ImageComposeScene((w.toInt() * scale).toInt(), (h.toInt() * scale).toInt(), Density(scale)) {
        if (pane.startsWith("app")) App(state, capture, rooms) else CompositionLocalProvider(LocalMediaCapture provides capture, LocalRooms provides rooms) { KurzTheme(state.dark) {
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
    if (pane == "approomclick") {  // clica no mosaico da Vrinda (2.º da grelha a 1440x900) e vê se fica em destaque
        val at = Offset(843f, 185f) * scale
        scene.sendPointerEvent(PointerEventType.Move, at); scene.sendPointerEvent(PointerEventType.Press, at); scene.sendPointerEvent(PointerEventType.Release, at)
        repeat(3) { scene.render((100 + it * 16) * 1_000_000L) }
        println("focus = ${state.focus}")
    }
    val png = scene.render(if (pane == "approomclick") 200_000_000L else 64_000_000L).encodeToData(EncodedImageFormat.PNG)!!.bytes
    File(out).also { it.absoluteFile.parentFile.mkdirs() }.writeBytes(png)
    scene.close()
    println("wrote $out")
}

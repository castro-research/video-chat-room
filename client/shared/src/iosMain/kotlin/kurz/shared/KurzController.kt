package kurz.shared

import androidx.compose.runtime.snapshotFlow
import androidx.compose.runtime.snapshots.Snapshot
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.collect
import kotlinx.coroutines.launch
import kurz.core.AppState
import kurz.core.serverLabel
import kurz.media.Capture
import kurz.media.IosMedia
import kurz.media.Participant
import kurz.media.Room
import kurz.media.peopleIn
import kurz.media.shownRoom
import kurz.media.syncCapture
import kurz.media.syncHealth
import kurz.media.syncRoom
import kurz.media.syncRoomList
import kurz.media.user
import platform.UIKit.UIView
import kotlin.concurrent.AtomicInt

// O que o SwiftUI vê: instantâneos imutáveis do estado (KState) e acções. A lógica é a do Compose (kurz.media.Session), sem UI.

data class KPerson(val id: String, val name: String, val hue: Float, val isMe: Boolean, val mic: Boolean, val camera: Boolean, val screen: Boolean, val volume: Float)

data class KRoom(val id: String, val name: String, val people: List<KPerson>, val connected: Boolean)

/** [health]: 1 = responde ao /healthz, 0 = não responde, -1 = ainda por saber. */
data class KServer(val url: String, val label: String, val health: Int, val canRemove: Boolean)

data class KState(
    val myName: String,
    val serverUrl: String,
    val serverLabel: String,
    val serverHue: Float,
    val serverHealth: Int,
    val servers: List<KServer>,
    val rooms: List<KRoom>,
    val listError: String?,
    /** Há uma sala ligada (ou a ligar), esteja ou não a ser mostrada. */
    val inRoom: Boolean,
    /** A sala está no ecrã (senão, lista de salas com a barra "Ver sala"). */
    val roomVisible: Boolean,
    val roomId: String?,
    val roomName: String?,
    val people: List<KPerson>,
    val muted: Boolean,
    val camOn: Boolean,
    val micLevel: Float,
    val reconnecting: Boolean,
    val canSetVolume: Boolean,
    val notice: String?,
    /** `<userId>:<source>` das faixas de vídeo que já chegaram (a própria câmara incluída). */
    val videoKeys: List<String>,
)

fun interface StateSubscription { fun cancel() }

class KurzController {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main)
    private val app = AppState()
    private val media = IosMedia()
    private val rooms get() = media.rooms
    private val capture get() = media.capture

    init {
        pumpSnapshots(scope)
        scope.launch { syncCapture(app, capture) }
        scope.launch { syncRoom(app, rooms) }
        scope.launch { syncHealth(app, rooms) }
        scope.launch { syncRoomList(app, rooms) }
    }

    /** Chama [onState] já com o estado actual e depois a cada mudança, na thread principal. */
    fun observe(onState: (KState) -> Unit): StateSubscription {
        val job = scope.launch { snapshotFlow { build() }.collect { onState(it) } }
        return StateSubscription { job.cancel() }
    }

    private fun person(pt: Participant): KPerson {
        val u = pt.user(app.me, rooms.userId)
        return KPerson(pt.userId, u.name, u.hue, pt.userId == rooms.userId, pt.mic, pt.camera, pt.screen, rooms.volume(pt.userId))
    }

    private fun build(): KState {
        val s = app; val r = rooms
        val real: Room? = shownRoom(s, r)
        val health = { url: String -> when (s.health[url]) { true -> 1; false -> 0; null -> -1 } }
        return KState(
            myName = s.me.name,
            serverUrl = s.managerUrl,
            serverLabel = s.server.name,
            serverHue = s.server.hue,
            serverHealth = health(s.managerUrl),
            servers = s.servers.map { KServer(it, serverLabel(it), health(it), it != s.servers.first()) },
            rooms = r.rooms.map { room -> KRoom(room.id, room.name, peopleIn(room, s, r).map(::person), s.roomId == room.id) },
            listError = r.listError,
            inRoom = s.roomId != null,
            roomVisible = s.roomId != null && s.contentOpen && s.showVoice,
            roomId = s.roomId,
            roomName = real?.name ?: s.roomId,
            people = real?.let { peopleIn(it, s, r) }.orEmpty().map(::person),
            muted = s.muted,
            camOn = s.camOn,
            micLevel = if (capture.mic == Capture.On && !s.muted) capture.micLevel else 0f,
            reconnecting = r.reconnecting,
            canSetVolume = r.canSetVolume,
            notice = r.notice ?: capture.notice,
            videoKeys = media.videoKeys(),
        )
    }

    // --- servidores ---
    fun setServersOpen(open: Boolean) { app.serversOpen = open }
    fun selectServer(url: String) = app.selectServer(url)
    /** false se não for um endereço. */
    fun addServer(input: String): Boolean = app.addServer(input)
    fun removeServer(url: String) = app.removeServer(url)

    // --- salas ---
    /** Cria a sala e entra nela; [done] diz se correu bem (senão há um aviso em [KState.notice]). */
    fun createRoom(name: String, done: (Boolean) -> Unit) {
        val n = name.trim().ifEmpty { return done(false) }
        scope.launch { val room = rooms.create(app.managerUrl, n); room?.let { app.joinRoom(it.id) }; done(room != null) }
    }
    fun joinRoom(id: String) = app.joinRoom(id)
    /** Mostra a sala já ligada. */
    fun showRoom() = app.openVoiceRoom()
    /** Volta à lista sem sair da sala. */
    fun hideRoom() = app.closeContent()
    fun leaveRoom() = app.leaveVoice()

    // --- controlos ---
    fun toggleMute() { app.muted = !app.muted }
    fun toggleCamera() { app.camOn = !app.camOn }
    fun setVolume(userId: String, volume: Float) = rooms.setVolume(userId, volume)
    fun dismissNotice() { rooms.notice = null; capture.notice = null }

    // --- vídeo: [view] é um RTCMTLVideoView; chamar de novo (a cada mudança de [KState.videoKeys]) liga a faixa que entretanto chegou ---
    fun bindVideo(userId: String, source: String, view: UIView) = media.bindVideo(userId, source, view)
    fun unbindVideo(view: UIView) = media.unbindVideo(view)

    fun release() { scope.cancel(); media.release() }
}

private var pumping = false

/**
 * Sem o Compose a correr ninguém avisa os snapshotFlow de que o estado mudou: como o GlobalSnapshotManager do Compose,
 * agenda um sendApplyNotifications por cada escrita no snapshot global.
 */
private fun pumpSnapshots(scope: CoroutineScope) {
    if (pumping) return
    pumping = true
    val scheduled = AtomicInt(0)
    Snapshot.registerGlobalWriteObserver {
        if (scheduled.compareAndSet(0, 1)) scope.launch { scheduled.value = 0; Snapshot.sendApplyNotifications() }
    }
}

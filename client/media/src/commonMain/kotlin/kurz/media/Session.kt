package kurz.media

import androidx.compose.runtime.snapshotFlow
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.launch
import kurz.core.AppState
import kurz.core.Status
import kurz.core.User
import kotlin.time.Duration.Companion.seconds
import kotlin.time.TimeMark
import kotlin.time.TimeSource

// A ligação entre o estado (AppState) e a mídia/salas (MediaCapture, RoomClient), sem UI: o Compose chama-a de um
// LaunchedEffect (ui/Capture.kt) e o iOS nativo de um CoroutineScope (shared/KurzController).

// UI otimista: ao clicar numa sala a UI mostra-a já, com o que se sabe (a última listagem) e o próprio lá dentro, em vez de
// esperar o manager e o SFU (e o poll de 2–3 s). Se a entrada falhar, syncRoom tira o utilizador da sala e mostra o aviso.

/** A sala real ligada (ou a ligar) como a UI a mostra: a do SFU se já respondeu, senão a da listagem; null se o código nem está listado. */
fun shownRoom(s: AppState, r: RoomClient): Room? =
    s.roomId?.let { id -> r.room?.takeIf { it.id == id } ?: r.rooms.firstOrNull { it.id == id } }

/** Quem a UI mostra em [room]: na sala ligada o próprio entra já (com o estado dos botões), nas outras sai já. */
fun peopleIn(room: Room, s: AppState, r: RoomClient): List<Participant> {
    val known = if (s.roomId == room.id) shownRoom(s, r) ?: room else room
    val others = known.participants.filter { it.userId != r.userId }
    if (s.roomId != room.id) return others
    return listOf(Participant(r.userId, s.me.name, mic = !s.muted, camera = s.camOn, screen = s.sharing)) + others
}

/** O participante como User da UI; o próprio é [me], para a pré-visualização local e o "(tu)". */
fun Participant.user(me: User, myId: String) =
    if (userId == myId) me else User(userId, name, Status.Online, hue = (userId.hashCode() and 0x7fffffff) % 360f)

/** Liga os botões (muted/camOn/sharing) à captura real: só captura com a sala ligada, e um pedido recusado repõe o botão. Não termina. */
suspend fun syncCapture(s: AppState, c: MediaCapture): Unit = coroutineScope {
    launch { snapshotFlow { s.inVoice to s.muted }.collectLatest { (joined, muted) -> if (joined && !muted) { if (!c.startMic()) s.muted = true } else c.stopMic() } }
    launch { snapshotFlow { s.inVoice to s.camOn }.collectLatest { (joined, on) -> if (joined && on) { if (!c.startCamera()) s.camOn = false } else c.stopCamera() } }
    launch { snapshotFlow { s.inVoice to s.sharing }.collectLatest { (joined, on) -> if (joined && on) { if (!c.startScreen()) s.sharing = false } else c.stopScreen() } }
    // a câmara também pode falhar depois de arrancar (ex.: dispositivo ocupado); a partilha pode ser parada pelo sistema
    launch { snapshotFlow { c.camera }.collectLatest { if (it == Capture.Off && s.camOn) s.camOn = false } }
    c.onScreenStopped = { s.sharing = false }
    try { awaitCancellation() } finally { c.onScreenStopped = null }
}

/**
 * Liga AppState.roomId à sala real: entra, actualiza os participantes a cada 2 s e sai. Se a ligação ao SFU cai fica na sala e tenta
 * voltar a cada 2 s (a UI mostra "a reconectar" e o desktop toca um som); só desiste, e sai, passados 30 s sem conseguir.
 * Falhou a primeira entrada ou o utilizador saiu = volta para fora da sala. Não termina.
 */
suspend fun syncRoom(s: AppState, r: RoomClient) {
    snapshotFlow { s.roomId }.collectLatest { id ->
        if (id == null) return@collectLatest
        try {
            if (r.join(s.managerUrl, id, s.me.name)) {
                var lostSince: TimeMark? = null
                while (r.room != null) {
                    delay(2_000)
                    if (!r.reconnecting) { lostSince = null; r.poll(s.managerUrl); continue }
                    val since = lostSince ?: TimeSource.Monotonic.markNow().also { lostSince = it }
                    if (!r.reconnect(s.managerUrl, id, s.me.name) && since.elapsedNow() > 30.seconds) {
                        r.notice = "Sem ligação ao servidor há mais de 30 s. Saíste da sala."
                        r.leave()
                    }
                }
            }
            if (s.roomId == id) s.leaveVoice()
        } finally {
            r.leave()
        }
    }
}

/** Sonda o /healthz a cada 5 s: o servidor ligado sempre, os outros da lista só enquanto a modal está aberta. Não termina. */
suspend fun syncHealth(s: AppState, r: RoomClient) {
    snapshotFlow { Triple(s.managerUrl, s.serversOpen, s.servers.size) }.collectLatest {
        while (true) {
            val urls = if (s.serversOpen) s.servers.toList() else listOf(s.managerUrl)
            coroutineScope { urls.forEach { u -> launch { s.health[u] = r.ping(u) } } }
            delay(5_000)
        }
    }
}

/** Lista as salas do servidor ligado a cada 3 s. Não termina. */
suspend fun syncRoomList(s: AppState, r: RoomClient) {
    snapshotFlow { s.managerUrl }.collectLatest { url ->
        delay(300)  // espera que se acabe de escrever a URL
        while (true) { r.list(url); delay(3_000) }
    }
}

package kurz.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.staticCompositionLocalOf
import kotlinx.coroutines.delay
import kurz.core.AppState
import kurz.media.Capture
import kurz.media.MediaCapture
import kurz.media.Participant
import kurz.media.Room
import kurz.media.RoomClient

val LocalMediaCapture = staticCompositionLocalOf<MediaCapture> { error("MediaCapture não fornecido") }
val LocalRooms = staticCompositionLocalOf<RoomClient> { error("RoomClient não fornecido") }

/** Liga os botões (muted/camOn/sharing) à captura real: só captura com a sala ligada, e um pedido recusado repõe o botão. */
@Composable
fun CaptureSync(s: AppState, c: MediaCapture) {
    val joined = s.inVoice
    LaunchedEffect(joined, s.muted) { if (joined && !s.muted) { if (!c.startMic()) s.muted = true } else c.stopMic() }
    LaunchedEffect(joined, s.camOn) { if (joined && s.camOn) { if (!c.startCamera()) s.camOn = false } else c.stopCamera() }
    LaunchedEffect(joined, s.sharing) { if (joined && s.sharing) { if (!c.startScreen()) s.sharing = false } else c.stopScreen() }
    // a câmara também pode falhar depois de arrancar (ex.: dispositivo ocupado); a partilha pode ser parada pelo sistema
    LaunchedEffect(c.camera) { if (c.camera == Capture.Off && s.camOn) s.camOn = false }
    DisposableEffect(c) { c.onScreenStopped = { s.sharing = false }; onDispose { c.onScreenStopped = null } }
}

// UI otimista: ao clicar numa sala a UI mostra-a já, com o que se sabe (a última listagem) e o próprio lá dentro, em vez de
// esperar o manager e o SFU (e o poll de 2–3 s). Se a entrada falhar, RoomSync tira o utilizador da sala e mostra o aviso.

/** A sala real ligada (ou a ligar) como a UI a mostra: a do SFU se já respondeu, senão a da listagem; null se o código nem está listado. */
internal fun shownRoom(s: AppState, r: RoomClient): Room? =
    s.roomId?.let { id -> r.room?.takeIf { it.id == id } ?: r.rooms.firstOrNull { it.id == id } }

/** Quem a UI mostra em [room]: na sala ligada o próprio entra já (com o estado dos botões), nas outras sai já. */
internal fun peopleIn(room: Room, s: AppState, r: RoomClient): List<Participant> {
    val known = if (s.roomId == room.id) shownRoom(s, r) ?: room else room
    val others = known.participants.filter { it.userId != r.userId }
    if (s.roomId != room.id) return others
    return listOf(Participant(r.userId, s.me.name, mic = !s.muted, camera = s.camOn, screen = s.sharing)) + others
}

/** Liga AppState.roomId à sala real: entra, actualiza os participantes a cada 2 s e sai; falhou ou caiu = volta para fora da sala. */
@Composable
fun RoomSync(s: AppState, r: RoomClient) {
    LaunchedEffect(s.roomId) {
        val id = s.roomId ?: return@LaunchedEffect
        try {
            if (r.join(s.managerUrl, id, s.me.name)) while (r.room != null) { delay(2_000); r.poll(s.managerUrl) }
            if (s.roomId == id) s.leaveVoice()
        } finally {
            r.leave()
        }
    }
}

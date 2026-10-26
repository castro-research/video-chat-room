package kurz.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.staticCompositionLocalOf
import kurz.core.AppState
import kurz.media.MediaCapture
import kurz.media.RoomClient
import kurz.media.syncCapture
import kurz.media.syncHealth
import kurz.media.syncRoom
import kurz.media.syncRoomList

val LocalMediaCapture = staticCompositionLocalOf<MediaCapture> { error("MediaCapture não fornecido") }
val LocalRooms = staticCompositionLocalOf<RoomClient> { error("RoomClient não fornecido") }

// A lógica está em kurz.media.Session (partilhada com o iOS nativo); aqui só se prende ao ciclo de vida da composição.

@Composable
fun CaptureSync(s: AppState, c: MediaCapture) = LaunchedEffect(s, c) { syncCapture(s, c) }

@Composable
fun RoomSync(s: AppState, r: RoomClient) = LaunchedEffect(s, r) { syncRoom(s, r) }

@Composable
fun HealthSync(s: AppState, r: RoomClient) = LaunchedEffect(s, r) { syncHealth(s, r) }

@Composable
fun RoomListSync(s: AppState, r: RoomClient) = LaunchedEffect(s, r) { syncRoomList(s, r) }

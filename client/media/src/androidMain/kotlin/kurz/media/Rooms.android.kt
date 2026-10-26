package kurz.media

import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember

@Composable
actual fun rememberRoomClient(capture: MediaCapture): RoomClient = remember { UnsupportedRooms() }

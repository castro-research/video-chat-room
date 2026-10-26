package kurz.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.staticCompositionLocalOf
import kurz.core.AppState
import kurz.media.Capture
import kurz.media.MediaCapture

val LocalMediaCapture = staticCompositionLocalOf<MediaCapture> { error("MediaCapture não fornecido") }

/** Liga os botões (muted/camOn/sharing) à captura real: só captura com a sala ligada, e um pedido recusado repõe o botão. */
@Composable
fun CaptureSync(s: AppState, c: MediaCapture) {
    val joined = s.voiceChannelId != null
    LaunchedEffect(joined, s.muted) { if (joined && !s.muted) { if (!c.startMic()) s.muted = true } else c.stopMic() }
    LaunchedEffect(joined, s.camOn) { if (joined && s.camOn) { if (!c.startCamera()) s.camOn = false } else c.stopCamera() }
    LaunchedEffect(joined, s.sharing) { if (joined && s.sharing) { if (!c.startScreen()) s.sharing = false } else c.stopScreen() }
    // a câmara também pode falhar depois de arrancar (ex.: dispositivo ocupado); a partilha pode ser parada pelo sistema
    LaunchedEffect(c.camera) { if (c.camera == Capture.Off && s.camOn) s.camOn = false }
    DisposableEffect(c) { c.onScreenStopped = { s.sharing = false }; onDispose { c.onScreenStopped = null } }
}

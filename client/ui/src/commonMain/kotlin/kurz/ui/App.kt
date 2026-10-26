package kurz.ui

import kurz.core.*
import kurz.media.MediaCapture
import kurz.media.RoomClient
import kurz.media.rememberMediaCapture
import kurz.media.rememberRoomClient

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.ExperimentalComposeUiApi
import androidx.compose.ui.Modifier
import androidx.compose.ui.backhandler.BackHandler
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

// Montagem responsiva (ver WindowClass):
//  Medium/Expanded  [sidebar 248] [sala de voz | vazio]
//  Compact          um painel de cada vez: lista de salas → sala
// O chat e a lista de membros (ChatPane, MembersPanel) usam dados de exemplo do Model e por isso não aparecem: só há salas reais.
@OptIn(ExperimentalComposeUiApi::class)
@Composable
fun App(state: AppState = remember { AppState() }, capture: MediaCapture = rememberMediaCapture(), rooms: RoomClient = rememberRoomClient(capture)) {
    CaptureSync(state, capture)
    RoomSync(state, rooms)
    HealthSync(state, rooms)
    KurzTheme(state.dark) {
        BoxWithConstraints(Modifier.fillMaxSize()) {
            val wc = windowClassFor(maxWidth)
            // a cor por trás das barras do sistema acompanha o ecrã visível
            val bg = if (wc == WindowClass.Compact && !state.contentOpen) p.sidebar else p.main
            Box(Modifier.fillMaxSize().background(bg).windowInsetsPadding(WindowInsets.safeDrawing)) {
            CompositionLocalProvider(LocalWindowClass provides wc, LocalMediaCapture provides capture, LocalRooms provides rooms) {
                val full = Modifier.fillMaxSize()
                if (wc == WindowClass.Compact) {
                    BackHandler(enabled = state.contentOpen) { state.closeContent() }
                    if (state.contentOpen && state.showVoice) VoiceRoom(state, full) else ChannelSidebar(state, full)
                } else {
                    Row(full) {
                        ChannelSidebar(state, Modifier.width(248.dp).fillMaxHeight())
                        val mainMod = Modifier.weight(1f).fillMaxHeight()
                        if (state.showVoice) VoiceRoom(state, mainMod) else NoRoom(mainMod)
                    }
                }
                ServerDialog(state)
                CreateRoomDialog(state)
                SharePicker(state)
            }
            }
        }
    }
}

/** Painel principal sem sala ligada. */
@Composable
private fun NoRoom(modifier: Modifier) {
    Box(modifier.background(p.main), Alignment.Center) {
        Text("Escolhe uma sala à esquerda para entrar.", color = p.textFaint, fontSize = 14.sp, textAlign = TextAlign.Center, modifier = Modifier.padding(24.dp))
    }
}

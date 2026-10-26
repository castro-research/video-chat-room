package kurz.ui

import kurz.core.*
import kurz.media.MediaCapture
import kurz.media.rememberMediaCapture

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.*
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.ExperimentalComposeUiApi
import androidx.compose.ui.Modifier
import androidx.compose.ui.backhandler.BackHandler
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp

// Montagem responsiva (ver WindowClass):
//  Expanded  [sidebar 248] [chat|voz] [membros 288]
//  Medium    [sidebar 248] [chat|voz]            + gaveta de membros
//  Compact   um painel de cada vez: lista de canais → chat/sala, gaveta de membros
@OptIn(ExperimentalComposeUiApi::class)
@Composable
fun App(state: AppState = remember { AppState() }, capture: MediaCapture = rememberMediaCapture()) {
    CaptureSync(state, capture)
    KurzTheme(state.dark) {
        BoxWithConstraints(Modifier.fillMaxSize()) {
            val wc = windowClassFor(maxWidth)
            // a cor por trás das barras do sistema acompanha o ecrã visível
            val bg = if (wc == WindowClass.Compact && !state.contentOpen) p.sidebar else p.main
            val outer = this
            Box(Modifier.fillMaxSize().background(bg).windowInsetsPadding(WindowInsets.safeDrawing)) {
            CompositionLocalProvider(LocalWindowClass provides wc, LocalMediaCapture provides capture) {
                val full = Modifier.fillMaxSize()
                if (wc == WindowClass.Compact) {
                    BackHandler(enabled = state.membersOverlay || state.contentOpen) {
                        if (state.membersOverlay) state.membersOverlay = false else state.closeContent()
                    }
                    when {
                        !state.contentOpen -> ChannelSidebar(state, full)
                        state.showVoice -> VoiceRoom(state, full)
                        else -> ChatPane(state, full)
                    }
                } else {
                    Row(full) {
                        ChannelSidebar(state, Modifier.width(248.dp).fillMaxHeight())
                        val mainMod = Modifier.weight(1f).fillMaxHeight()
                        if (state.showVoice) VoiceRoom(state, mainMod) else ChatPane(state, mainMod)
                        if (wc == WindowClass.Expanded && state.showMembers && !state.showVoice) MembersPanel(state, Modifier.width(288.dp).fillMaxHeight())
                    }
                }
                if (wc != WindowClass.Expanded) MembersDrawer(state, outer.maxWidth)
            }
            }
        }
    }
}

/** Gaveta de membros à direita com fundo escurecido (ecrãs médio/estreito). */
@Composable
private fun BoxScope.MembersDrawer(s: AppState, screenWidth: Dp) {
    val open = s.membersOverlay && !s.showVoice && (s.contentOpen || LocalWindowClass.current != WindowClass.Compact)
    if (!open) return
    Box(Modifier.fillMaxSize().background(Color.Black.copy(alpha = 0.4f)).clickable(remember { MutableInteractionSource() }, null) { s.membersOverlay = false })
    MembersPanel(s, Modifier.align(Alignment.CenterEnd).fillMaxHeight().width(minOf(340.dp, screenWidth * 0.85f)), onClose = { s.membersOverlay = false })
}

package kurz.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.compositionLocalOf
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import kurz.core.AppState

/** Classe de largura da janela: telemóvel (um painel de cada vez), tablet pequeno (lista+conteúdo), desktop/tablet largo (3 painéis). */
enum class WindowClass { Compact, Medium, Expanded }

val LocalWindowClass = compositionLocalOf { WindowClass.Expanded }

fun windowClassFor(width: Dp) = when {
    width < 600.dp -> WindowClass.Compact
    width < 1000.dp -> WindowClass.Medium
    else -> WindowClass.Expanded
}

val compact: Boolean @Composable get() = LocalWindowClass.current == WindowClass.Compact

/** Margem lateral do conteúdo. */
val gutter: Dp @Composable get() = if (compact) 16.dp else 24.dp

/** Altura de linha tocável (≥48dp em ecrãs táteis). */
val rowHeight: Dp @Composable get() = if (compact) 48.dp else 32.dp

/** Lado de botões de ícone. */
val iconBtnSize: Dp @Composable get() = if (compact) 44.dp else 36.dp

@Composable
fun membersActive(s: AppState) = if (LocalWindowClass.current == WindowClass.Expanded) s.showMembers else s.membersOverlay

fun AppState.toggleMembers(wc: WindowClass) {
    if (wc == WindowClass.Expanded) showMembers = !showMembers else membersOverlay = !membersOverlay
}

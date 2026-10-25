package kurz.ui

import kurz.core.*

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.material3.LocalContentColor
import androidx.compose.ui.graphics.Color
import androidx.compose.material3.LocalTextStyle
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.style.LineHeightStyle
import androidx.compose.ui.unit.em

// Tokens lidos da referência (client/reference/discord-redesign.png). Dono: fundação — agentes não editam.
@Immutable
class Palette(
    val dark: Boolean,
    val rail: Color,       // coluna de servidores
    val sidebar: Color,    // lista de canais
    val main: Color,       // chat / sala de voz
    val panel: Color,      // painel de membros
    val border: Color,     // divisores de 1dp
    val text: Color,       // texto principal
    val textMuted: Color,  // nomes de canais, secundário
    val textFaint: Color,  // subtítulos, horas, placeholders
    val hover: Color,
    val selected: Color,   // canal selecionado
    val input: Color,      // caixas de texto / pesquisa / aviso de não lidas
    val accent: Color,     // azul-violeta do logótipo e botões
    val onAccent: Color,
    val link: Color,       // "Ver todos"
    val online: Color,
    val idle: Color,
    val offline: Color,
    val danger: Color,
)

val LightPalette = Palette(
    dark = false,
    rail = Color(0xFFF1F2F7), sidebar = Color(0xFFF7F8FB), main = Color(0xFFFFFFFF), panel = Color(0xFFFFFFFF),
    border = Color(0xFFE6E8EF), text = Color(0xFF16181D), textMuted = Color(0xFF5E6472), textFaint = Color(0xFF9AA0AE),
    hover = Color(0xFFECEEF4), selected = Color(0xFFE9EBF2), input = Color(0xFFF4F5F9),
    accent = Color(0xFF5865F2), onAccent = Color(0xFFFFFFFF), link = Color(0xFF2F5BEA),
    online = Color(0xFF23A55A), idle = Color(0xFFF0B232), offline = Color(0xFF9AA0AE), danger = Color(0xFFE5484D),
)

val DarkPalette = Palette(
    dark = true,
    rail = Color(0xFF111214), sidebar = Color(0xFF1A1B1F), main = Color(0xFF16171A), panel = Color(0xFF16171A),
    border = Color(0xFF26282D), text = Color(0xFFF2F3F5), textMuted = Color(0xFFB0B5C0), textFaint = Color(0xFF727887),
    hover = Color(0xFF24262B), selected = Color(0xFF2B2D33), input = Color(0xFF212226),
    accent = Color(0xFF5865F2), onAccent = Color(0xFFFFFFFF), link = Color(0xFF5B83FF),
    online = Color(0xFF23A55A), idle = Color(0xFFF0B232), offline = Color(0xFF727887), danger = Color(0xFFFF6B6F),
)

val LocalPalette = staticCompositionLocalOf { LightPalette }

@Composable
fun KurzTheme(dark: Boolean = isSystemInDarkTheme(), content: @Composable () -> Unit) {
    val p = if (dark) DarkPalette else LightPalette
    val scheme = (if (dark) darkColorScheme() else lightColorScheme()).copy(
        primary = p.accent, onPrimary = p.onAccent, background = p.main, onBackground = p.text,
        surface = p.main, onSurface = p.text, error = p.danger,
    )
    MaterialTheme(colorScheme = scheme) {
        CompositionLocalProvider(
            LocalPalette provides p, LocalContentColor provides p.text,
            LocalTextStyle provides TextStyle(lineHeight = 1.35.em, lineHeightStyle = LineHeightStyle(LineHeightStyle.Alignment.Center, LineHeightStyle.Trim.None)),
            content = content,
        )
    }
}

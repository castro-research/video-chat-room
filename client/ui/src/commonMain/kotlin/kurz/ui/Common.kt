package kurz.ui

import kurz.core.*

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.hoverable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsHoveredAsState
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.LocalTextStyle
import androidx.compose.material3.Text
import androidx.compose.ui.text.style.LineHeightStyle
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Shape
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.input.pointer.PointerIcon
import androidx.compose.ui.input.pointer.pointerHoverIcon
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

// Peças partilhadas. Dono: fundação — agentes usam, não editam.

val p: Palette @Composable get() = LocalPalette.current

/** Fundo de hover/selecionado + cursor de mão + clique. Para linhas de canal, membros, botões de ícone. */
@Composable
fun Modifier.rowClick(selected: Boolean = false, shape: Shape = RoundedCornerShape(6.dp), onClick: () -> Unit = {}): Modifier {
    val src = remember { MutableInteractionSource() }
    val hovered by src.collectIsHoveredAsState()
    val bg = if (selected) p.selected else if (hovered) p.hover else Color.Transparent
    return this.clip(shape).background(bg).hoverable(src).pointerHoverIcon(PointerIcon.Hand).clickable(src, null, onClick = onClick)
}

@Composable
fun statusColor(s: Status) = when (s) { Status.Online -> p.online; Status.Idle -> p.idle; Status.Offline -> p.offline }

/** Avatar redondo com gradiente por matiz e iniciais. [status] != null desenha a bolinha; [dotBorder] = cor do fundo onde assenta. */
@Composable
fun Avatar(name: String, hue: Float, size: Dp, status: Status? = null, dotBorder: Color = p.main, rounded: Dp? = null) {
    val shape: Shape = if (rounded != null) RoundedCornerShape(rounded) else CircleShape
    val a = Color.hsv(hue % 360f, 0.55f, 0.95f)
    val b = Color.hsv((hue + 30f) % 360f, 0.75f, 0.72f)
    Box(Modifier.size(size)) {
        Box(Modifier.size(size).clip(shape).background(Brush.linearGradient(listOf(a, b))), Alignment.Center) {
            Text(name.split(' ', '-').filter { it.isNotEmpty() }.take(2).joinToString("") { it.first().uppercase() },
                color = Color.White, fontSize = (size.value * 0.38f).sp, lineHeight = (size.value * 0.38f).sp, fontWeight = FontWeight.SemiBold,
                textAlign = TextAlign.Center,
                style = LocalTextStyle.current.copy(lineHeightStyle = LineHeightStyle(LineHeightStyle.Alignment.Center, LineHeightStyle.Trim.Both)))
        }
        if (status != null) {
            val dot = size * 0.34f
            Box(Modifier.align(Alignment.BottomEnd).size(dot).clip(CircleShape).background(dotBorder).border(0.dp, Color.Transparent, CircleShape)) {
                Box(Modifier.align(Alignment.Center).size(dot * 0.68f).clip(CircleShape).background(statusColor(status)))
            }
        }
    }
}

@Composable
fun Avatar(u: User, size: Dp, showStatus: Boolean = false, dotBorder: Color = p.main) =
    Avatar(u.name, u.hue, size, if (showStatus) u.status else null, dotBorder)

/** Selo pequeno em maiúsculas (ADMIN / MOD). */
@Composable
fun Badge(text: String) = Text(text, color = p.textFaint, fontSize = 10.sp, fontWeight = FontWeight.Medium, letterSpacing = 0.8.sp)

/** Botão de ícone quadrado de [size] dp (cabeçalhos, barras). [active] pinta o ícone com o acento. */
@Composable
fun IconBtn(icon: ImageVector, desc: String, onClick: () -> Unit = {}, size: Dp = iconBtnSize, iconSize: Dp = 20.dp, active: Boolean = false, tint: Color = p.textMuted) {
    Box(Modifier.size(size).rowClick(shape = RoundedCornerShape(8.dp), onClick = onClick), Alignment.Center) {
        Icon(icon, desc, Modifier.size(iconSize), tint = if (active) p.accent else tint)
    }
}

/** Foto de exemplo: céu → horizonte → terreno em gradiente vertical. */
@Composable
fun PhotoView(photo: Photo, modifier: Modifier = Modifier, shape: Shape = RoundedCornerShape(8.dp)) {
    val sky = Color(photo.sky); val horizon = Color(photo.horizon); val ground = Color(photo.ground)
    Canvas(modifier.clip(shape)) {
        drawRect(Brush.verticalGradient(0f to sky, 0.55f to horizon, 0.56f to ground, 1f to ground.copy(alpha = 0.85f)))
        drawCircle(Color.White.copy(alpha = 0.18f), radius = size.height * 0.35f, center = Offset(size.width * 0.7f, size.height * 0.3f))
    }
}

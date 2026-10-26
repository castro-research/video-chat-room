package kurz.ui

import kurz.core.*
import kurz.media.ScreenQuality
import kurz.media.ScreenResolution
import kurz.media.ScreenSource

import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.ScreenShare
import androidx.compose.material.icons.outlined.Code
import androidx.compose.material.icons.outlined.DesktopWindows
import androidx.compose.material.icons.outlined.WebAsset
import androidx.compose.material3.Icon
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.ExperimentalComposeUiApi
import androidx.compose.ui.Modifier
import androidx.compose.ui.backhandler.BackHandler
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

/**
 * Modal de partilha de ecrã, por cima de tudo: miniaturas das janelas ("Aplicativos") e dos monitores ("Tela Inteira", para quem tem mais de um).
 * Tocar numa liga a partilha com ela (ou troca, se já estiver a partilhar). Fecha ao tocar fora ou com Esc/voltar.
 */
@OptIn(ExperimentalComposeUiApi::class)
@Composable
fun SharePicker(s: AppState) {
    if (!s.sharePickerOpen) return
    BackHandler { s.sharePickerOpen = false }
    val cap = LocalMediaCapture.current
    var windows by remember { mutableStateOf(true) }
    val sources = cap.screenSources.filter { it.window == windows }
    val current = if (s.sharing) cap.screenSourceId ?: cap.screenSources.firstOrNull()?.id else null

    Box(Modifier.fillMaxSize().background(Color.Black.copy(alpha = 0.5f)).clickable(remember { MutableInteractionSource() }, null) { s.sharePickerOpen = false }, Alignment.Center) {
        Column(
            Modifier.padding(24.dp).widthIn(max = 760.dp).fillMaxWidth().heightIn(max = 560.dp).clip(RoundedCornerShape(12.dp)).background(p.main)
                .clickable(remember { MutableInteractionSource() }, null) {}  // o toque dentro não chega à cortina
                .padding(16.dp),
            Arrangement.spacedBy(14.dp),
        ) {
            Row(Modifier.fillMaxWidth().background(p.sidebar, RoundedCornerShape(10.dp)).padding(4.dp), Arrangement.spacedBy(4.dp)) {
                PickerTab("Aplicativos", Icons.Outlined.WebAsset, windows, Modifier.weight(1f)) { windows = true }
                PickerTab("Tela Inteira", Icons.Outlined.DesktopWindows, !windows, Modifier.weight(1f)) { windows = false }
            }
            if (sources.isEmpty()) Text(if (cap.screenSources.isEmpty()) "A procurar…" else "Nada para partilhar aqui.", color = p.textFaint, fontSize = 13.sp)
            LazyVerticalGrid(GridCells.Fixed(2), Modifier.weight(1f, false), horizontalArrangement = Arrangement.spacedBy(12.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                items(sources, key = { it.id }) { src ->
                    SourceCard(src, cap.screenThumbs[src.id], src.id == current) { cap.selectScreen(src.id); s.sharing = true; s.sharePickerOpen = false }
                }
            }
            val q = cap.screenQuality
            Row(Modifier.fillMaxWidth(), Arrangement.spacedBy(12.dp)) {
                PresetButton("Pair coding", Icons.Outlined.Code, ScreenQuality.PairCoding, q, Modifier.weight(1f)) { cap.selectScreenQuality(it) }
                PresetButton("Normal", Icons.AutoMirrored.Outlined.ScreenShare, ScreenQuality.Normal, q, Modifier.weight(1f)) { cap.selectScreenQuality(it) }
            }
            QualityRow("Resolução", ScreenResolution.entries, q.resolution, { it.label }) { cap.selectScreenQuality(q.copy(resolution = it)) }
            QualityRow("Quadros", ScreenQuality.fpsOptions, q.fps, { it.toString() }) { cap.selectScreenQuality(q.copy(fps = it)) }
            if (cap.canShareAudio) Row(Modifier.fillMaxWidth(), Arrangement.SpaceBetween, Alignment.CenterVertically) {
                Text("Partilhar áudio do sistema", color = p.text, fontSize = 13.sp, fontWeight = FontWeight.SemiBold)
                Switch(cap.screenAudio, { cap.selectScreenAudio(it) })
            }
        }
    }
}

/** Atalho com a qualidade recomendada para um uso; fica marcado enquanto a qualidade actual for exactamente a dele. */
@Composable
private fun PresetButton(label: String, icon: ImageVector, preset: ScreenQuality, current: ScreenQuality, modifier: Modifier, onPick: (ScreenQuality) -> Unit) {
    val on = preset == current
    val shape = RoundedCornerShape(8.dp)
    Row(modifier.rowClick(on, shape) { onPick(preset) }.border(1.dp, if (on) p.accent else p.border, shape).padding(12.dp), Arrangement.spacedBy(10.dp), Alignment.CenterVertically) {
        Icon(icon, null, Modifier.size(20.dp), tint = if (on) p.text else p.textMuted)
        Column {
            Text(label, color = p.text, fontSize = 13.sp, fontWeight = FontWeight.SemiBold)
            Text(preset.label, color = p.textFaint, fontSize = 11.5.sp)
        }
    }
}

/** Uma linha "nome + opções lado a lado" para escolher um valor. */
@Composable
private fun <T> QualityRow(title: String, options: List<T>, selected: T, label: (T) -> String, onPick: (T) -> Unit) {
    Row(Modifier.fillMaxWidth(), Arrangement.SpaceBetween, Alignment.CenterVertically) {
        Text(title, color = p.text, fontSize = 13.sp, fontWeight = FontWeight.SemiBold)
        Row(Modifier.background(p.sidebar, RoundedCornerShape(8.dp)).padding(3.dp), Arrangement.spacedBy(2.dp)) {
            options.forEach { o ->
                Text(label(o), Modifier.widthIn(min = 52.dp).height(28.dp).rowClick(o == selected, RoundedCornerShape(6.dp)) { onPick(o) }.wrapContentHeight().padding(horizontal = 10.dp),
                    color = if (o == selected) p.text else p.textMuted, fontSize = 12.sp, fontWeight = FontWeight.SemiBold, textAlign = TextAlign.Center)
            }
        }
    }
}

@Composable
private fun PickerTab(label: String, icon: ImageVector, on: Boolean, modifier: Modifier, onClick: () -> Unit) {
    Row(modifier.height(36.dp).rowClick(on, RoundedCornerShape(8.dp), onClick), Arrangement.spacedBy(8.dp, Alignment.CenterHorizontally), Alignment.CenterVertically) {
        Icon(icon, null, Modifier.size(18.dp), tint = if (on) p.text else p.textMuted)
        Text(label, color = if (on) p.text else p.textMuted, fontSize = 13.sp, fontWeight = FontWeight.SemiBold)
    }
}

@Composable
private fun SourceCard(src: ScreenSource, thumb: ImageBitmap?, chosen: Boolean, onClick: () -> Unit) {
    Column(Modifier.rowClick(chosen, RoundedCornerShape(8.dp), onClick).padding(6.dp), Arrangement.spacedBy(8.dp)) {
        Box(Modifier.fillMaxWidth().aspectRatio(16f / 9f).clip(RoundedCornerShape(6.dp)).background(Color.Black)) {
            if (thumb != null) Image(thumb, null, Modifier.fillMaxSize(), contentScale = ContentScale.Fit)
        }
        Row(horizontalArrangement = Arrangement.spacedBy(6.dp), verticalAlignment = Alignment.CenterVertically) {
            Icon(if (src.window) Icons.Outlined.WebAsset else Icons.Outlined.DesktopWindows, null, Modifier.size(16.dp), tint = p.textMuted)
            Text(src.name, Modifier.weight(1f, false), color = p.text, fontSize = 13.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
    }
}

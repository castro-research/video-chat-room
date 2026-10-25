package kurz.ui

import kurz.core.*

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.ArrowBack
import androidx.compose.material.icons.automirrored.outlined.ScreenShare
import androidx.compose.material.icons.automirrored.outlined.VolumeUp
import androidx.compose.material.icons.outlined.*
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

private val camPhoto = Photo(0xFF6C7FC9, 0xFFF08F8F, 0xFF3A3F57, 1.78f)

@Composable
fun VoiceRoom(s: AppState, modifier: Modifier = Modifier) {
    val ch = s.voiceChannel ?: return
    val people = s.voiceParticipants(ch.id)
    val speaking = people.firstOrNull { it != s.me }
    val others = people.size - 1
    Column(modifier.background(p.main)) {
        // cabeçalho
        Row(Modifier.fillMaxWidth().height(56.dp).padding(horizontal = if (compact) 8.dp else 24.dp), verticalAlignment = Alignment.CenterVertically) {
            if (compact) IconBtn(Icons.AutoMirrored.Outlined.ArrowBack, "Voltar", onClick = { s.closeContent() })
            Icon(Icons.AutoMirrored.Outlined.VolumeUp, null, Modifier.size(18.dp), tint = p.textMuted)
            Spacer(Modifier.width(8.dp))
            Text(ch.name, color = p.text, fontSize = 15.sp, fontWeight = FontWeight.SemiBold)
            Text("  ·  ${people.size} ${if (people.size == 1) "pessoa ligada" else "pessoas ligadas"}", color = p.textFaint, fontSize = 12.sp)
        }
        Box(Modifier.fillMaxWidth().height(1.dp).background(p.border))
        // grelha
        Column(Modifier.weight(1f).fillMaxWidth()) {
            LazyVerticalGrid(
                GridCells.Adaptive(if (compact) 220.dp else 340.dp), Modifier.weight(1f).fillMaxWidth(),
                contentPadding = PaddingValues(gutter),
                horizontalArrangement = Arrangement.spacedBy(16.dp), verticalArrangement = Arrangement.spacedBy(16.dp),
            ) {
                items(people, key = { it.id }) { u -> Tile(s, u, u == speaking) }
            }
            if (others == 0) Text(
                "Ainda ninguém mais está nesta sala. Quem entrar aparece aqui.", color = p.textFaint, fontSize = 13.sp,
                modifier = Modifier.fillMaxWidth().padding(bottom = 24.dp), textAlign = androidx.compose.ui.text.style.TextAlign.Center,
            )
        }
        // controlos
        Box(Modifier.fillMaxWidth().height(1.dp).background(p.border))
        Row(Modifier.fillMaxWidth().height(80.dp), Arrangement.Center, Alignment.CenterVertically) {
            Ctl(if (s.muted) Icons.Outlined.MicOff else Icons.Outlined.Mic, "Microfone", !s.muted) { s.muted = !s.muted }
            Spacer(Modifier.width(12.dp))
            Ctl(if (s.camOn) Icons.Outlined.Videocam else Icons.Outlined.VideocamOff, "Câmara", s.camOn) { s.camOn = !s.camOn }
            Spacer(Modifier.width(12.dp))
            Ctl(Icons.AutoMirrored.Outlined.ScreenShare, "Partilhar ecrã", s.sharing, offTint = p.textMuted) { s.sharing = !s.sharing }
            Spacer(Modifier.width(12.dp))
            Box(
                Modifier.height(48.dp).rowClick(shape = CircleShape) { s.leaveVoice() }.background(p.danger.copy(alpha = 0.14f)).padding(horizontal = 20.dp),
                Alignment.Center,
            ) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Icon(Icons.Outlined.CallEnd, null, Modifier.size(20.dp), tint = p.danger)
                    Spacer(Modifier.width(8.dp))
                    Text("Sair", color = p.danger, fontSize = 13.sp, fontWeight = FontWeight.SemiBold)
                }
            }
        }
    }
}

@Composable
private fun Ctl(icon: ImageVector, desc: String, active: Boolean, offTint: Color = p.danger, onClick: () -> Unit) {
    Box(Modifier.size(48.dp).rowClick(selected = active, shape = CircleShape, onClick = onClick), Alignment.Center) {
        Icon(icon, desc, Modifier.size(22.dp), tint = if (active) p.text else offTint)
    }
}

@Composable
private fun Tile(s: AppState, u: User, speaking: Boolean) {
    val me = u == s.me
    val shape = RoundedCornerShape(12.dp)
    Box(
        Modifier.fillMaxWidth().aspectRatio(16f / 9f).clip(shape).background(p.sidebar)
            .border(if (speaking) 2.dp else 1.dp, if (speaking) p.accent else p.border, shape),
        Alignment.Center,
    ) {
        if (me && s.camOn) PhotoView(camPhoto, Modifier.fillMaxSize(), shape) else Avatar(u, 88.dp)
        Text(
            if (me) "${u.name} (tu)" else u.name, color = Color.White, fontSize = 12.sp,
            modifier = Modifier.align(Alignment.BottomStart).padding(10.dp).clip(RoundedCornerShape(6.dp))
                .background(Color.Black.copy(alpha = 0.55f)).padding(horizontal = 8.dp, vertical = 3.dp),
        )
        if (me && s.muted) Icon(
            Icons.Outlined.MicOff, null, tint = p.danger,
            modifier = Modifier.align(Alignment.TopEnd).padding(10.dp).size(24.dp).clip(CircleShape)
                .background(Color.Black.copy(alpha = 0.55f)).padding(4.dp),
        )
    }
}

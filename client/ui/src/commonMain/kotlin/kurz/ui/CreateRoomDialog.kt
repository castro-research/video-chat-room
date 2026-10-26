package kurz.ui

import kurz.core.*

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.VolumeUp
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.ExperimentalComposeUiApi
import androidx.compose.ui.Modifier
import androidx.compose.ui.backhandler.BackHandler
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.launch

/** Modal de criar canal, por cima de tudo. Só há canais de voz (as salas do manager); entra na sala assim que ela é criada. Fecha ao tocar fora ou com Esc/voltar. */
@OptIn(ExperimentalComposeUiApi::class)
@Composable
fun CreateRoomDialog(s: AppState) {
    if (!s.createRoomOpen) return
    BackHandler { s.createRoomOpen = false }
    val r = LocalRooms.current
    val scope = rememberCoroutineScope()
    var name by remember { mutableStateOf("") }
    val focus = remember { FocusRequester() }
    LaunchedEffect(Unit) { focus.requestFocus() }
    fun create() {
        val n = name.trim().ifEmpty { return }
        scope.launch { r.create(s.managerUrl, n)?.let { s.createRoomOpen = false; s.joinRoom(it.id) } }
    }

    Box(Modifier.fillMaxSize().background(Color.Black.copy(alpha = 0.5f)).clickable(remember { MutableInteractionSource() }, null) { s.createRoomOpen = false }, Alignment.Center) {
        Column(
            Modifier.padding(24.dp).widthIn(max = 420.dp).fillMaxWidth().clip(RoundedCornerShape(12.dp)).background(p.main)
                .clickable(remember { MutableInteractionSource() }, null) {}  // o toque dentro não chega à cortina
                .padding(20.dp),
            Arrangement.spacedBy(14.dp),
        ) {
            Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Text("Criar canal", color = p.text, fontSize = 16.sp, fontWeight = FontWeight.SemiBold)
                Text("em Salas", color = p.textFaint, fontSize = 12.sp)
            }
            Text("Tipo de canal", color = p.textMuted, fontSize = 12.sp, fontWeight = FontWeight.Medium)
            Row(Modifier.fillMaxWidth().background(p.input, RoundedCornerShape(8.dp)).padding(12.dp), Arrangement.spacedBy(10.dp), Alignment.CenterVertically) {
                Box(Modifier.size(18.dp).background(p.accent, CircleShape), Alignment.Center) { Box(Modifier.size(8.dp).background(Color.White, CircleShape)) }
                Icon(Icons.AutoMirrored.Outlined.VolumeUp, null, Modifier.size(18.dp), tint = p.text)
                Column(Modifier.weight(1f)) {
                    Text("Voz", color = p.text, fontSize = 13.sp, fontWeight = FontWeight.SemiBold)
                    Text("Voz, vídeo e partilha de ecrã.", color = p.textFaint, fontSize = 11.5.sp)
                }
            }
            Text("Nome do canal", color = p.textMuted, fontSize = 12.sp, fontWeight = FontWeight.Medium)
            Field(name, "novo-canal", focus = focus, onEnter = ::create) { name = it }
            Row(Modifier.fillMaxWidth(), Arrangement.spacedBy(8.dp, Alignment.End)) {
                SmallButton("Cancelar") { s.createRoomOpen = false }
                Text("Criar canal", Modifier.height(30.dp).rowClick(shape = RoundedCornerShape(6.dp), onClick = ::create)
                    .background(p.accent.copy(alpha = if (name.isBlank()) 0.4f else 1f)).padding(horizontal = 10.dp, vertical = 7.dp),
                    color = Color.White, fontSize = 12.sp, maxLines = 1)
            }
        }
    }
}

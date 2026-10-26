package kurz.ui

import kurz.core.*

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Check
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.ExperimentalComposeUiApi
import androidx.compose.ui.Modifier
import androidx.compose.ui.backhandler.BackHandler
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

/** Modal de servidores, por cima de tudo: escolher o ligado, adicionar outro pelo endereço ou tirar um adicionado. Fecha ao tocar fora ou com Esc/voltar. */
@OptIn(ExperimentalComposeUiApi::class)
@Composable
fun ServerDialog(s: AppState) {
    if (!s.serversOpen) return
    BackHandler { s.serversOpen = false }
    var input by remember { mutableStateOf("") }
    var invalid by remember { mutableStateOf(false) }
    val focus = remember { FocusRequester() }
    LaunchedEffect(Unit) { focus.requestFocus() }
    fun add() { if (s.addServer(input)) { input = ""; s.serversOpen = false } else invalid = true }

    val none = remember { MutableInteractionSource() }
    Box(Modifier.fillMaxSize().background(Color.Black.copy(alpha = 0.5f)).clickable(none, null) { s.serversOpen = false }, Alignment.Center) {
        Column(
            Modifier.padding(24.dp).widthIn(max = 420.dp).fillMaxWidth().clip(RoundedCornerShape(12.dp)).background(p.main)
                .clickable(remember { MutableInteractionSource() }, null) {}  // o toque dentro não chega à cortina
                .padding(20.dp),
            Arrangement.spacedBy(14.dp),
        ) {
            Text("Servidores", color = p.text, fontSize = 16.sp, fontWeight = FontWeight.SemiBold)
            Column(Modifier.fillMaxWidth(), Arrangement.spacedBy(2.dp)) {
                s.servers.forEach { url ->
                    val on = url == s.managerUrl
                    Row(Modifier.fillMaxWidth().height(rowHeight).rowClick(on) { s.selectServer(url); s.serversOpen = false }.padding(start = 8.dp),
                        Arrangement.spacedBy(8.dp), Alignment.CenterVertically) {
                        Icon(Icons.Outlined.Check, if (on) "Ligado" else null, Modifier.size(16.dp), tint = if (on) p.accent else Color.Transparent)
                        Text(serverLabel(url), Modifier.weight(1f), color = if (on) p.text else p.textMuted, fontSize = 13.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        HealthDot(s, url)
                        if (url == s.servers.first()) Spacer(Modifier.width(8.dp))
                        else IconBtn(Icons.Outlined.Close, "Remover", { s.removeServer(url) }, size = 28.dp, iconSize = 14.dp)
                    }
                }
            }
            Box(Modifier.fillMaxWidth().height(1.dp).background(p.border))
            Text("Adicionar servidor", color = p.textMuted, fontSize = 12.sp, fontWeight = FontWeight.Medium)
            Field(input, "endereço, ex.: cord.kurz.fyi", focus = focus, onEnter = ::add) { input = it; invalid = false }
            if (invalid) Text("Isto não é um endereço. Usa o nome do servidor, ex.: cord.kurz.fyi ou http://localhost:30080.", color = p.danger, fontSize = 11.5.sp)
            Row(Modifier.fillMaxWidth(), Arrangement.spacedBy(8.dp, Alignment.End)) {
                SmallButton("Cancelar") { s.serversOpen = false }
                SmallButton("Adicionar", ::add)
            }
        }
    }
}

/** Verde = o servidor responde ao /healthz, vermelho = não responde, cinza = ainda por saber. */
@Composable
internal fun HealthDot(s: AppState, url: String) {
    val (color, label) = when (s.health[url]) {
        true -> p.online to "Servidor a responder"
        false -> p.danger to "Servidor sem resposta"
        null -> p.textFaint to "A verificar o servidor"
    }
    Box(Modifier.size(8.dp).clip(CircleShape).background(color).semantics { contentDescription = label })
}

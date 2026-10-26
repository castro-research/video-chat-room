package kurz.ui

import kurz.core.*
import kurz.media.Room

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.KeyboardArrowRight
import androidx.compose.material.icons.automirrored.outlined.VolumeUp
import androidx.compose.material.icons.outlined.*
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.text.TextStyle
import kotlinx.coroutines.delay
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

@Composable
fun ChannelSidebar(s: AppState, modifier: Modifier = Modifier) {
    Column(modifier.background(p.sidebar)) {
        Row(Modifier.fillMaxWidth().height(56.dp).padding(horizontal = 16.dp), Arrangement.spacedBy(10.dp), Alignment.CenterVertically) {
            Row(Modifier.weight(1f).rowClick(shape = RoundedCornerShape(8.dp)) { s.serversOpen = true }.padding(4.dp), Arrangement.spacedBy(10.dp), Alignment.CenterVertically) {
                Box {
                    Avatar(s.server.name, s.server.hue, 32.dp, rounded = 8.dp)
                    Box(Modifier.align(Alignment.BottomEnd).offset(3.dp, 3.dp).background(p.sidebar, CircleShape).padding(2.dp)) { HealthDot(s, s.managerUrl) }
                }
                Text(s.server.name, Modifier.weight(1f, false), color = p.text, fontSize = 15.sp, fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Icon(Icons.Outlined.KeyboardArrowDown, "Servidores", Modifier.size(16.dp), tint = p.textMuted)
            }
            IconBtn(if (s.dark) Icons.Outlined.LightMode else Icons.Outlined.DarkMode, "Alternar tema", { s.dark = !s.dark }, size = 32.dp, iconSize = 18.dp)
        }
        val rooms = LocalRooms.current
        rooms.notice?.let { Notice(it) { rooms.notice = null } }
        LazyColumn(Modifier.weight(1f).fillMaxWidth().padding(horizontal = 8.dp)) {
            item("rooms") { RoomsSection(s) }  // só as salas do manager (o seed é dele); os canais de exemplo do Model não aparecem
        }
        (s.voiceChannel?.name ?: s.roomId?.let { shownRoom(s, rooms)?.name ?: it })?.let { name ->
            Column(Modifier.fillMaxWidth().padding(horizontal = 8.dp, vertical = 8.dp).background(p.input, RoundedCornerShape(8.dp)).padding(10.dp),
                Arrangement.spacedBy(8.dp)) {
                Text(if (rooms.reconnecting) "A reconectar · $name" else "Voz ligada · $name", color = if (rooms.reconnecting) p.idle else p.online, fontSize = 12.sp, fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Row(Modifier.fillMaxWidth(), Arrangement.spacedBy(8.dp)) {
                    if (!s.showVoice || compact) Text("Ver sala", Modifier.weight(1f).rowClick(shape = RoundedCornerShape(6.dp)) { s.openVoiceRoom() }.padding(vertical = 6.dp),
                        color = p.text, fontSize = 12.sp, textAlign = androidx.compose.ui.text.style.TextAlign.Center)
                    Text("Sair", Modifier.weight(1f).rowClick(shape = RoundedCornerShape(6.dp)) { s.leaveVoice() }.padding(vertical = 6.dp),
                        color = p.danger, fontSize = 12.sp, textAlign = androidx.compose.ui.text.style.TextAlign.Center)
                }
            }
        }
        Box(Modifier.fillMaxWidth().height(1.dp).background(p.border))
        Row(Modifier.fillMaxWidth().height(60.dp).padding(horizontal = 16.dp), Arrangement.spacedBy(10.dp), Alignment.CenterVertically) {
            Avatar(s.me, 36.dp, showStatus = true, dotBorder = p.sidebar)
            Column(Modifier.weight(1f)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(s.me.name, color = p.text, fontSize = 13.sp, fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, false))
                    Icon(Icons.Outlined.KeyboardArrowDown, null, Modifier.size(16.dp), tint = p.textMuted)
                }
                Text(heart(s.me.subtitle ?: "", p.danger), color = p.textFaint, fontSize = 11.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            IconBtn(if (s.muted) Icons.Outlined.MicOff else Icons.Outlined.Mic, "Microfone", { s.muted = !s.muted }, size = 32.dp, iconSize = 18.dp,
                tint = if (s.muted) p.danger else p.textMuted)
        }
    }
}

private fun heart(t: String, c: Color): AnnotatedString = buildAnnotatedString {
    t.forEach { if (it == '❤') withStyle(SpanStyle(color = c)) { append(it) } else append(it) }
}

/** Salas reais do servidor ligado (o endereço muda-se na modal): lista (actualizada a cada 3 s); o "+" ao lado do título abre a modal de criar. */
@Composable
private fun RoomsSection(s: AppState) {
    val r = LocalRooms.current
    LaunchedEffect(s.managerUrl) {
        delay(300)  // espera que se acabe de escrever a URL
        while (true) { r.list(s.managerUrl); delay(3_000) }
    }
    val empty = r.rooms.isEmpty() && r.listError == null  // servidor sem nada: sem o título não há "+", por isso o menu
    Column(Modifier.fillMaxWidth(), Arrangement.spacedBy(6.dp)) {
        Row(Modifier.fillMaxWidth().padding(start = 8.dp, top = 8.dp).height(24.dp), verticalAlignment = Alignment.CenterVertically) {
            Text("Salas", Modifier.weight(1f), color = p.text, fontSize = 13.sp, fontWeight = FontWeight.SemiBold)
            if (!empty) IconBtn(Icons.Outlined.Add, "Criar canal", { s.createRoomOpen = true }, size = 24.dp, iconSize = 16.dp)
        }
        r.listError?.let { Text(it, Modifier.padding(horizontal = 8.dp), color = p.danger, fontSize = 11.sp) }
        r.rooms.forEach { RoomRow(s, it) }
        if (empty) Column(Modifier.fillMaxWidth().background(p.input, RoundedCornerShape(8.dp)).padding(4.dp)) {
            Row(Modifier.fillMaxWidth().height(rowHeight).rowClick { s.createRoomOpen = true }.padding(horizontal = 8.dp), Arrangement.spacedBy(8.dp), Alignment.CenterVertically) {
                Text("Criar canal", Modifier.weight(1f), color = p.text, fontSize = 13.sp)
                Icon(Icons.Outlined.AddCircleOutline, null, Modifier.size(16.dp), tint = p.textMuted)
            }
        }
    }
}

@Composable
private fun RoomRow(s: AppState, room: Room) {
    val rooms = LocalRooms.current
    val connected = s.roomId == room.id
    val people = peopleIn(room, s, rooms)  // otimista: o próprio entra já na sala clicada e sai já da anterior
    Column(Modifier.fillMaxWidth()) {
        Row(Modifier.fillMaxWidth().height(rowHeight).rowClick(connected) { if (connected) s.openVoiceRoom() else s.joinRoom(room.id) }.padding(horizontal = 8.dp),
            Arrangement.spacedBy(8.dp), Alignment.CenterVertically) {
            val c = if (connected) p.text else p.textMuted
            Icon(Icons.AutoMirrored.Outlined.VolumeUp, null, Modifier.size(16.dp), tint = c)
            Text(room.name, color = c, fontSize = 13.sp, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, false))
            Text("${room.id} · ${people.size}", color = p.textFaint, fontSize = 11.sp, maxLines = 1)
        }
        people.forEach { pt ->
            val u = pt.user(s.me, rooms.userId)
            Row(Modifier.fillMaxWidth().height(28.dp).padding(start = 32.dp), Arrangement.spacedBy(8.dp), Alignment.CenterVertically) {
                Avatar(u, 20.dp)
                Text(u.name, color = p.textMuted, fontSize = 12.sp, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, false))
                if (!pt.mic) Icon(Icons.Outlined.MicOff, null, Modifier.size(12.dp), tint = p.textFaint)
                if (pt.camera) Icon(Icons.Outlined.Videocam, null, Modifier.size(12.dp), tint = p.textFaint)
                if (pt.screen) Icon(Icons.Outlined.ScreenShare, null, Modifier.size(12.dp), tint = p.textFaint)
            }
        }
    }
}

@Composable
internal fun Field(value: String, hint: String, modifier: Modifier = Modifier.fillMaxWidth(), focus: FocusRequester? = null, onEnter: () -> Unit = {}, onChange: (String) -> Unit) {
    Box(modifier.height(30.dp).clip(RoundedCornerShape(6.dp)).background(p.input).padding(horizontal = 8.dp), Alignment.CenterStart) {
        if (value.isEmpty()) Text(hint, color = p.textFaint, fontSize = 12.sp)
        BasicTextField(
            value, onChange, Modifier.fillMaxWidth().then(if (focus != null) Modifier.focusRequester(focus) else Modifier).onPreviewKeyEvent {
                val enter = it.key == Key.Enter || it.key == Key.NumPadEnter
                if (enter && it.type == KeyEventType.KeyDown) onEnter()
                enter
            },
            singleLine = true, textStyle = TextStyle(color = p.text, fontSize = 12.sp), cursorBrush = SolidColor(p.accent),
        )
    }
}

@Composable
internal fun SmallButton(label: String, onClick: () -> Unit) =
    Text(label, Modifier.height(30.dp).rowClick(shape = RoundedCornerShape(6.dp), onClick = onClick).background(p.input).padding(horizontal = 10.dp, vertical = 7.dp),
        color = p.text, fontSize = 12.sp, maxLines = 1)

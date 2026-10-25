package kurz.ui

import kurz.core.*

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.KeyboardArrowRight
import androidx.compose.material.icons.automirrored.outlined.VolumeUp
import androidx.compose.material.icons.outlined.*
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
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
            Avatar(s.server.name, s.server.hue, 32.dp, rounded = 8.dp)
            Text(s.server.name, Modifier.weight(1f), color = p.text, fontSize = 15.sp, fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.Ellipsis)
            IconBtn(if (s.dark) Icons.Outlined.LightMode else Icons.Outlined.DarkMode, "Alternar tema", { s.dark = !s.dark }, size = 32.dp, iconSize = 18.dp)
        }
        LazyColumn(Modifier.weight(1f).fillMaxWidth().padding(horizontal = 8.dp)) {
            s.categories.forEach { cat ->
                val closed = cat.name in s.collapsed
                item(cat.name) {
                    Row(Modifier.padding(top = 16.dp, bottom = 4.dp).rowClick { s.toggleCategory(cat.name) }.padding(horizontal = 8.dp, vertical = 4.dp),
                        verticalAlignment = Alignment.CenterVertically) {
                        Text(cat.name, color = p.text, fontSize = 13.sp, fontWeight = FontWeight.SemiBold)
                        Icon(if (closed) Icons.AutoMirrored.Outlined.KeyboardArrowRight else Icons.Outlined.KeyboardArrowDown, null, Modifier.size(16.dp), tint = p.textMuted)
                    }
                }
                if (!closed) cat.channels.forEach { ch ->
                    item(ch.id) { ChannelRow(s, ch) }
                }
            }
        }
        s.voiceChannel?.let { vc ->
            Column(Modifier.fillMaxWidth().padding(horizontal = 8.dp, vertical = 8.dp).background(p.input, RoundedCornerShape(8.dp)).padding(10.dp),
                Arrangement.spacedBy(8.dp)) {
                Text("Voz ligada · ${vc.name}", color = p.online, fontSize = 12.sp, fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.Ellipsis)
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

@Composable
private fun ChannelRow(s: AppState, ch: Channel) {
    val voice = ch.kind == ChannelKind.Voice
    val connected = voice && s.voiceChannelId == ch.id
    val selected = if (voice) connected else !s.showVoice && s.textChannelId == ch.id
    Column(Modifier.fillMaxWidth()) {
        Row(Modifier.fillMaxWidth().height(rowHeight).rowClick(selected) { if (voice) s.joinVoice(ch.id) else s.openText(ch.id) }.padding(horizontal = 8.dp),
            Arrangement.spacedBy(8.dp), Alignment.CenterVertically) {
            val c = if (selected || connected) p.text else p.textMuted
            Icon(if (voice) Icons.AutoMirrored.Outlined.VolumeUp else Icons.Outlined.Tag, null, Modifier.size(16.dp), tint = c)
            Text(ch.name, color = c, fontSize = 13.sp, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, false))
            if (ch.locked) Icon(Icons.Outlined.Lock, null, Modifier.size(12.dp), tint = p.textFaint)
        }
        if (connected) s.voiceParticipants(ch.id).forEach { u ->
            Row(Modifier.fillMaxWidth().height(28.dp).padding(start = 32.dp), Arrangement.spacedBy(8.dp), Alignment.CenterVertically) {
                Avatar(u, 20.dp)
                Text(u.name, color = p.textMuted, fontSize = 12.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
        }
    }
}

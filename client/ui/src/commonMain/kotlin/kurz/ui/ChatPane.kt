package kurz.ui

import kurz.core.*

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.AddCircleOutline
import androidx.compose.material.icons.outlined.EmojiEmotions
import androidx.compose.material.icons.outlined.Forum
import androidx.compose.material.icons.outlined.MoreVert
import androidx.compose.material.icons.outlined.Notifications
import androidx.compose.material.icons.outlined.People
import androidx.compose.material.icons.outlined.PushPin
import androidx.compose.material.icons.automirrored.outlined.Send
import androidx.compose.material.icons.outlined.Tag
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.isShiftPressed
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.material.icons.automirrored.outlined.ArrowBack
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

@Composable
fun ChatPane(s: AppState, modifier: Modifier = Modifier) {
    Column(modifier.background(p.main)) {
        ChatHeader(s)
        Box(Modifier.fillMaxWidth().height(1.dp).background(p.border))
        MessageList(s, Modifier.weight(1f).fillMaxWidth())
        Composer(s)
    }
}

@Composable
private fun ChatHeader(s: AppState) {
    val wc = LocalWindowClass.current
    val narrow = wc == WindowClass.Compact
    Row(Modifier.fillMaxWidth().height(56.dp).padding(horizontal = if (narrow) 8.dp else 24.dp), verticalAlignment = Alignment.CenterVertically) {
        if (narrow) IconBtn(Icons.AutoMirrored.Outlined.ArrowBack, "Voltar", onClick = { s.closeContent() })
        Icon(Icons.Outlined.Tag, null, Modifier.size(18.dp), tint = p.textMuted)
        Spacer(Modifier.width(6.dp))
        Text(s.textChannel.name.lowercase(), color = p.text, fontSize = 15.sp, fontWeight = FontWeight.SemiBold, maxLines = 1)
        Spacer(Modifier.weight(1f))
        if (!narrow) {
            IconBtn(Icons.Outlined.PushPin, "Mensagens afixadas")
            IconBtn(Icons.Outlined.Notifications, "Notificações")
            IconBtn(Icons.Outlined.Forum, "Tópicos")
        }
        IconBtn(Icons.Outlined.People, "Participantes", onClick = { s.toggleMembers(wc) }, active = membersActive(s))
        IconBtn(Icons.Outlined.MoreVert, "Mais")
    }
}

@Composable
private fun MessageList(s: AppState, modifier: Modifier) {
    val msgs = s.messages[s.textChannelId].orEmpty()
    val state = rememberLazyListState()
    // desce até ao fim ao abrir o canal e a cada nova mensagem
    LaunchedEffect(s.textChannelId, msgs.size) { if (msgs.isNotEmpty()) state.scrollToItem(state.layoutInfo.totalItemsCount) }
    if (msgs.isEmpty()) {
        Box(modifier.padding(gutter), Alignment.Center) {
            Text("Ainda não há mensagens em #${s.textChannel.name.lowercase()}. Escreve a primeira.",
                color = p.textFaint, fontSize = 13.sp, textAlign = TextAlign.Center)
        }
        return
    }
    LazyColumn(modifier, state, contentPadding = PaddingValues(horizontal = gutter, vertical = 16.dp), verticalArrangement = Arrangement.spacedBy(20.dp)) {
        if (s.unread > 0) item {
            Box(Modifier.fillMaxWidth().height(32.dp).rowClick(shape = RoundedCornerShape(6.dp)) { s.unread = 0 }.background(p.input), Alignment.Center) {
                Text("MENSAGENS NÃO LIDAS · ${s.unread}", color = p.textFaint, fontSize = 11.sp, letterSpacing = 1.2.sp, fontWeight = FontWeight.Medium)
            }
        }
        items(msgs, key = { it.id }) { MessageRow(it) }
    }
}

@Composable
private fun MessageRow(m: Message) {
    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
        Avatar(m.author, 36.dp)
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Row(Modifier.fillMaxWidth().height(20.dp), verticalAlignment = Alignment.CenterVertically) {
                Text(m.author.name, color = p.text, fontSize = 14.sp, fontWeight = FontWeight.SemiBold, maxLines = 1, modifier = Modifier.weight(1f))
                Text(m.time, color = p.textFaint, fontSize = 12.sp)
            }
            if (m.photos.isNotEmpty()) Row(Modifier.widthIn(max = 640.dp).fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                // largura proporcional ao aspeto → mesma altura, e encolhe com o ecrã
                m.photos.forEach { PhotoView(it, Modifier.weight(it.aspect).aspectRatio(it.aspect)) }
            }
            Text(m.text, color = p.text, fontSize = 13.5.sp, lineHeight = 20.sp)
        }
    }
}

@Composable
private fun Composer(s: AppState) {
    var text by remember(s.textChannelId) { mutableStateOf("") }
    fun send() { if (text.isNotBlank()) { s.send(text); text = "" } }
    Row(
        Modifier.fillMaxWidth().padding(gutter).height(48.dp).clip(RoundedCornerShape(8.dp)).background(p.input).padding(horizontal = 14.dp),
        verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        Icon(Icons.Outlined.AddCircleOutline, "Anexar", Modifier.size(22.dp), tint = p.textMuted)
        Box(Modifier.weight(1f)) {
            if (text.isEmpty()) Text("A tua mensagem…", color = p.textFaint, fontSize = 13.5.sp)
            BasicTextField(
                text, { text = it }, Modifier.fillMaxWidth().onPreviewKeyEvent {
                    val enter = it.key == Key.Enter || it.key == Key.NumPadEnter
                    if (enter && !it.isShiftPressed) { if (it.type == KeyEventType.KeyDown) send(); true } else false
                },
                singleLine = true, textStyle = TextStyle(color = p.text, fontSize = 13.5.sp), cursorBrush = SolidColor(p.accent),
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Send), keyboardActions = KeyboardActions(onSend = { send() }),
            )
        }
        Icon(Icons.Outlined.EmojiEmotions, "Emojis", Modifier.size(20.dp), tint = p.textMuted)
        Box(Modifier.size(32.dp).rowClick(shape = RoundedCornerShape(8.dp)) { send() }, Alignment.Center) {
            Icon(Icons.AutoMirrored.Outlined.Send, "Enviar", Modifier.size(20.dp), tint = if (text.isBlank()) p.textMuted else p.accent)
        }
    }
}

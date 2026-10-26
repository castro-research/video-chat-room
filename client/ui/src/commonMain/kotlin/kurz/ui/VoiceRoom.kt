package kurz.ui

import kurz.core.*
import kurz.media.Capture
import kurz.media.Device
import kurz.media.MAX_VOLUME
import kurz.media.MediaCapture
import kurz.media.Participant
import kurz.media.peopleIn
import kurz.media.shownRoom
import kurz.media.user

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items as rowItems
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.ArrowBack
import androidx.compose.material.icons.automirrored.outlined.ScreenShare
import androidx.compose.material.icons.automirrored.outlined.VolumeOff
import androidx.compose.material.icons.automirrored.outlined.VolumeUp
import androidx.compose.material.icons.outlined.*
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.Slider
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
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.delay
import kotlin.math.roundToInt

@Composable
fun VoiceRoom(s: AppState, modifier: Modifier = Modifier) {
    val rooms = LocalRooms.current
    val real = shownRoom(s, rooms)      // sala real (manager + SFU), já otimista; senão, canal de exemplo
    val title = real?.name ?: s.roomId?.let { "A entrar…" } ?: s.voiceChannel?.name ?: return
    val inRoom = real?.let { peopleIn(it, s, rooms) }.orEmpty()
    val parts = inRoom.associateBy { it.userId }
    val people = when {
        s.roomId != null -> inRoom.map { it.user(s.me, rooms.userId) }.let { if (s.me in it) it else listOf(s.me) + it }
        else -> s.voiceParticipants(s.voiceChannelId!!)
    }
    val cap = LocalMediaCapture.current
    LaunchedEffect(Unit) { cap.refreshDevices() }
    val speaking = if (s.roomId != null) null else people.firstOrNull { it != s.me }  // sala real: sem indicação de quem fala
    val others = people.size - 1
    Column(modifier.background(p.main)) {
        // cabeçalho
        Row(Modifier.fillMaxWidth().height(56.dp).padding(horizontal = if (compact) 8.dp else 24.dp), verticalAlignment = Alignment.CenterVertically) {
            if (compact) IconBtn(Icons.AutoMirrored.Outlined.ArrowBack, "Voltar", onClick = { s.closeContent() })
            Icon(Icons.AutoMirrored.Outlined.VolumeUp, null, Modifier.size(18.dp), tint = p.textMuted)
            Spacer(Modifier.width(8.dp))
            Text(title, color = p.text, fontSize = 15.sp, fontWeight = FontWeight.SemiBold)
            s.roomId?.let { Text("  ·  código $it", color = p.textFaint, fontSize = 12.sp) }
            Text("  ·  ${people.size} ${if (people.size == 1) "pessoa ligada" else "pessoas ligadas"}", color = p.textFaint, fontSize = 12.sp)
            if (rooms.reconnecting) Text("  ·  a reconectar…", color = p.idle, fontSize = 12.sp, fontWeight = FontWeight.SemiBold)
        }
        Box(Modifier.fillMaxWidth().height(1.dp).background(p.border))
        // grelha; ao tocar num mosaico (pessoa ou partilha) ele fica em destaque e os outros ficam numa fila por baixo; tocar no destacado volta à grelha
        val slots = buildList<Pair<String, @Composable (Modifier) -> Unit>> {
            people.forEach { u -> add("u:${u.id}" to { m -> Tile(s, u, u == speaking, parts[u.id], m) }) }
            if (s.sharing && cap.screen == Capture.On) add("screen:me" to { m -> ScreenTile("${s.me.name} · ecrã", m) { cap.ScreenPreview(it) } })
            parts.values.filter { it.screen && it.userId != rooms.userId }.forEach { pt ->
                add("screen:${pt.userId}" to { m -> ScreenTile("${pt.name} · ecrã", m) { rooms.RemoteVideo(pt.userId, "screen", it) } })
            }
        }
        val big = slots.firstOrNull { it.first == s.focus }  // a chave pode já não existir (saiu, parou de partilhar): então é a grelha
        val shape = RoundedCornerShape(12.dp)
        Column(Modifier.weight(1f).fillMaxWidth()) {
            if (big == null) {
                LazyVerticalGrid(
                    GridCells.Adaptive(if (compact) 220.dp else 340.dp), Modifier.weight(1f).fillMaxWidth(),
                    contentPadding = PaddingValues(gutter),
                    horizontalArrangement = Arrangement.spacedBy(16.dp), verticalArrangement = Arrangement.spacedBy(16.dp),
                ) {
                    items(slots, key = { it.first }) { (k, tile) -> tile(Modifier.fillMaxWidth().aspectRatio(16f / 9f).rowClick(shape = shape) { s.focus = k }) }
                }
                if (others == 0) Text(
                    "Ainda ninguém mais está nesta sala. Quem entrar aparece aqui.", color = p.textFaint, fontSize = 13.sp,
                    modifier = Modifier.fillMaxWidth().padding(bottom = 24.dp), textAlign = androidx.compose.ui.text.style.TextAlign.Center,
                )
            } else {
                Box(Modifier.weight(1f).fillMaxWidth().padding(gutter)) {
                    big.second(Modifier.fillMaxSize().rowClick(shape = shape) { s.focus = null })
                    Box(Modifier.fillMaxSize().border(2.dp, p.accent, shape))  // o destacado leva o contorno de acento
                    Row(
                        Modifier.align(Alignment.TopStart).padding(10.dp).clip(RoundedCornerShape(6.dp)).background(Color.Black.copy(alpha = 0.55f)).padding(horizontal = 8.dp, vertical = 3.dp),
                        Arrangement.spacedBy(6.dp), Alignment.CenterVertically,
                    ) {
                        Icon(Icons.Outlined.CloseFullscreen, null, Modifier.size(14.dp), tint = Color.White)
                        Text("Voltar à grelha", color = Color.White, fontSize = 12.sp)
                    }
                }
                LazyRow(Modifier.fillMaxWidth(), contentPadding = PaddingValues(start = gutter, end = gutter, bottom = gutter), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    rowItems(slots.filter { it !== big }, key = { it.first }) { (k, tile) -> tile(Modifier.height(96.dp).aspectRatio(16f / 9f).rowClick(shape = shape) { s.focus = k }) }
                }
            }
        }
        // controlos
        Box(Modifier.fillMaxWidth().height(1.dp).background(p.border))
        cap.notice?.let { Notice(it) { cap.notice = null } }
        Row(Modifier.fillMaxWidth().height(80.dp), Arrangement.Center, Alignment.CenterVertically) {
            Ctl(if (s.muted) Icons.Outlined.MicOff else Icons.Outlined.Mic, "Microfone", !s.muted) { s.muted = !s.muted }
            DeviceMenu("microfone", cap.micDevices, cap.micDeviceId, "Padrão do sistema", cap::refreshDevices, defaultDetail = cap.micDefaultName) { cap.selectMic(it) }
            Spacer(Modifier.width(12.dp))
            Ctl(if (s.camOn) Icons.Outlined.Videocam else Icons.Outlined.VideocamOff, "Câmara", s.camOn) { s.camOn = !s.camOn }
            DeviceMenu("câmara", cap.cameraDevices, cap.cameraDeviceId, null, cap::refreshDevices, ready = { cap.cameraReady[it.id] }) { it?.let(cap::selectCamera) }
            Spacer(Modifier.width(12.dp))
            ShareControl(s, cap)
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

/**
 * Seta ao lado do botão: abre a lista de dispositivos, com uma bolinha no escolhido. [defaultLabel] = entrada "padrão do sistema" (null se não houver),
 * com [defaultDetail] (o dispositivo que o sistema usa) por baixo. [ready]: null = a verificar, false = não dá; ambos ficam desactivados com o motivo.
 * Sem dispositivos não aparece.
 */
@Composable
private fun DeviceMenu(
    what: String, devices: List<Device>, selected: String?, defaultLabel: String?, onOpen: () -> Unit,
    defaultDetail: String? = null, ready: (Device) -> Boolean? = { true }, onPick: (String?) -> Unit,
) {
    if (devices.isEmpty()) return
    var open by remember { mutableStateOf(false) }
    Box {
        Box(Modifier.size(24.dp).rowClick(shape = CircleShape) { onOpen(); open = true }, Alignment.Center) {
            Icon(Icons.Outlined.KeyboardArrowUp, "Escolher $what", Modifier.size(16.dp), tint = p.textMuted)
        }
        DropdownMenu(open, { open = false }, containerColor = p.sidebar) {
            @Composable fun entry(id: String?, label: String, detail: String?, ok: Boolean?) = DropdownMenuItem(
                enabled = ok == true,
                text = {
                    Column(Modifier.widthIn(min = 220.dp, max = 320.dp)) {
                        Text(label, color = if (ok == true) p.text else p.textFaint, fontSize = 13.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        (if (ok == null) "a verificar…" else if (!ok) "sem imagem" else detail)?.let { Text(it, color = p.textFaint, fontSize = 11.sp, maxLines = 1, overflow = TextOverflow.Ellipsis) }
                    }
                },
                trailingIcon = { Radio(id == selected) },
                onClick = { open = false; onPick(id) },
            )
            defaultLabel?.let { entry(null, it, defaultDetail, true) }
            devices.forEach { entry(it.id, it.name, null, ready(it)) }
        }
    }
}

@Composable
private fun Radio(on: Boolean) {
    Box(Modifier.size(18.dp).border(2.dp, if (on) p.accent else p.textFaint, CircleShape), Alignment.Center) {
        if (on) Box(Modifier.size(9.dp).background(p.accent, CircleShape))
    }
}

/** Ligada, o botão pára a partilha; desligada (ou na seta) abre a modal de ecrãs e janelas (SharePicker). Sem lista (Android/iOS) liga logo. */
@Composable
private fun ShareControl(s: AppState, cap: MediaCapture) {
    fun pick() { cap.refreshScreenSources(); s.sharePickerOpen = true }
    Row(verticalAlignment = Alignment.CenterVertically) {
        Ctl(Icons.AutoMirrored.Outlined.ScreenShare, "Partilhar ecrã", s.sharing, offTint = p.textMuted) {
            if (s.sharing) s.sharing = false else if (cap.canPickScreen) pick() else s.sharing = true
        }
        if (cap.canPickScreen) Box(Modifier.size(24.dp).rowClick(shape = CircleShape) { pick() }, Alignment.Center) {
            Icon(Icons.Outlined.KeyboardArrowUp, "Escolher o que partilhar", Modifier.size(16.dp), tint = p.textMuted)
        }
    }
}

@Composable
private fun Ctl(icon: ImageVector, desc: String, active: Boolean, offTint: Color = p.danger, onClick: () -> Unit) {
    Box(Modifier.size(48.dp).rowClick(selected = active, shape = CircleShape, onClick = onClick), Alignment.Center) {
        Icon(icon, desc, Modifier.size(22.dp), tint = if (active) p.text else offTint)
    }
}

/** [part]: o participante da sala real (fontes publicadas), null nos canais de exemplo. [modifier] traz o tamanho e o toque (ver VoiceRoom). */
@Composable
private fun Tile(s: AppState, u: User, otherSpeaking: Boolean, part: Participant?, modifier: Modifier) {
    val me = u == s.me
    val cap = LocalMediaCapture.current
    val muted = if (me) s.muted else part?.mic == false
    val micOn = me && cap.mic == Capture.On && !s.muted
    // o próprio fala quando o volume real do microfone passa o limiar (~ -42 dB)
    val speaking = if (me) micOn && cap.micLevel > 0.3f else otherSpeaking
    val shape = RoundedCornerShape(12.dp)
    BoxWithConstraints(
        modifier.background(p.sidebar).border(if (speaking) 2.dp else 1.dp, if (speaking) p.accent else p.border, shape),
        Alignment.Center,
    ) {
        if (me && s.camOn && cap.camera == Capture.On) cap.CameraPreview(Modifier.fillMaxSize()) else Avatar(u, (maxHeight * 0.4f).coerceAtMost(88.dp))
        if (!me && part?.camera == true) LocalRooms.current.RemoteVideo(u.id, "camera", Modifier.fillMaxSize())  // tapa o avatar quando chega o 1.º frame
        Text(
            if (me) "${u.name} (tu)" else u.name, color = Color.White, fontSize = 12.sp, maxLines = 1,
            modifier = Modifier.align(Alignment.BottomStart).padding(10.dp).clip(RoundedCornerShape(6.dp))
                .background(Color.Black.copy(alpha = 0.55f)).padding(horizontal = 8.dp, vertical = 3.dp),
        )
        if (micOn) Box(Modifier.align(Alignment.BottomStart).fillMaxWidth(cap.micLevel).height(3.dp).background(p.accent))
        if (!me && part != null) VolumeControl(u.id, Modifier.align(Alignment.TopStart).padding(10.dp))
        if (muted) Icon(
            Icons.Outlined.MicOff, null, tint = p.danger,
            modifier = Modifier.align(Alignment.TopEnd).padding(10.dp).size(24.dp).clip(CircleShape)
                .background(Color.Black.copy(alpha = 0.55f)).padding(4.dp),
        )
    }
}

/** Botão no mosaico de um participante que abre um menu com o volume com que o ouves (só para ti), de 0 a 200 %. */
@Composable
private fun VolumeControl(userId: String, modifier: Modifier) {
    val rooms = LocalRooms.current
    if (!rooms.canSetVolume) return
    var open by remember { mutableStateOf(false) }
    val v = rooms.volume(userId)
    Box(modifier) {
        Icon(
            if (v == 0f) Icons.AutoMirrored.Outlined.VolumeOff else Icons.AutoMirrored.Outlined.VolumeUp, "Volume", tint = Color.White,
            modifier = Modifier.size(24.dp).clip(CircleShape).background(Color.Black.copy(alpha = 0.55f)).rowClick(shape = CircleShape) { open = true }.padding(4.dp),
        )
        DropdownMenu(open, { open = false }, containerColor = p.sidebar) {
            Row(Modifier.width(220.dp).padding(horizontal = 12.dp), Arrangement.spacedBy(10.dp), Alignment.CenterVertically) {
                Slider(v, { rooms.setVolume(userId, it) }, Modifier.weight(1f), valueRange = 0f..MAX_VOLUME)
                Text("${(v * 100).roundToInt()}%", color = p.text, fontSize = 12.sp, modifier = Modifier.width(40.dp))
            }
        }
    }
}

/** Uma partilha de ecrã (própria ou remota), num mosaico à parte. */
@Composable
private fun ScreenTile(label: String, modifier: Modifier, video: @Composable (Modifier) -> Unit) {
    val shape = RoundedCornerShape(12.dp)
    Box(modifier.background(Color.Black).border(1.dp, p.border, shape), Alignment.Center) {
        video(Modifier.fillMaxSize())
        Text(
            label, color = Color.White, fontSize = 12.sp, maxLines = 1,
            modifier = Modifier.align(Alignment.BottomStart).padding(10.dp).clip(RoundedCornerShape(6.dp))
                .background(Color.Black.copy(alpha = 0.55f)).padding(horizontal = 8.dp, vertical = 3.dp),
        )
    }
}

/** Aviso de permissão/dispositivo em falta ou de falha de ligação; toque para fechar, fecha sozinho ao fim de alguns segundos. */
@Composable
internal fun Notice(text: String, onDismiss: () -> Unit) {
    LaunchedEffect(text) { delay(8_000); onDismiss() }
    Row(
        Modifier.fillMaxWidth().background(p.danger.copy(alpha = 0.12f)).rowClick(shape = RoundedCornerShape(0.dp)) { onDismiss() }
            .padding(horizontal = gutter, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        Icon(Icons.Outlined.ErrorOutline, null, Modifier.size(18.dp), tint = p.danger)
        Text(text, color = p.text, fontSize = 12.5.sp, modifier = Modifier.weight(1f))
        Icon(Icons.Outlined.Close, "Fechar", Modifier.size(16.dp), tint = p.textMuted)
    }
}

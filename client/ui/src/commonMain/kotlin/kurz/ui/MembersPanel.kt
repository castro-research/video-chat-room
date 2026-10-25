package kurz.ui

import kurz.core.*

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.Search
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

// Painel direito de membros (288 dp). Pesquisa filtra admins e participantes por nome.
@Composable
fun MembersPanel(s: AppState, modifier: Modifier = Modifier, onClose: (() -> Unit)? = null) {
    var query by remember { mutableStateOf("") }
    val q = query.trim()
    val admins = s.admins.filter { it.name.contains(q, ignoreCase = true) }
    val participants = s.participants.filter { it.name.contains(q, ignoreCase = true) }

    Row(modifier.background(p.panel)) {
        Box(Modifier.fillMaxHeight().width(1.dp).background(p.border))
        Column(Modifier.weight(1f).fillMaxHeight()) {
            Row(Modifier.fillMaxWidth().height(56.dp).padding(start = 16.dp, end = if (onClose != null) 8.dp else 16.dp), verticalAlignment = Alignment.CenterVertically) {
                Box(Modifier.weight(1f)) { SearchField(query, { query = it }, Modifier) }
                if (onClose != null) IconBtn(Icons.Outlined.Close, "Fechar", onClose)
            }
            Box(Modifier.fillMaxWidth().height(1.dp).background(p.border))
            LazyColumn(contentPadding = PaddingValues(start = 8.dp, end = 8.dp, bottom = 16.dp)) {
                item { SectionHeader("Admins", s.admins.size) }
                items(admins, key = { it.id }) { MemberRow(it) }
                item { SectionHeader("Participantes", s.participantCount, action = "Ver todos") }
                items(participants, key = { it.id }) { MemberRow(it) }
                if (admins.isEmpty() && participants.isEmpty()) {
                    item {
                        Text("Nenhum participante encontrado.", color = p.textFaint, fontSize = 12.sp,
                            modifier = Modifier.padding(top = 16.dp))
                    }
                }
            }
        }
    }
}

@Composable
private fun SearchField(value: String, onChange: (String) -> Unit, modifier: Modifier) {
    Row(
        modifier.fillMaxWidth().height(36.dp).clip(RoundedCornerShape(8.dp)).background(p.input).padding(horizontal = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(Icons.Outlined.Search, null, Modifier.size(16.dp), tint = p.textFaint)
        Spacer(Modifier.width(8.dp))
        Box(Modifier.weight(1f)) {
            if (value.isEmpty()) Text("Pesquisar participantes…", color = p.textFaint, fontSize = 13.sp)
            BasicTextField(
                value = value,
                onValueChange = onChange,
                singleLine = true,
                textStyle = TextStyle(color = p.text, fontSize = 13.sp),
                cursorBrush = SolidColor(p.accent),
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}

@Composable
private fun SectionHeader(title: String, count: Int, action: String? = null) {
    Row(
        Modifier.fillMaxWidth().padding(start = 8.dp, end = if (action != null) 4.dp else 8.dp, top = 16.dp, bottom = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(title, color = p.text, fontSize = 13.sp, fontWeight = FontWeight.SemiBold)
        Spacer(Modifier.width(6.dp))
        Text("$count", color = p.textFaint, fontSize = 12.sp)
        Spacer(Modifier.weight(1f))
        if (action != null) {
            Text(action, color = p.link, fontSize = 12.sp, fontWeight = FontWeight.Medium,
                modifier = Modifier.rowClick(shape = RoundedCornerShape(4.dp)) {}.padding(horizontal = 4.dp))
        }
    }
}

@Composable
private fun MemberRow(u: User) {
    val offline = u.status == Status.Offline
    Row(
        Modifier.fillMaxWidth().height(if (compact) 56.dp else 48.dp).rowClick().padding(horizontal = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Avatar(u, 32.dp, showStatus = true, dotBorder = p.panel)
        Spacer(Modifier.width(10.dp))
        Column(Modifier.weight(1f)) {
            Text(u.name, color = if (offline) p.textMuted else p.text, fontSize = 13.sp,
                fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.Ellipsis)
            Text(withHeart(u.subtitle.orEmpty(), p.danger), color = p.textFaint, fontSize = 11.sp,
                maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
        u.badge?.let {
            Spacer(Modifier.width(8.dp))
            Badge(it)
        }
    }
}

/** Subtítulo com o "❤" pintado a [heart]. */
private fun withHeart(text: String, heart: Color): AnnotatedString = buildAnnotatedString {
    text.split("❤").forEachIndexed { i, part ->
        if (i > 0) withStyle(SpanStyle(color = heart)) { append("❤") }
        append(part)
    }
}

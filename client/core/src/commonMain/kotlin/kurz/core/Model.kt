package kurz.core

import androidx.compose.runtime.*

// Modelo + estado com dados de exemplo da referência. Dono: fundação — agentes não editam.
// Canais de voz de exemplo: joinVoice / leaveVoice. Salas reais (manager + SFU): joinRoom / roomId, mídia em kurz.media.RoomClient.

enum class Status { Online, Idle, Offline }
enum class ChannelKind { Text, Voice }

data class User(
    val id: String,
    val name: String,
    val status: Status,
    val subtitle: String? = null,   // estado personalizado ("Designing on Figma! ❤") ou "Online"/"Offline"
    val badge: String? = null,      // "ADMIN" | "MOD"
    val hue: Float = 230f,          // cor do avatar gerado
)

data class Server(val id: String, val name: String, val hue: Float)
data class Channel(val id: String, val name: String, val kind: ChannelKind, val locked: Boolean = false)
data class Category(val name: String, val channels: List<Channel>)

/** Imagem de exemplo desenhada com gradiente (sem ficheiros). [aspect] = largura/altura. */
data class Photo(val sky: Long, val horizon: Long, val ground: Long, val aspect: Float)  // cores ARGB

data class Message(
    val id: String,
    val author: User,
    val time: String,
    val text: String,
    val photos: List<Photo> = emptyList(),
)

private val guestNames = listOf("Lontra", "Raposa", "Coruja", "Tucano", "Capivara", "Gaivota", "Tatu", "Jaguar", "Arara", "Sagui")

private val lake = Photo(0xFF3F8FB5, 0xFFF0A58C, 0xFF1F4A3C, 2.4f)
private val sunset = Photo(0xFF6C7FC9, 0xFFF08F8F, 0xFF3A3F57, 1.0f)

class AppState(dark: Boolean = false, private val defaultServer: String = BuildInfo.defaultServer) {
    // --- pessoas ---
    // Sem login: nome aleatório, só em memória (muda a cada arranque). ponytail: perfil e persistência quando houver contas.
    val me = User("me", "${guestNames.random()} ${(10..99).random()}", Status.Online, "Online", hue = (0..359).random().toFloat())
    private val krishna = User("krishna", "Krishna", Status.Online, "Coding on VS Code.", "ADMIN", 150f)
    private val radha = User("radha", "Radha", Status.Offline, "Offline", "ADMIN", 330f)
    private val vrinda = User("vrinda", "Vrinda", Status.Online, "Online", "MOD", 20f)
    private val madhav = User("madhav", "Madhav", Status.Online, "Offline", "MOD", 95f)
    private val govind = User("govind", "Govind", Status.Offline, "Offline", null, 50f)
    private val keshav = User("keshav", "Keshav", Status.Offline, "Offline", null, 255f)
    private val shyaam = User("shyaam", "Shyaam", Status.Offline, "Offline", null, 170f)
    private val kishori = User("kishori", "Kishori", Status.Offline, "Offline", null, 280f)
    private val gopi = User("gopi", "Gopi", Status.Offline, "Offline", null, 40f)

    val admins = listOf(me, krishna, radha)
    val participants = listOf(vrinda, madhav, govind, keshav, shyaam, kishori, gopi)
    val participantCount = 24

    // --- servidores e canais ---
    // o servidor ligado (ver "servidores" mais abaixo), com o cabeçalho da barra lateral a mostrá-lo
    val server get() = Server(managerUrl, serverLabel(managerUrl), ((managerUrl.hashCode() and 0x7fffffff) % 360).toFloat())

    val categories = listOf(
        Category("Information", listOf(
            Channel("announcements", "Announcements", ChannelKind.Text), Channel("info", "Info", ChannelKind.Text),
            Channel("faq", "FAQ", ChannelKind.Text), Channel("rules", "Rules", ChannelKind.Text),
        )),
        Category("Channels", listOf(
            Channel("feedback", "Feedback", ChannelKind.Voice), Channel("user-interface", "User-Interface", ChannelKind.Voice),
            Channel("dev-discussions", "Dev-discussions", ChannelKind.Voice), Channel("events", "Events", ChannelKind.Voice, locked = true),
            Channel("meetings", "Meetings", ChannelKind.Voice, locked = true),
        )),
        Category("Music & Bots", listOf(
            Channel("music-voice", "Music-Voice-Channel", ChannelKind.Voice), Channel("bot-commands", "Bot-Commands", ChannelKind.Text),
        )),
        Category("General", listOf(
            Channel("general-talk", "General-talk", ChannelKind.Voice), Channel("general-text", "General-text", ChannelKind.Text),
        )),
        Category("Work Space", listOf(
            Channel("discussions", "Discussions", ChannelKind.Voice), Channel("development", "Development", ChannelKind.Text),
        )),
    )
    val collapsed = mutableStateSetOf<String>()          // nomes de categorias fechadas
    fun toggleCategory(name: String) { if (!collapsed.remove(name)) collapsed.add(name) }

    private val allChannels get() = categories.flatMap { it.channels }
    fun channel(id: String) = allChannels.first { it.id == id }

    // --- navegação ---
    var textChannelId by mutableStateOf("general-text")  // canal de texto aberto
    var voiceChannelId by mutableStateOf<String?>(null)  // sala de voz ligada
    var viewVoice by mutableStateOf(false)               // área principal mostra a sala de voz
    val showVoice get() = inVoice && viewVoice
    val textChannel get() = channel(textChannelId)
    val voiceChannel get() = voiceChannelId?.let { channel(it) }
    // Em ecrãs estreitos (telemóvel) mostra-se um painel de cada vez: false = lista de canais, true = chat/sala.
    var contentOpen by mutableStateOf(false)
    fun closeContent() { contentOpen = false }
    fun openText(id: String) { textChannelId = id; viewVoice = false; contentOpen = true }
    fun joinVoice(id: String) { roomId = null; focus = null; voiceChannelId = id; viewVoice = true; contentOpen = true }   // exemplo: sem mídia remota
    var focus by mutableStateOf<String?>(null)           // mosaico da sala de voz em destaque (chave do VoiceRoom); null = grelha
    fun openVoiceRoom() { viewVoice = true; contentOpen = true }
    fun leaveVoice() {
        val viewing = showVoice
        voiceChannelId = null; roomId = null; viewVoice = false; muted = false; camOn = false; sharing = false; focus = null
        if (viewing) contentOpen = false
    }

    // --- servidores (managers) ---
    // ponytail: só em memória; os adicionados somem ao fechar a app (persistir quando houver onde guardar)
    val servers = mutableStateListOf(defaultServer)      // o de fábrica primeiro
    var managerUrl by mutableStateOf(defaultServer)      // o ligado: as salas vêm dele
    var serversOpen by mutableStateOf(false)             // modal de servidores
    val health = mutableStateMapOf<String, Boolean>()    // servidor -> responde ao /healthz; sem entrada = ainda por saber
    /** Muda de servidor; as salas são de cada um, por isso sai da sala em que estiver. */
    fun selectServer(url: String) { if (url == managerUrl) return; if (inVoice) leaveVoice(); managerUrl = url }
    /** Adiciona o servidor escrito e liga-se a ele; false se não for um endereço. */
    fun addServer(input: String): Boolean {
        val url = normalizeServer(input) ?: return false
        if (url !in servers) servers.add(url)
        selectServer(url)
        return true
    }
    /** Tira um servidor adicionado (o de fábrica fica); se era o ligado, volta ao de fábrica. */
    fun removeServer(url: String) {
        if (url == defaultServer) return
        servers.remove(url)
        if (managerUrl == url) selectServer(defaultServer)
    }

    // --- salas reais: o manager dá a sala, o SFU a mídia (kurz.media.RoomClient faz a ligação) ---
    var roomId by mutableStateOf<String?>(null)          // sala real ligada; exclui um canal de voz de exemplo
    val inVoice get() = voiceChannelId != null || roomId != null
    var createRoomOpen by mutableStateOf(false)          // modal de criar canal
    var sharePickerOpen by mutableStateOf(false)         // modal de escolher o que partilhar (ecrã/janela)
    fun joinRoom(id: String) { voiceChannelId = null; focus = null; roomId = id; viewVoice = true; contentOpen = true }

    private val voiceUsers = mapOf("general-talk" to listOf(govind, keshav), "dev-discussions" to listOf(krishna), "discussions" to listOf(vrinda))
    /** Quem está na sala de voz [id]; inclui o próprio utilizador se estiver ligado. */
    fun voiceParticipants(id: String): List<User> = (voiceUsers[id] ?: emptyList()) + if (voiceChannelId == id) listOf(me) else emptyList()

    // --- controlos de voz / aparência / layout ---
    var muted by mutableStateOf(false)
    var camOn by mutableStateOf(false)
    var sharing by mutableStateOf(false)
    var dark by mutableStateOf(dark)
    var showMembers by mutableStateOf(true)         // painel fixo (ecrã largo)
    var membersOverlay by mutableStateOf(false)     // gaveta de membros (ecrã médio/estreito)

    // --- mensagens ---
    var unread by mutableStateOf(6)
    val messages = mutableStateMapOf(
        "general-text" to listOf(
            Message("1", govind, "13:40", "Heyy look! These are pics from my recent vacation!!", listOf(lake, sunset)),
            Message("2", me, "13:42", "Oh wow! This looks amazing...\nI wish I could go with you! But I'm really busy these days"),
            Message("3", vrinda, "13:44", "Haha! I loved these pics on Google!!!!\nTry picking some rare pictures atleast... It will be great for you. 🤣"),
            Message("4", govind, "13:44", "Oops! I try harder next time! 🤣🤣"),
        ),
    )
    fun send(text: String) {
        if (text.isBlank()) return
        val list = messages[textChannelId] ?: emptyList()
        messages[textChannelId] = list + Message("m${list.size + 1}-$textChannelId", me, "agora", text.trim())
    }
}

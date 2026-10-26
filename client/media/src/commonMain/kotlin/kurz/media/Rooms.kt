package kurz.media

import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import kotlin.random.Random

// Salas reais: REST do manager + WebSocket de mídia do SFU (experimentos/README.md, "Interface entre os serviços").

@Serializable data class Room(val id: String, val name: String, val participants: List<Participant> = emptyList())

/** [mic]/[camera]/[screen]: a fonte está publicada no SFU. */
@Serializable data class Participant(val userId: String, val name: String, val mic: Boolean = false, val camera: Boolean = false, val screen: Boolean = false)

@Serializable data class IceServer(val urls: List<String>, val username: String? = null, val credential: String? = null)

/** Resposta de `POST /v1/rooms/{id}/join` (o `participants` do contrato não é usado: a lista vem de `GET /v1/rooms/{id}`). */
@Serializable data class VoiceSession(val media: Media, val iceServers: List<IceServer> = emptyList()) {
    @Serializable data class Media(val url: String, val token: String)
}

@Serializable internal data class RoomList(val rooms: List<Room>)
@Serializable internal data class ApiError(val code: String, val message: String)

internal val json = Json { ignoreUnknownKeys = true }

/** Frame `{op, d}` do WebSocket de mídia (contract/openapi.yaml, `Media*`). */
fun mediaFrame(op: String, d: JsonObject = JsonObject(emptyMap())): String = buildJsonObject { put("op", op); put("d", d) }.toString()
fun mediaFrame(op: String, vararg d: Pair<String, String>): String = mediaFrame(op, buildJsonObject { d.forEach { (k, v) -> put(k, v) } })

/** (op, d) de um frame recebido; d vazio se faltar. */
fun parseMediaFrame(text: String): Pair<String, JsonObject> {
    val o = json.parseToJsonElement(text).jsonObject
    return o.getValue("op").jsonPrimitive.content to (o["d"] as? JsonObject ?: JsonObject(emptyMap()))
}

/**
 * Cliente das salas reais. A UI faz o ritmo (lista a cada 3 s, participantes a cada 2 s) e mostra [notice];
 * a plataforma faz o resto (HTTP, WebSocket, PeerConnection). Uma instância por janela: [rememberRoomClient].
 */
abstract class RoomClient {
    /** Id deste cliente nas salas: o contrato deixa o userId livre e não há login. */
    val userId = "u" + Random.nextLong().toULong().toString(36).take(8)

    var rooms by mutableStateOf<List<Room>>(emptyList()); protected set
    /** Erro da última listagem (manager fora, URL errada); null se correu bem. */
    var listError by mutableStateOf<String?>(null); protected set
    /** Sala ligada, com os participantes da última consulta; volta a null se a ligação ao SFU cair. */
    var room by mutableStateOf<Room?>(null); protected set
    /** A ligação ao SFU caiu e está a tentar voltar: [room] mantém-se (a UI fica na sala) até [reconnect] conseguir ou a UI desistir com [leave]. */
    var reconnecting by mutableStateOf(false); protected set
    /** Última falha de uma acção, em português, para mostrar ao utilizador. */
    var notice by mutableStateOf<String?>(null)

    /** Volume com que se ouve cada participante, 0..[MAX_VOLUME] (1 = normal); só vale para quem se mexeu. */
    var volumes by mutableStateOf<Map<String, Float>>(emptyMap()); protected set
    fun volume(userId: String) = volumes[userId] ?: 1f
    /** Muda o volume de [userId] só para este cliente (voz e áudio da partilha). */
    open fun setVolume(userId: String, v: Float) {}
    /** A plataforma sabe mudar o volume de cada participante. */
    open val canSetVolume get() = false

    abstract suspend fun list(base: String)
    /** O manager em [base] responde a `GET /healthz`. Nunca lança: qualquer falha é false. */
    abstract suspend fun ping(base: String): Boolean
    abstract suspend fun create(base: String, name: String): Room?
    /** Entra na sala [id] e liga a mídia; devolve false (com [notice]) se falhar. */
    abstract suspend fun join(base: String, id: String, name: String): Boolean
    /** Volta a entrar na sala [id] depois de a ligação cair, sem mexer em [room] nem avisar de falhas; true se voltou. */
    open suspend fun reconnect(base: String, id: String, name: String): Boolean = false
    /** Actualiza os participantes de [room]. */
    abstract suspend fun poll(base: String)
    abstract fun leave()

    /** Último frame da track remota `<userId>:<source>`; nada enquanto não houver. */
    @Composable abstract fun RemoteVideo(userId: String, source: String, modifier: Modifier)
}

const val MAX_VOLUME = 2f

/** Android e iOS: compila a parte comum, mas sem mídia real. */
class UnsupportedRooms : RoomClient() {
    private val msg = "Salas com áudio e vídeo ainda não são suportadas nesta plataforma."
    override suspend fun list(base: String) { listError = msg }
    override suspend fun ping(base: String) = false
    override suspend fun create(base: String, name: String): Room? { notice = msg; return null }
    override suspend fun join(base: String, id: String, name: String): Boolean { notice = msg; return false }
    override suspend fun poll(base: String) {}
    override fun leave() { room = null }
    @Composable override fun RemoteVideo(userId: String, source: String, modifier: Modifier) {}
}

@Composable
expect fun rememberRoomClient(capture: MediaCapture): RoomClient

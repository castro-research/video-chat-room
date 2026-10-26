package kurz.media

import androidx.compose.foundation.Image
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.layout.ContentScale
import dev.onvoid.webrtc.CreateSessionDescriptionObserver
import dev.onvoid.webrtc.PeerConnectionFactory
import dev.onvoid.webrtc.PeerConnectionObserver
import dev.onvoid.webrtc.RTCAnswerOptions
import dev.onvoid.webrtc.RTCConfiguration
import dev.onvoid.webrtc.RTCIceCandidate
import dev.onvoid.webrtc.RTCIceGatheringState
import dev.onvoid.webrtc.RTCIceServer
import dev.onvoid.webrtc.RTCOfferOptions
import dev.onvoid.webrtc.RTCPeerConnection
import dev.onvoid.webrtc.RTCPeerConnectionState
import dev.onvoid.webrtc.RTCRtpReceiver
import dev.onvoid.webrtc.RTCRtpSender
import dev.onvoid.webrtc.RTCRtpTransceiver
import dev.onvoid.webrtc.RTCRtpTransceiverDirection
import dev.onvoid.webrtc.RTCRtpTransceiverInit
import dev.onvoid.webrtc.RTCSdpType
import dev.onvoid.webrtc.RTCSessionDescription
import dev.onvoid.webrtc.RTCSignalingState
import dev.onvoid.webrtc.SetSessionDescriptionObserver
import dev.onvoid.webrtc.media.MediaStreamTrack
import dev.onvoid.webrtc.media.video.VideoTrack
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.future.await
import kotlinx.coroutines.launch
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withTimeoutOrNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import java.io.IOException
import java.net.URI
import java.net.URLEncoder
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.net.http.WebSocket
import java.time.Duration
import java.util.concurrent.CompletionStage
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

@Composable
actual fun rememberRoomClient(capture: MediaCapture): RoomClient {
    val r = remember(capture) { DesktopRooms(capture as? DesktopCapture) }
    DisposableEffect(r) { onDispose(r::leave) }
    return r
}

private val closer = CoroutineScope(SupervisorJob() + Dispatchers.IO)

internal class DesktopRooms(private val capture: DesktopCapture?) : RoomClient() {
    private var session: MediaSession? = null
    private val remote = mutableStateMapOf<String, FrameSink>()  // track id `<userId>:<source>` -> frames

    override suspend fun list(base: String) {
        try { rooms = Manager(base).rooms(); listError = null } catch (e: CancellationException) { throw e } catch (e: Exception) { listError = describe(e) }
    }

    // já entra na lista (a UI mostra a sala que se vai abrir sem esperar pela listagem seguinte)
    override suspend fun create(base: String, name: String): Room? = attempt { Manager(base).create(name) }?.also { n -> rooms = rooms.filter { it.id != n.id } + n }

    override suspend fun join(base: String, id: String, name: String): Boolean {
        leave()
        return attempt {
            val m = Manager(base)
            val r = m.room(id)
            lateinit var s: MediaSession
            s = MediaSession(m.join(r.id, userId, name), userId, rtc, ::remoteTrack) { reason ->
                if (session === s) { leave(); notice = reason }
            }
            s.connect()
            session = s; room = r
            // a captura já ligada entra agora; o resto entra/sai quando os botões mudarem
            capture?.let { c -> c.tracks.forEach { (src, t) -> s.publish(src, t) }; c.onTrack = s::publish }
        } != null
    }

    override suspend fun poll(base: String) {
        val id = room?.id ?: return
        // falha pontual: fica a última lista
        val r = try { Manager(base).room(id) } catch (e: CancellationException) { throw e } catch (e: Exception) { return }
        if (room?.id == id) room = r
    }

    override fun leave() {
        capture?.onTrack = null
        val s = session
        session = null; room = null
        // fora da thread da UI: fechar a PeerConnection nativa espera pelas threads do libwebrtc (mais com vídeo a correr)
        if (s != null) closer.launch { s.close() }
        remote.clear()
    }

    private fun remoteTrack(track: MediaStreamTrack, added: Boolean) {
        if (track !is VideoTrack) return  // o áudio remoto toca sozinho pelo AudioDeviceModule
        remote.remove(track.id)?.let(track::removeSink)
        if (added) remote[track.id] = FrameSink().also(track::addSink)
    }

    @Composable
    override fun RemoteVideo(userId: String, source: String, modifier: Modifier) {
        val fit = if (source == "screen") ContentScale.Fit else ContentScale.Crop
        remote["$userId:$source"]?.image?.let { Image(it, null, modifier, contentScale = fit) }
    }

    private suspend fun <T> attempt(block: suspend () -> T): T? =
        try { block() } catch (e: CancellationException) { throw e } catch (e: Exception) { notice = describe(e); null }
}

private fun describe(e: Exception) = if (e is IOException) e.message ?: "Erro de rede." else "Resposta inesperada do servidor (${e::class.simpleName})."

private val http = HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(3)).build()

/** REST do manager (experimentos/README.md). Erros viram IOException com mensagem em português. */
class Manager(base: String) {
    private val base = base.trim().trimEnd('/')

    suspend fun rooms(): List<Room> = json.decodeFromString<RoomList>(call("GET", "/v1/rooms")).rooms
    suspend fun room(id: String): Room = json.decodeFromString(call("GET", "/v1/rooms/${enc(id)}"))
    suspend fun create(name: String): Room = json.decodeFromString(call("POST", "/v1/rooms", buildJsonObject { put("name", name) }))
    suspend fun join(id: String, userId: String, name: String): VoiceSession =
        json.decodeFromString(call("POST", "/v1/rooms/${enc(id)}/join", buildJsonObject { put("userId", userId); put("name", name) }))

    private fun enc(s: String) = URLEncoder.encode(s.trim(), Charsets.UTF_8)

    private suspend fun call(method: String, path: String, body: JsonObject? = null): String {
        val req = try {
            HttpRequest.newBuilder(URI(base + path)).timeout(Duration.ofSeconds(5)).header("Content-Type", "application/json")
                .method(method, body?.let { HttpRequest.BodyPublishers.ofString(it.toString()) } ?: HttpRequest.BodyPublishers.noBody()).build()
        } catch (e: Exception) {
            throw IOException("URL do manager inválida: $base")
        }
        val res = try { http.sendAsync(req, HttpResponse.BodyHandlers.ofString()).await() } catch (e: IOException) {
            throw IOException("Manager indisponível em $base.")
        }
        if (res.statusCode() in 200..299) return res.body()
        val err = runCatching { json.decodeFromString<ApiError>(res.body()) }.getOrNull()
        throw IOException(
            if (err?.code == "not_found") "Sala não encontrada."
            else "O manager recusou o pedido (${res.statusCode()}): ${err?.message ?: res.body().take(120)}"
        )
    }
}

/**
 * Uma ligação ao SFU: WebSocket `media.url` + uma PeerConnection, com os frames `Media*` de contract/openapi.yaml.
 * Toda a negociação corre em série num só coroutine ([ops]); o cliente é o par polite: numa colisão de ofertas
 * faz rollback, responde à oferta do servidor e volta a oferecer.
 * [onRemote]: track remota chegou (true) ou saiu (false), na thread do webrtc. [onClosed]: a sessão caiu sem [close].
 */
class MediaSession(
    private val voice: VoiceSession,
    private val userId: String,
    factory: PeerConnectionFactory,
    private val onRemote: (track: MediaStreamTrack, added: Boolean) -> Unit,
    private val onClosed: (reason: String) -> Unit,
) : PeerConnectionObserver {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val ops = Channel<suspend () -> Unit>(Channel.UNLIMITED)
    private val joined = CompletableDeferred<Unit>()
    private val pc: RTCPeerConnection = factory.createPeerConnection(RTCConfiguration().apply {
        iceServers = voice.iceServers.map { s -> RTCIceServer().apply { urls = s.urls; username = s.username; password = s.credential } }
    }, this)
    private lateinit var ws: WebSocket
    private val senders = mutableMapOf<String, RTCRtpSender>()  // fonte -> sender (só em [ops])
    private var wantOffer = false                               // há mudanças locais por negociar (só em [ops])
    @Volatile private var closed = false
    @Volatile private var sfuError: String? = null

    /** Abre o socket, envia `join` e espera `joined`; IOException com a razão se falhar. */
    suspend fun connect() {
        try {
            ws = try {
                http.newWebSocketBuilder().connectTimeout(Duration.ofSeconds(5)).buildAsync(URI(voice.media.url), Listener()).await()
            } catch (e: Exception) {
                throw IOException("Não foi possível ligar ao SFU em ${voice.media.url}.")
            }
            send(mediaFrame("join", "token" to voice.media.token))
            withTimeoutOrNull(10_000) { joined.await() } ?: throw IOException("O SFU não respondeu ao join.")
        } catch (e: Exception) {
            close(); throw e
        }
        scope.launch {
            for (op in ops) try {
                op()
                if (wantOffer && pc.signalingState == RTCSignalingState.STABLE) { wantOffer = false; offer() }
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                if (DEBUG) e.printStackTrace()
                fail("Falha na negociação com o SFU: ${e.message}")
            }
        }
    }

    /** Publica [track] como [source] ("mic" | "camera" | "screen"), substituindo a anterior; null deixa de publicar. */
    fun publish(source: String, track: MediaStreamTrack?) {
        ops.trySend {
            senders.remove(source)?.let(pc::removeTrack)
            if (track != null) {
                val init = RTCRtpTransceiverInit().apply { direction = RTCRtpTransceiverDirection.SEND_ONLY; streamIds = listOf(userId) }
                val sender = pc.addTransceiver(track, init).sender
                senders[source] = sender
                // o trackId tem de ser o do SDP (a=msid), que é o id do sender: igual ao da track ("camera"),
                // ou um UUID se esse id já foi usado nesta PeerConnection (câmara desligada e religada)
                send(mediaFrame("publish", "trackId" to sender.id, "source" to source))
            }
            wantOffer = true
        }
    }

    fun close() {
        if (closed) return
        closed = true
        scope.cancel()
        if (::ws.isInitialized) ws.abort()
        pc.close()
    }

    private fun fail(reason: String) {
        if (closed) return
        joined.completeExceptionally(IOException(reason))
        close()
        onClosed(reason)
    }

    private fun send(text: String) {
        if (DEBUG) println(">> $userId ${text.take(160)}")
        // o WebSocket do JDK não aceita dois envios em simultâneo
        try { synchronized(this) { ws.sendText(text, true).join() } } catch (e: Exception) { fail("Ligação ao SFU perdida.") }
    }

    private fun receive(text: String) {
        if (DEBUG) println("<< $userId ${text.take(160)}")
        val (op, d) = try { parseMediaFrame(text) } catch (e: Exception) { return }
        fun str(k: String) = d[k]?.jsonPrimitive?.content ?: ""
        when (op) {
            "joined" -> joined.complete(Unit)
            "error" -> sfuError = str("message").ifEmpty { str("code") }  // chega mesmo antes de o SFU fechar
            "offer" -> ops.trySend { onOffer(str("sdp")) }
            "answer" -> ops.trySend {
                if (pc.signalingState == RTCSignalingState.HAVE_LOCAL_OFFER) setRemote(RTCSdpType.ANSWER, str("sdp"))
            }
            "candidate" -> ops.trySend {
                val c = str("candidate")
                if (c.isNotEmpty() && pc.remoteDescription != null)
                    runCatching { pc.addIceCandidate(RTCIceCandidate(str("sdpMid"), d["sdpMLineIndex"]?.jsonPrimitive?.int ?: 0, c)) }
            }
        }
    }

    private suspend fun onOffer(sdp: String) {
        if (pc.signalingState != RTCSignalingState.STABLE) {  // colisão: o servidor ignora a nossa, cedemos
            if (DEBUG) println("-- $userId rollback")
            setLocal(RTCSessionDescription(RTCSdpType.ROLLBACK, ""))
            wantOffer = true
        }
        setRemote(RTCSdpType.OFFER, sdp)
        val answer = create { pc.createAnswer(RTCAnswerOptions(), it) }
        setLocal(answer)
        send(mediaFrame("answer", "sdp" to answer.sdp))
    }

    private suspend fun offer() {
        val o = create { pc.createOffer(RTCOfferOptions(), it) }
        setLocal(o)
        send(mediaFrame("offer", "sdp" to o.sdp))
    }

    private suspend fun create(call: (CreateSessionDescriptionObserver) -> Unit) = suspendCancellableCoroutine { k ->
        call(object : CreateSessionDescriptionObserver {
            override fun onSuccess(description: RTCSessionDescription) = k.resume(description)
            override fun onFailure(error: String) = k.resumeWithException(IOException(error))
        })
    }

    private suspend fun setLocal(d: RTCSessionDescription) = applied { pc.setLocalDescription(d, it) }
    private suspend fun setRemote(type: RTCSdpType, sdp: String) = applied { pc.setRemoteDescription(RTCSessionDescription(type, sdp), it) }
    private suspend fun applied(call: (SetSessionDescriptionObserver) -> Unit) = suspendCancellableCoroutine { k ->
        call(object : SetSessionDescriptionObserver {
            override fun onSuccess() = k.resume(Unit)
            override fun onFailure(error: String) = k.resumeWithException(IOException(error))
        })
    }

    // --- PeerConnectionObserver (thread de sinalização do webrtc) ---
    override fun onIceCandidate(c: RTCIceCandidate) {
        send(mediaFrame("candidate", buildJsonObject { put("candidate", c.sdp); put("sdpMid", c.sdpMid); put("sdpMLineIndex", c.sdpMLineIndex) }))
    }

    override fun onIceGatheringChange(state: RTCIceGatheringState) {
        if (state == RTCIceGatheringState.COMPLETE) send(mediaFrame("candidate", "candidate" to ""))  // fim dos candidatos
    }

    override fun onConnectionChange(state: RTCPeerConnectionState) {
        if (DEBUG) println("pc $userId $state")
        if (state == RTCPeerConnectionState.FAILED) fail("A ligação de mídia ao SFU falhou (sem caminho UDP até ao servidor?).")
    }

    override fun onTrack(transceiver: RTCRtpTransceiver) = onRemote(transceiver.receiver.track, true)
    override fun onRemoveTrack(receiver: RTCRtpReceiver) = onRemote(receiver.track, false)

    private inner class Listener : WebSocket.Listener {
        private val buf = StringBuilder()

        override fun onText(ws: WebSocket, data: CharSequence, last: Boolean): CompletionStage<*>? {
            buf.append(data)
            if (last) { val t = buf.toString(); buf.setLength(0); receive(t) }
            ws.request(1)
            return null
        }

        override fun onClose(ws: WebSocket, code: Int, reason: String): CompletionStage<*>? {
            val err = sfuError
            fail(when {
                err != null -> "O SFU recusou a ligação: $err"
                code == 4001 -> "O SFU recusou o token (inválido, expirado ou já usado)."
                else -> "O SFU fechou a ligação ($code${if (reason.isEmpty()) "" else ": $reason"})."
            })
            return null
        }

        override fun onError(ws: WebSocket, error: Throwable) = fail("Ligação ao SFU perdida (${error.message}).")
    }
}

// KURZ_MEDIA_DEBUG=1 imprime a sinalização e os estados da PeerConnection
private val DEBUG = System.getenv("KURZ_MEDIA_DEBUG") != null

package kurz.media

import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import com.shepeliev.webrtckmp.IceCandidate as RtcIceCandidate
import com.shepeliev.webrtckmp.IceServer as RtcIceServer
import com.shepeliev.webrtckmp.MediaStream
import com.shepeliev.webrtckmp.MediaStreamTrack
import com.shepeliev.webrtckmp.OfferAnswerOptions
import com.shepeliev.webrtckmp.PeerConnection
import com.shepeliev.webrtckmp.PeerConnectionState
import com.shepeliev.webrtckmp.RtcConfiguration
import com.shepeliev.webrtckmp.SessionDescription
import com.shepeliev.webrtckmp.SessionDescriptionType
import com.shepeliev.webrtckmp.SignalingState
import com.shepeliev.webrtckmp.VideoTrack
import com.shepeliev.webrtckmp.onConnectionStateChange
import com.shepeliev.webrtckmp.onIceCandidate
import com.shepeliev.webrtckmp.onRemoveTrack
import com.shepeliev.webrtckmp.onTrack
import io.ktor.client.HttpClient
import io.ktor.client.engine.darwin.Darwin
import io.ktor.client.plugins.timeout
import io.ktor.client.plugins.websocket.WebSockets
import io.ktor.client.plugins.websocket.webSocketSession
import io.ktor.client.request.get
import io.ktor.client.request.post
import io.ktor.client.request.setBody
import io.ktor.client.statement.bodyAsText
import io.ktor.http.ContentType
import io.ktor.http.contentType
import io.ktor.websocket.Frame
import io.ktor.websocket.close
import io.ktor.websocket.readText
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.collect
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withTimeoutOrNull
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import WebRTC.RTCDefaultVideoDecoderFactory
import WebRTC.RTCDefaultVideoEncoderFactory
import WebRTC.RTCVideoCodecInfo

private val http = HttpClient(Darwin) {
    // o URLSession tenta QUIC/HTTP3 primeiro e o WebSocket não passa em h3 atrás do Cloudflare ("Socket is not connected"): força TCP
    engine { configureRequest { setAssumesHTTP3Capable(false) } }
    install(WebSockets)
    install(io.ktor.client.plugins.HttpTimeout)
}
private val socketCloser = CoroutineScope(SupervisorJob() + Dispatchers.Default)

@Composable
actual fun rememberRoomClient(capture: MediaCapture): RoomClient {
    val client = remember { IosRooms(capture as? IosCapture) }
    DisposableEffect(client) { onDispose(client::leave) }
    return client
}

private class IosRooms(private val capture: IosCapture?) : RoomClient() {
    private val remote = mutableStateMapOf<String, VideoTrack>()
    private var listedBase = ""
    private var session: IosMediaSession? = null

    override suspend fun list(base: String) {
        if (base != listedBase) {
            listedBase = base
            rooms = emptyList()
            listError = null
        }
        try {
            val response = Manager(base).rooms()
            rooms = response
            listError = null
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            listError = e.message ?: "Não foi possível carregar as salas."
        }
    }

    override suspend fun ping(base: String): Boolean =
        try { Manager(base).healthy() } catch (e: CancellationException) { throw e } catch (_: Exception) { false }

    override suspend fun create(base: String, name: String): Room? =
        attempt { Manager(base).create(name) }?.also { created ->
            rooms = rooms.filterNot { it.id == created.id } + created
        }

    override suspend fun join(base: String, id: String, name: String): Boolean {
        leave()
        return try {
            connect(base, id, name)
            true
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            notice = describe(e)
            false
        }
    }

    override suspend fun reconnect(base: String, id: String, name: String): Boolean {
        dropSession()
        return try {
            connect(base, id, name)
            reconnecting = false
            true
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
            false
        }
    }

    private suspend fun connect(base: String, id: String, name: String) {
        val manager = Manager(base)
        val currentRoom = manager.room(id)
        val voice = manager.join(id, userId, name)
        lateinit var next: IosMediaSession
        next = IosMediaSession(voice, userId, ::onRemoteTrack) { reason ->
            if (session === next) {
                notice = reason
                session = null
                capture?.onTrack = null
                remote.clear()
                reconnecting = true
            }
        }
        next.connect { capture?.localTracks.orEmpty() }
        next.onMicLevel = { level -> capture?.pushLevel(level) }
        session = next
        room = currentRoom
        capture?.onTrack = next::publish
        reconnecting = false
    }

    private fun onRemoteTrack(track: MediaStreamTrack, added: Boolean, userId: String, source: String) {
        val key = "$userId:$source"
        if (added && track is VideoTrack) remote[key] = track else remote.remove(key)
    }

    override suspend fun poll(base: String) {
        val id = room?.id ?: return
        val updated = try { Manager(base).room(id) } catch (e: CancellationException) { throw e } catch (_: Exception) { return }
        if (room?.id == id) room = updated
    }

    override fun leave() {
        capture?.onTrack = null
        dropSession()
        room = null
        reconnecting = false
    }

    private fun dropSession() {
        val current = session
        session = null
        current?.close()
        remote.clear()
    }

    private suspend fun <T> attempt(block: suspend () -> T): T? =
        try { block() } catch (e: CancellationException) { throw e } catch (e: Exception) {
            notice = describe(e)
            null
        }

    @Composable
    override fun RemoteVideo(userId: String, source: String, modifier: Modifier) {
        remote["$userId:$source"]?.let { IosVideoView(it, modifier) }
    }
}

private class IosMediaSession(
    private val voice: VoiceSession,
    private val userId: String,
    private val onRemote: (MediaStreamTrack, Boolean, String, String) -> Unit,
    private val onClosed: (String) -> Unit,
) {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private val ops = Channel<suspend () -> Unit>(Channel.UNLIMITED)
    private val joined = CompletableDeferred<Unit>()
    private val sendMutex = Mutex()
    private val peer = PeerConnection(RtcConfiguration(iceServers = voice.iceServers.map {
        RtcIceServer(it.urls, it.username.orEmpty(), it.credential.orEmpty())
    }))
    private val senders = mutableMapOf<String, com.shepeliev.webrtckmp.RtpSender>()
    private val pendingCandidates = mutableListOf<RtcIceCandidate>()
    private var socket: io.ktor.client.plugins.websocket.DefaultClientWebSocketSession? = null
    private var wantOffer = false
    private var closed = false
    private var sfuError: String? = null
    var onMicLevel: ((Double) -> Unit)? = null

    suspend fun connect(localTracks: () -> Map<String, MediaStreamTrack>) {
        try {
            socket = http.webSocketSession(urlString = voice.media.url)
            val activeSocket = checkNotNull(socket)
            scope.launch {
                try {
                    for (frame in activeSocket.incoming) {
                        if (frame is Frame.Text) receive(frame.readText())
                    }
                    if (!closed) fail("O SFU fechou a ligação.")
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Exception) {
                    if (!closed) fail("Ligação ao SFU perdida (${e.message}).")
                }
            }
            scope.launch {
                for (op in ops) {
                    try {
                        op()
                        if (wantOffer && peer.signalingState == SignalingState.Stable) {
                            wantOffer = false
                            offer()
                        }
                    } catch (e: CancellationException) {
                        throw e
                    } catch (e: Exception) {
                        fail("Falha na negociação com o SFU: ${e.message}")
                    }
                }
            }
            scope.launch {
                peer.onIceCandidate.collect { candidate ->
                    send(mediaFrame("candidate", buildJsonObject {
                        put("candidate", candidate.candidate)
                        put("sdpMid", candidate.sdpMid)
                        put("sdpMLineIndex", candidate.sdpMLineIndex)
                    }))
                }
            }
            scope.launch {
                peer.onTrack.collect { event ->
                    val track = event.track ?: return@collect
                    val trackId = track.id
                    val source = trackId.substringAfter(':', "")
                    val remoteUser = event.streams.firstOrNull()?.id ?: trackId.substringBefore(':')
                    if (source.isNotEmpty()) onRemote(track, true, remoteUser, source)
                }
            }
            scope.launch {
                peer.onRemoveTrack.collect { receiver ->
                    val track = receiver.track ?: return@collect
                    val source = track.id.substringAfter(':', "")
                    val remoteUser = track.id.substringBefore(':')
                    if (source.isNotEmpty()) onRemote(track, false, remoteUser, source)
                }
            }
            scope.launch {
                peer.onConnectionStateChange.collect { state ->
                    if (state == PeerConnectionState.Failed) fail("A ligação de mídia ao SFU falhou.")
                }
            }
            send(mediaFrame("join", "token" to voice.media.token))
            withTimeoutOrNull(10_000) { joined.await() } ?: throw IllegalStateException(
                sfuError?.let { "O SFU recusou a ligação: $it" } ?: "O SFU não respondeu ao join.",
            )
            // lido só depois do join: o que foi ligado durante a espera também entra
            localTracks().forEach { (source, track) -> publish(source, track) }
            scope.launch {
                while (true) {
                    delay(100)
                    if ("mic" !in senders) continue
                    val level = runCatching {
                        peer.getStats()?.stats?.values?.firstOrNull { it.type == "media-source" && it.members["kind"] == "audio" }
                            ?.members?.get("audioLevel") as? Number
                    }.getOrNull()
                    onMicLevel?.invoke(level?.toDouble() ?: 0.0)
                }
            }
        } catch (e: CancellationException) {
            close()
            throw e
        } catch (e: Exception) {
            close()
            throw e
        }
    }

    fun publish(source: String, track: MediaStreamTrack?) {
        if (closed || source !in setOf("mic", "camera")) return
        // a remoção é síncrona: quem chama pára/liberta a faixa logo a seguir
        if (track == null) senders.remove(source)?.let(peer::removeTrack)
        ops.trySend {
            if (track != null) {
                senders.remove(source)?.let(peer::removeTrack)
                val sender = peer.addTrack(track, MediaStream())
                senders[source] = sender
                send(mediaFrame("publish", "trackId" to track.id, "source" to source))
            }
            wantOffer = true
        }
    }

    fun close() {
        if (closed) return
        closed = true
        scope.cancel()
        socket?.let { session -> socketCloser.launch { session.close() } }
        peer.close()
    }

    private fun fail(reason: String) {
        if (closed) return
        sfuError = reason
        joined.completeExceptionally(IllegalStateException(reason))
        close()
        onClosed(reason)
    }

    private suspend fun send(text: String) {
        if (closed) return
        sendMutex.withLock { socket?.send(Frame.Text(text)) }
    }

    private fun receive(text: String) {
        val (op, data) = try { parseMediaFrame(text) } catch (_: Exception) { return }
        fun string(key: String) = data[key]?.jsonPrimitive?.content.orEmpty()
        when (op) {
            "joined" -> joined.complete(Unit)
            "error" -> {
                sfuError = string("message").ifEmpty { string("code") }
                if (joined.isCompleted) fail("O SFU devolveu um erro: $sfuError")
                else joined.completeExceptionally(IllegalStateException(sfuError))
            }
            "offer" -> ops.trySend { onOffer(string("sdp")) }
            "answer" -> ops.trySend {
                if (peer.signalingState == SignalingState.HaveLocalOffer) {
                    peer.setRemoteDescription(SessionDescription(SessionDescriptionType.Answer, string("sdp")))
                    flushCandidates()
                }
            }
            "candidate" -> {
                val candidate = string("candidate")
                if (candidate.isNotEmpty()) ops.trySend {
                    val ice = RtcIceCandidate(
                        string("sdpMid"),
                        data["sdpMLineIndex"]?.jsonPrimitive?.int ?: 0,
                        candidate,
                    )
                    if (peer.remoteDescription == null) pendingCandidates += ice else peer.addIceCandidate(ice)
                }
            }
        }
    }

    @OptIn(kotlinx.cinterop.ExperimentalForeignApi::class)
    private suspend fun onOffer(sdp: String) {
        if (peer.signalingState != SignalingState.Stable) {
            peer.setLocalDescription(SessionDescription(SessionDescriptionType.Rollback, ""))
            wantOffer = true
        }
        peer.setRemoteDescription(SessionDescription(SessionDescriptionType.Offer, sdp))
        flushCandidates()
        val answer = peer.createAnswer(OfferAnswerOptions())
        peer.setLocalDescription(answer)
        println("KURZ-CODECS dec=${RTCDefaultVideoDecoderFactory().supportedCodecs().map { (it as RTCVideoCodecInfo).name }} enc=${RTCDefaultVideoEncoderFactory().supportedCodecs().map { (it as RTCVideoCodecInfo).name }}")  // TEMPORÁRIO
        println("KURZ-SDP offer:\n$sdp\nKURZ-SDP answer:\n${answer.sdp}")  // TEMPORÁRIO: diagnóstico do codec; remover
        send(mediaFrame("answer", "sdp" to answer.sdp))
    }

    private suspend fun flushCandidates() {
        pendingCandidates.toList().also { pendingCandidates.clear() }.forEach { peer.addIceCandidate(it) }
    }

    private suspend fun offer() {
        val offer = peer.createOffer(OfferAnswerOptions())
        peer.setLocalDescription(offer)
        send(mediaFrame("offer", "sdp" to offer.sdp))
    }
}

private class Manager(private val base: String) {
    private suspend fun call(method: String, path: String, body: String? = null): String {
        val response = when (method) {
            "GET" -> http.get(base.trimEnd('/') + path) { timeout { requestTimeoutMillis = 10_000 } }
            else -> http.post(base.trimEnd('/') + path) {
                timeout { requestTimeoutMillis = 10_000 }
                contentType(ContentType.Application.Json)
                setBody(body.orEmpty())
            }
        }
        val text = response.bodyAsText()
        if (response.status.value in 200..299) return text
        val apiError = runCatching { json.decodeFromString<ApiError>(text) }.getOrNull()
        throw IllegalStateException(
            if (apiError?.code == "not_found") "Sala não encontrada."
            else "O manager recusou o pedido (${response.status.value}): ${apiError?.message ?: text.take(120)}",
        )
    }

    suspend fun healthy(): Boolean { call("GET", "/healthz"); return true }
    suspend fun rooms() = json.decodeFromString<RoomList>(call("GET", "/v1/rooms")).rooms
    suspend fun room(id: String) = json.decodeFromString<Room>(call("GET", "/v1/rooms/$id"))
    suspend fun create(name: String) =
        json.decodeFromString<Room>(call("POST", "/v1/rooms", buildJsonObject { put("name", name) }.toString()))
    suspend fun join(id: String, userId: String, name: String) =
        json.decodeFromString<VoiceSession>(
            call("POST", "/v1/rooms/$id/join", buildJsonObject {
                put("userId", userId)
                put("name", name)
            }.toString()),
        )
}

private fun describe(e: Exception) = e.message?.takeIf { it.isNotBlank() } ?: "Não foi possível ligar ao servidor."

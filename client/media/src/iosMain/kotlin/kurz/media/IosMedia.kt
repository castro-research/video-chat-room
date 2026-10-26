package kurz.media

import WebRTC.RTCVideoRendererProtocol
import com.shepeliev.webrtckmp.VideoTrack
import kotlinx.cinterop.ExperimentalForeignApi
import platform.UIKit.UIView

/**
 * Captura e salas do iOS sem Compose, para a UI nativa (SwiftUI): o Swift mostra o vídeo com [bindVideo] em vez de UIKitView.
 * Uma instância por app.
 */
@OptIn(ExperimentalForeignApi::class)
class IosMedia {
    private val cap = IosCapture()
    private val client = IosRooms(cap)
    private val bound = mutableMapOf<UIView, VideoTrack>()

    val capture: MediaCapture get() = cap
    val rooms: RoomClient get() = client

    /** Faixas de vídeo disponíveis, por `<userId>:<source>` (a própria câmara incluída). Lê estado observável: quem a chama dentro de um snapshotFlow é avisado de mudanças. */
    fun videoKeys(): List<String> = client.remoteKeys() + listOfNotNull(cap.cameraVideo?.let { "${client.userId}:camera" })

    /** Liga a faixa `<userId>:<source>` a [view] (um RTCMTLVideoView). Idempotente: chamar de novo troca a faixa se mudou; sem faixa, desliga. */
    fun bindVideo(userId: String, source: String, view: UIView) {
        val track = if (userId == client.userId) cap.cameraVideo.takeIf { source == "camera" } else client.remoteTrack("$userId:$source")
        val old = bound[view]
        if (old === track) return
        old?.removeRenderer(view as RTCVideoRendererProtocol)
        if (track == null) bound.remove(view) else { track.addRenderer(view as RTCVideoRendererProtocol); bound[view] = track }
    }

    fun unbindVideo(view: UIView) {
        bound.remove(view)?.removeRenderer(view as RTCVideoRendererProtocol)
    }

    fun release() { capture.release(); rooms.leave() }
}

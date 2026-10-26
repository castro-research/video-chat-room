import SwiftUI
import WebRTC

/// Vídeo da faixa `<userId>:<source>`. A faixa pode chegar depois da vista: cada vez que o estado muda o Kotlin volta a ligá-la (idempotente).
struct VideoView: UIViewRepresentable {
    @EnvironmentObject var store: Store
    let userId: String
    let source: String
    var mirrored = false
    var fit = false

    final class Coordinator {
        let store: Store
        init(_ store: Store) { self.store = store }
    }

    func makeCoordinator() -> Coordinator { Coordinator(store) }

    func makeUIView(context: Context) -> RTCMTLVideoView {
        let v = RTCMTLVideoView(frame: .zero)
        v.videoContentMode = fit ? .scaleAspectFit : .scaleAspectFill
        if mirrored { v.transform = CGAffineTransform(scaleX: -1, y: 1) }
        store.kurz.bindVideo(userId: userId, source: source, view: v)
        return v
    }

    func updateUIView(_ v: RTCMTLVideoView, context: Context) {
        store.kurz.bindVideo(userId: userId, source: source, view: v)
    }

    static func dismantleUIView(_ v: RTCMTLVideoView, coordinator: Coordinator) {
        coordinator.store.kurz.unbindVideo(view: v)
    }
}

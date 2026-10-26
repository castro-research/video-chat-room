import SwiftUI
import WebRTC

#if os(macOS)
import AppKit

/// Mostra uma track de vídeo. [fit]: a imagem inteira com margens (ecrãs partilhados); senão preenche e corta (câmaras).
/// O RTCMTLVideoView no macOS só sabe ajustar, por isso para preencher ele fica maior que o recipiente, que corta.
struct VideoView: NSViewRepresentable {
    let track: RTCVideoTrack?
    var fit = false

    func makeNSView(context: Context) -> VideoContainer { VideoContainer(fit: fit) }

    func updateNSView(_ v: VideoContainer, context: Context) {
        v.fit = fit
        v.bind(track)
    }

    static func dismantleNSView(_ v: VideoContainer, coordinator: ()) { v.bind(nil) }
}

final class VideoContainer: NSView, RTCVideoViewDelegate {
    private let video = RTCMTLVideoView(frame: .zero)
    private var track: RTCVideoTrack?
    private var size = CGSize.zero
    var fit: Bool { didSet { needsLayout = true } }

    init(fit: Bool) {
        self.fit = fit
        super.init(frame: .zero)
        wantsLayer = true
        layer?.masksToBounds = true
        video.delegate = self
        addSubview(video)
    }

    required init?(coder: NSCoder) { fatalError() }

    /// Liga [t] (ou desliga, com nil); chamar de novo com a mesma track não faz nada.
    func bind(_ t: RTCVideoTrack?) {
        guard t !== track else { return }
        track?.remove(video)
        track = t
        t?.add(video)
    }

    override func layout() {
        super.layout()
        guard !fit, size.width > 0, size.height > 0, bounds.width > 0, bounds.height > 0 else { video.frame = bounds; return }
        let scale = max(bounds.width / size.width, bounds.height / size.height)
        let w = size.width * scale, h = size.height * scale
        video.frame = CGRect(x: (bounds.width - w) / 2, y: (bounds.height - h) / 2, width: w, height: h)
    }

    func videoView(_ videoView: RTCVideoRenderer, didChangeVideoSize size: CGSize) {
        DispatchQueue.main.async { [self] in self.size = size; needsLayout = true }
    }
}
#else
import UIKit

/// Mostra uma track de vídeo. [fit]: a imagem inteira com margens (ecrãs partilhados); senão preenche e corta (câmaras).
struct VideoView: UIViewRepresentable {
    let track: RTCVideoTrack?
    var fit = false
    var mirrored = false

    final class Coordinator { var track: RTCVideoTrack? }
    func makeCoordinator() -> Coordinator { Coordinator() }

    func makeUIView(context: Context) -> RTCMTLVideoView {
        let v = RTCMTLVideoView(frame: .zero)
        v.videoContentMode = fit ? .scaleAspectFit : .scaleAspectFill
        if mirrored { v.transform = CGAffineTransform(scaleX: -1, y: 1) }
        return v
    }

    func updateUIView(_ v: RTCMTLVideoView, context: Context) {
        v.videoContentMode = fit ? .scaleAspectFit : .scaleAspectFill
        let c = context.coordinator
        guard c.track !== track else { return }
        c.track?.remove(v)
        c.track = track
        track?.add(v)
    }

    static func dismantleUIView(_ v: RTCMTLVideoView, coordinator: Coordinator) { coordinator.track?.remove(v) }
}
#endif

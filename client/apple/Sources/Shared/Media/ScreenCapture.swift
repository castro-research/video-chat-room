#if os(macOS)
import Foundation
import WebRTC

/// A partilha de ecrã: a track "screen" que a sala publica. Cada [start] abre uma captura nova (outra fonte ou qualidade),
/// por isso quem publica troca a track: abre a nova antes de largar a velha.
final class ScreenCapture: NSObject, RTCDesktopCapturerDelegate {
    private(set) var track: RTCVideoTrack?
    private var source: RTCVideoSource?
    private var capturer: RTCDesktopCapturer?

    /// A captura parou por iniciativa do sistema (ex.: "Parar partilha" na barra de menus).
    var onStopped: (() -> Void)?

    /// Abre a captura de [desktop] com [quality]; a anterior (se houver) fica a correr até [stop] — ver [Opened].
    func open(_ desktop: RTCDesktopSource, quality: ScreenQuality) -> Opened {
        let src = MediaEngine.shared.factory.videoSource(forScreenCast: true)
        // o limite é uma caixa larga (32:9) para a altura mandar também nos ecrãs ultra-largos; Fonte = caixa de 8K (não reduz nada)
        let h = quality.resolution == .source ? 4320 : quality.resolution.rawValue
        src.adaptOutputFormat(toWidth: Int32(h * 32 / 9), height: Int32(h), fps: Int32(quality.fps))
        let cap = RTCDesktopCapturer(source: desktop, delegate: self, capture: src)
        cap.startCapture(withFPS: quality.fps)
        return Opened(source: src, capturer: cap, track: MediaEngine.shared.factory.videoTrack(with: src, trackId: "screen"))
    }

    /// Uma captura aberta e ainda não adoptada.
    struct Opened {
        let source: RTCVideoSource
        let capturer: RTCDesktopCapturer
        let track: RTCVideoTrack
    }

    /// Passa a ser esta a captura activa e pára a anterior.
    func adopt(_ o: Opened) {
        let old = capturer
        source = o.source; capturer = o.capturer; track = o.track
        old?.stopCapture()
    }

    func stop() {
        let c = capturer
        capturer = nil; source = nil; track = nil
        c?.stopCapture()
    }

    func didSourceCaptureStart(_ capturer: RTCDesktopCapturer) {}
    func didSourceCapturePaused(_ capturer: RTCDesktopCapturer) {}
    func didSourceCaptureStop(_ capturer: RTCDesktopCapturer) {
        // só conta se era a captura activa (as trocas em quente também páram a anterior)
        if capturer === self.capturer { DispatchQueue.main.async { [weak self] in self?.onStopped?() } }
    }
    func didSourceCaptureError(_ capturer: RTCDesktopCapturer) { didSourceCaptureStop(capturer) }
}
#endif

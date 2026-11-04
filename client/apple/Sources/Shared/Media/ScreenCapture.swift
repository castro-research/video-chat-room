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
        // Fonte = caixa de 8K (não reduz nada)
        let h = quality.resolution == .source ? 4320 : quality.resolution.rawValue
        let fitter = Fitter(source: src, maxHeight: h, fps: quality.fps)
        let cap = RTCDesktopCapturer(source: desktop, delegate: self, capture: fitter)
        cap.startCapture(withFPS: quality.fps)
        return Opened(source: src, capturer: cap, track: MediaEngine.shared.factory.videoTrack(with: src, trackId: "screen"), fitter: fitter)
    }

    /// Uma captura aberta e ainda não adoptada.
    struct Opened {
        let source: RTCVideoSource
        let capturer: RTCDesktopCapturer
        let track: RTCVideoTrack
        let fitter: Fitter
    }

    /// Passa a ser esta a captura activa e pára a anterior.
    func adopt(_ o: Opened) {
        let old = capturer
        source = o.source; capturer = o.capturer; track = o.track; fitter = o.fitter
        old?.stopCapture()
    }

    func stop() {
        let c = capturer
        capturer = nil; source = nil; track = nil; fitter = nil
        c?.stopCapture()
    }

    private var fitter: Fitter?

    /// Repassa os quadros ao source e, quando o tamanho capturado muda (outra janela, ecrã redimensionado), refaz o pedido de
    /// formato com a proporção do próprio quadro, limitado em altura (e a 32:9 de largura) e sem ampliar. O `adaptOutputFormat`
    /// do WebRTC CORTA o quadro para a proporção pedida: uma caixa fixa (ex.: 32:9) deixava só uma tira do ecrã.
    final class Fitter: NSObject, RTCVideoCapturerDelegate {
        private let source: RTCVideoSource
        private let maxHeight: Int
        private let fps: Int
        private var size = (w: 0, h: 0)  // só na thread de captura

        init(source: RTCVideoSource, maxHeight: Int, fps: Int) {
            self.source = source; self.maxHeight = maxHeight; self.fps = fps
        }

        func capturer(_ capturer: RTCVideoCapturer, didCapture frame: RTCVideoFrame) {
            let w = Int(frame.width), h = Int(frame.height)
            if w > 0, h > 0, (w, h) != size {
                size = (w, h)
                let s = min(1.0, Double(maxHeight) / Double(h), Double(maxHeight * 32 / 9) / Double(w))
                source.adaptOutputFormat(toWidth: Int32((Double(w) * s).rounded()), height: Int32((Double(h) * s).rounded()), fps: Int32(fps))
            }
            source.capturer(capturer, didCapture: frame)
        }
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

import AVFoundation
import WebRTC

/// O microfone: a track "mic" que a sala publica. O ADM do WebRTC só grava enquanto a track estiver enviada.
final class MicCapture {
    private(set) var track: RTCAudioTrack?

    /// false se o sistema negou o microfone.
    func start() async -> Bool {
        if track != nil { return true }
        guard await Permission.granted(.audio) else { return false }
        let engine = MediaEngine.shared
        track = engine.factory.audioTrack(with: engine.factory.audioSource(with: MediaEngine.noConstraints), trackId: "mic")
        return true
    }

    func stop() {
        track?.isEnabled = false
        track = nil
    }
}

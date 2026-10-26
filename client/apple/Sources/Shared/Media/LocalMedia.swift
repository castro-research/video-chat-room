import Foundation
import WebRTC

/// Mic, câmara e ecrã juntos, como a sala os vê: [onTrack] avisa cada track que liga (ou desliga, com nil), e é o que
/// publica no SFU. Nada aqui é UI: o estado dos botões vive no AppModel.
@MainActor
final class LocalMedia {
    let mic = MicCapture()
    let camera = CameraCapture()
#if os(macOS)
    let screen = ScreenCapture()
    let screenSources = ScreenSources()
#endif
    let devices = AudioDevices()

    /// Avisado com ("mic" | "camera" | "screen", track) quando uma fonte liga e (source, nil) quando desliga.
    var onTrack: ((String, RTCMediaStreamTrack?) -> Void)?

    /// As tracks activas por fonte (para a sala que acaba de ligar publicar o que já estava ligado).
    var tracks: [String: RTCMediaStreamTrack] {
        var t: [String: RTCMediaStreamTrack] = [:]
        if let m = mic.track { t["mic"] = m }
        if let c = camera.track { t["camera"] = c }
#if os(macOS)
        if let s = screen.track { t["screen"] = s }
#endif
        return t
    }

    // --- microfone ---

    func startMic() async -> Bool {
        guard await mic.start() else { return false }
        devices.restore()  // o ADM novo começa nos padrões; antes de a track seguir para a sala
        onTrack?("mic", mic.track)
        return true
    }

    func stopMic() {
        guard mic.track != nil else { return }
        onTrack?("mic", nil)
        mic.stop()
    }

    // --- câmara ---

    func startCamera() async -> Bool {
        guard await camera.start(deviceId: nil) else { return false }
        onTrack?("camera", camera.track)
        return true
    }

    func stopCamera() {
        guard camera.track != nil else { return }
        onTrack?("camera", nil)
        camera.stop()
    }

#if os(macOS)
    // --- ecrã ---

    /// Abre a captura de [id] (nil = o primeiro ecrã) com [quality]; se já havia partilha, troca em quente.
    /// false se a fonte não abriu (a janela pode ter fechado, ou o sistema negou a gravação do ecrã).
    func startScreen(source id: String?, quality: ScreenQuality) async -> Bool {
        if screenSources.sources.isEmpty { screenSources.refresh() }
        guard let desktop = (id ?? screenSources.firstScreen?.id).flatMap(screenSources.source) else { return false }
        let opened = screen.open(desktop, quality: quality)
        screen.adopt(opened)
        onTrack?("screen", opened.track)
        return true
    }

    func stopScreen() {
        guard screen.track != nil else { return }
        onTrack?("screen", nil)
        screen.stop()
    }

#else
    func startScreen(source id: String?, quality: ScreenQuality) async -> Bool { false }
    func stopScreen() {}
#endif

    func release() {
        onTrack = nil
        stopMic(); stopCamera(); stopScreen()
    }
}

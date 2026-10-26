import AVFoundation
import WebRTC

/// Uma câmara. [id] é o uniqueID do sistema.
struct Camera: Identifiable, Hashable {
    let id: String
    let name: String
}

/// A câmara: a track "camera" que a sala publica e a pré-visualização usa. Trocar de câmara troca só o dispositivo
/// do capturador: a track (e o que a sala já negociou) fica a mesma.
final class CameraCapture {
    private(set) var track: RTCVideoTrack?
    private var source: RTCVideoSource?
    private var capturer: RTCCameraVideoCapturer?
    private let defaults = UserDefaults.standard

    static var cameras: [Camera] { RTCCameraVideoCapturer.captureDevices().map { Camera(id: $0.uniqueID, name: $0.localizedName) } }

    /// A câmara lembrada, se ainda existir.
    var rememberedId: String? { defaults.string(forKey: "camera").flatMap { id in Self.cameras.contains { $0.id == id } ? id : nil } }

    /// false se o sistema negou a câmara ou não há nenhuma que abra.
    func start(deviceId: String?) async -> Bool {
        if track != nil { return true }
        guard await Permission.granted(.video) else { return false }
        let engine = MediaEngine.shared
        let src = engine.factory.videoSource()
        let cap = RTCCameraVideoCapturer(delegate: src)
        guard await open(cap, deviceId: deviceId ?? rememberedId) else { return false }
        source = src
        capturer = cap
        track = engine.factory.videoTrack(with: src, trackId: "camera")
        return true
    }

    /// Troca de câmara com a captura ligada; false (e fica a anterior) se a nova não abrir.
    func switchTo(deviceId: String) async -> Bool {
        guard let cap = capturer else { return false }
        return await open(cap, deviceId: deviceId)
    }

    func stop() {
        capturer?.stopCapture()
        capturer = nil; source = nil; track = nil
    }

    private func open(_ cap: RTCCameraVideoCapturer, deviceId: String?) async -> Bool {
        let devices = RTCCameraVideoCapturer.captureDevices()
        guard let device = devices.first(where: { $0.uniqueID == deviceId }) ?? devices.first, let format = Self.format(for: device) else { return false }
        do {
            try await cap.startCapture(with: device, format: format, fps: 30)
            defaults.set(device.uniqueID, forKey: "camera")
            return true
        } catch {
            return false
        }
    }

    /// O formato mais próximo de 1280x720 que dá pelo menos 30 fps (senão, o mais próximo).
    private static func format(for device: AVCaptureDevice) -> AVCaptureDevice.Format? {
        let formats = RTCCameraVideoCapturer.supportedFormats(for: device)
        func distance(_ f: AVCaptureDevice.Format) -> Int32 {
            let d = CMVideoFormatDescriptionGetDimensions(f.formatDescription)
            return abs(d.width - 1280) + abs(d.height - 720)
        }
        let fast = formats.filter { $0.videoSupportedFrameRateRanges.contains { $0.maxFrameRate >= 30 } }
        return (fast.isEmpty ? formats : fast).min { distance($0) < distance($1) }
    }
}

import AVFoundation
import WebRTC

/// Uma fábrica do WebRTC por processo: as tracks locais e a PeerConnection da sala têm de vir da mesma.
/// Os codecs são os por defeito (H264, VP8, VP9, AV1); o SFU só negoceia VP8 hoje.
final class MediaEngine {
    static let shared = MediaEngine()

    let factory = RTCPeerConnectionFactory(encoderFactory: RTCDefaultVideoEncoderFactory(), decoderFactory: RTCDefaultVideoDecoderFactory())
#if os(macOS)
    var adm: RTCAudioDeviceModule { factory.audioDeviceModule }
#endif

    private init() {
#if os(iOS)
        // chamada de voz/vídeo: grava e toca, altifalante por defeito, segue AirPods/auscultadores
        let session = RTCAudioSession.sharedInstance()
        session.lockForConfiguration()
        try? session.setCategory(.playAndRecord, mode: .videoChat, options: [.defaultToSpeaker, .allowBluetooth, .allowBluetoothA2DP])
        try? session.setActive(true)
        session.unlockForConfiguration()
#endif
    }

    static let noConstraints = RTCMediaConstraints(mandatoryConstraints: nil, optionalConstraints: nil)
}

/// Pede (ou lê) a permissão do sistema para microfone ou câmara.
enum Permission {
    static func granted(_ type: AVMediaType) async -> Bool {
        switch AVCaptureDevice.authorizationStatus(for: type) {
        case .authorized: return true
        case .notDetermined: return await AVCaptureDevice.requestAccess(for: type)
        default: return false
        }
    }
}

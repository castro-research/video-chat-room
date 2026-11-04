import Foundation
import KurzKit
import WebRTC

/// A sala de voz ligada: a MediaSession com o SFU (entrada por `joinVoice`), as tracks remotas e o volume de cada pessoa.
/// Quem está na sala vem do gateway (`voice.state`), não daqui. Se a ligação ao SFU cai fica na sala e tenta voltar a cada 2 s
/// (`joinVoice` de novo: o token de mídia é de uso único); só desiste passados 30 s.
@MainActor
final class RoomSession: ObservableObject {
    /// A ligação ao SFU caiu e está a tentar voltar: a sala mantém-se até conseguir ou desistir.
    @Published private(set) var reconnecting = false
    @Published var notice: String?
    /// Nível do microfone (0..1), medido pelo WebRTC.
    @Published private(set) var micLevel: Double = 0
    /// `<userId>:<source>` das faixas de vídeo que já chegaram.
    @Published private(set) var videoKeys: Set<String> = []
    /// Volume com que se ouve cada pessoa, 0...2 (1 = normal); só vale para quem se mexeu.
    @Published private(set) var volumes: [String: Float] = [:]

    private(set) var userId = ""
    /// Avisado a cada ligação ao SFU conseguida (a primeira e as reconexões): é a hora de publicar mute/câmara/partilha no gateway.
    var onJoined: (() -> Void)?
    private let local: LocalMedia
    private var session: MediaSession?
    private var videoTracks: [String: RTCVideoTrack] = [:]
    private var audioTracks: [String: RTCAudioTrack] = [:]
    private var levelLoop: Task<Void, Never>?

    init(local: LocalMedia) {
        self.local = local
    }

    func videoTrack(_ key: String) -> RTCVideoTrack? { videoTracks[key] }
    func volume(_ userId: String) -> Float { volumes[userId] ?? 1 }

    /// Muda o volume de [userId] só para este cliente (voz e áudio da partilha).
    func setVolume(_ userId: String, _ v: Float) {
        let vol = min(max(v, 0), 2)
        volumes[userId] = vol
        for (key, t) in audioTracks where key.hasPrefix(userId + ":") { t.source.volume = Double(vol) }
    }

    /// Entra no canal de voz e mantém-no até o Task ser cancelado ou a ligação se perder de vez. Não lança: as falhas ficam em [notice].
    func run(api: KurzAPI, channelId: String, userId: String) async {
        self.userId = userId
        defer { leave() }
        guard await open(api: api, channelId: channelId, report: true) else { return }
        var lostSince: Date?
        while !Task.isCancelled {
            try? await Task.sleep(nanoseconds: 2_000_000_000)
            if Task.isCancelled { return }
            if !reconnecting { lostSince = nil; continue }
            let since = lostSince ?? Date()
            lostSince = since
            if await !reconnect(api: api, channelId: channelId) && Date().timeIntervalSince(since) > 30 {
                notice = "Sem ligação ao servidor há mais de 30 s. Saíste da sala."
                return
            }
        }
    }

    func leave() {
        local.onTrack = nil
        dropSession()
        reconnecting = false
    }

    // --- ligação ---

    private func reconnect(api: KurzAPI, channelId: String) async -> Bool {
        dropSession()
        let ok = await open(api: api, channelId: channelId, report: false)
        if ok { reconnecting = false }
        return ok
    }

    private final class Box { weak var session: MediaSession? }

    private func open(api: KurzAPI, channelId: String, report: Bool) async -> Bool {
        do {
            let voice = try await api.joinVoice(channelId: channelId)
            let box = Box()
            let s = MediaSession(voice: voice, userId: userId, factory: MediaEngine.shared.factory,
                onRemote: { [weak self] track, added, uid, source in
                    Task { @MainActor in self?.remote(track, added: added, userId: uid, source: source) }
                },
                onClosed: { [weak self, box] reason in
                    // caiu: fica na sala, a UI vai tentando voltar
                    Task { @MainActor in
                        guard let self, let s = box.session, self.session === s else { return }
                        self.dropSession()
                        self.notice = reason
                        self.reconnecting = true
                    }
                })
            box.session = s
            try await s.connect()
            session = s
            // a captura já ligada entra agora; o resto entra/sai quando os botões mudarem
            for (source, track) in local.tracks { s.publish(source, track) }
            local.onTrack = { [weak s] source, track in s?.publish(source, track) }
            let local = self.local
            levelLoop = Task { [weak self, weak s] in
                var tick = 0
                while !Task.isCancelled {
                    tick += 1
                    if tick % 14 == 1, let s { print("KURZ-AUDIO", await s.debugLine(), "micTrack=\(local.mic.track != nil)") }
                    try? await Task.sleep(nanoseconds: 150_000_000)
                    guard let s else { return }
                    let level = await s.audioLevel()
                    self?.micLevel = local.mic.track != nil ? level : 0
                }
            }
            reconnecting = false
            onJoined?()
            return true
        } catch {
            if report && !(error is CancellationError) { notice = Self.message(error) }
            return false
        }
    }

    /// O aviso para o utilizador: sem Connect/Speak o servidor responde 403 `missing_permission`; sem voz, 501 `not_supported`.
    private static func message(_ error: Error) -> String {
        if let e = error as? KurzError {
            if e.code == "missing_permission" || e.code == "forbidden" { return Msg.voiceDenied }
            if e.code == "not_supported" { return Msg.noVoice }
        }
        return error.localizedDescription
    }

    /// Larga a ligação ao SFU (e as tracks remotas): é o que acontece enquanto a UI tenta voltar.
    private func dropSession() {
        local.onTrack = nil
        levelLoop?.cancel(); levelLoop = nil
        micLevel = 0
        let s = session
        session = nil
        // fora da thread da UI: fechar a PeerConnection nativa espera pelas threads do libwebrtc
        if let s { Task.detached { s.close() } }
        videoTracks = [:]; audioTracks = [:]; videoKeys = []
    }

    private func remote(_ track: RTCMediaStreamTrack, added: Bool, userId: String, source: String) {
        let key = "\(userId):\(source)"
        if let video = track as? RTCVideoTrack {
            if added { videoTracks[key] = video } else { videoTracks[key] = nil }
            videoKeys = Set(videoTracks.keys)
        } else if let audio = track as? RTCAudioTrack {
            if added { audioTracks[key] = audio; audio.source.volume = Double(volume(userId)) } else { audioTracks[key] = nil }
        }
    }
}

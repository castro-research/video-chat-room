import Foundation
import WebRTC

/// Resolve uma vez, com sucesso ou erro, e quem espera é acordado; sem timeout próprio (ver MediaSession.connect).
private final class Gate {
    private let lock = NSLock()
    private var result: Result<Void, Error>?
    private var waiter: CheckedContinuation<Void, Error>?

    func complete(_ r: Result<Void, Error>) {
        lock.lock()
        guard result == nil else { lock.unlock(); return }
        result = r
        let w = waiter
        waiter = nil
        lock.unlock()
        w?.resume(with: r)
    }

    var isDone: Bool { lock.lock(); defer { lock.unlock() }; return result != nil }

    func wait() async throws {
        try await withCheckedThrowingContinuation { (c: CheckedContinuation<Void, Error>) in
            lock.lock()
            if let r = result { lock.unlock(); c.resume(with: r) } else { waiter = c; lock.unlock() }
        }
    }
}

/// Uma ligação ao SFU: WebSocket `media.url` + uma PeerConnection, com os frames `Media*` de contract/openapi.yaml.
/// Toda a negociação corre em série num só Task ([ops]); o cliente é o par polite: numa colisão de ofertas faz rollback,
/// responde à oferta do servidor e volta a oferecer.
final class MediaSession: NSObject, RTCPeerConnectionDelegate {
    /// Track remota chegou (added) ou saiu; [userId] e [source] vêm do id `<userId>:<source>`.
    typealias RemoteHandler = (_ track: RTCMediaStreamTrack, _ added: Bool, _ userId: String, _ source: String) -> Void

    private let voice: VoiceSession
    private let userId: String
    private let onRemote: RemoteHandler
    private let onClosed: (String) -> Void
    private let pc: RTCPeerConnection
    private var ws: URLSessionWebSocketTask?

    private let ops: AsyncStream<() async throws -> Void>
    private let opsIn: AsyncStream<() async throws -> Void>.Continuation
    private let sends: AsyncStream<String>
    private let sendsIn: AsyncStream<String>.Continuation
    private let joined = Gate()

    private var senders: [String: RTCRtpSender] = [:]   // fonte -> sender (só em ops)
    private var wantOffer = false                       // há mudanças locais por negociar (só em ops)
    private var pending: [RTCIceCandidate] = []         // candidatos que chegaram antes da descrição remota (só em ops)

    private let lock = NSLock()
    private var closed = false
    private var sfuError: String?

    init(voice: VoiceSession, userId: String, factory: RTCPeerConnectionFactory, onRemote: @escaping RemoteHandler, onClosed: @escaping (String) -> Void) {
        self.voice = voice
        self.userId = userId
        self.onRemote = onRemote
        self.onClosed = onClosed
        (ops, opsIn) = AsyncStream<() async throws -> Void>.makeStream()
        (sends, sendsIn) = AsyncStream<String>.makeStream()
        let config = RTCConfiguration()
        config.sdpSemantics = .unifiedPlan
        config.iceServers = voice.iceServers.map { RTCIceServer(urlStrings: $0.urls, username: $0.username, credential: $0.credential) }
        pc = factory.peerConnection(with: config, constraints: RTCMediaConstraints(mandatoryConstraints: nil, optionalConstraints: nil), delegate: nil)!
        super.init()
        pc.delegate = self
    }

    private var isClosed: Bool { lock.lock(); defer { lock.unlock() }; return closed }

    /// Abre o socket, envia `join` e espera `joined`; KurzError com a razão se falhar.
    func connect() async throws {
        guard let url = URL(string: voice.media.url) else { throw KurzError("Não foi possível ligar ao SFU em \(voice.media.url).") }
        let task = URLSession.shared.webSocketTask(with: url)
        ws = task
        task.resume()
        receiveLoop(task)
        sendLoop(task)
        send(frame("join", ["token": voice.media.token]))
        let timeout = Task { [joined] in
            try? await Task.sleep(nanoseconds: 10_000_000_000)
            joined.complete(.failure(KurzError("O SFU não respondeu ao join.")))
        }
        defer { timeout.cancel() }
        do { try await joined.wait() } catch { close(); throw error }
        runOps()
    }

    /// Publica [track] como [source] ("mic" | "camera" | "screen"), substituindo a anterior; nil deixa de publicar.
    func publish(_ source: String, _ track: RTCMediaStreamTrack?) {
        opsIn.yield { [self] in
            if let old = senders.removeValue(forKey: source) { pc.removeTrack(old) }
            if let track {
                let `init` = RTCRtpTransceiverInit()
                `init`.direction = .sendOnly
                `init`.streamIds = [userId]
                guard let sender = pc.addTransceiver(with: track, init: `init`)?.sender else { return }
                senders[source] = sender
                // o trackId tem de ser o do SDP (a=msid), que é o id do sender
                send(frame("publish", ["trackId": sender.senderId, "source": source]))
            }
            wantOffer = true
        }
    }

    /// Nível do microfone (0..1) medido pelo WebRTC; 0 se não houver.
    func audioLevel() async -> Double {
        await withCheckedContinuation { (c: CheckedContinuation<Double, Never>) in
            pc.statistics { report in
                for (_, s) in report.statistics where s.type == "media-source" && (s.values["kind"] as? String) == "audio" {
                    c.resume(returning: (s.values["audioLevel"] as? NSNumber)?.doubleValue ?? 0)
                    return
                }
                c.resume(returning: 0)
            }
        }
    }

    func close() {
        lock.lock()
        if closed { lock.unlock(); return }
        closed = true
        lock.unlock()
        opsIn.finish()
        sendsIn.finish()
        ws?.cancel(with: .goingAway, reason: nil)
        pc.close()
    }

    private func fail(_ reason: String) {
        if isClosed { return }
        joined.complete(.failure(KurzError(reason)))
        close()
        onClosed(reason)
    }

    // --- sinalização ---

    private func frame(_ op: String, _ d: [String: Any]) -> String {
        let data = (try? JSONSerialization.data(withJSONObject: ["op": op, "d": d])) ?? Data()
        return String(decoding: data, as: UTF8.self)
    }

    /// Os envios saem em série, por um só Task (o URLSessionWebSocketTask não garante ordem com envios simultâneos).
    private func send(_ text: String) { sendsIn.yield(text) }

    private func sendLoop(_ task: URLSessionWebSocketTask) {
        Task { [weak self, sends] in
            for await text in sends {
                do { try await task.send(.string(text)) } catch { self?.fail("Ligação ao SFU perdida."); return }
            }
        }
    }

    private func receiveLoop(_ task: URLSessionWebSocketTask) {
        Task { [weak self] in
            while true {
                do {
                    let m = try await task.receive()
                    if case .string(let s) = m { self?.receive(s) }
                } catch {
                    self?.socketClosed(task)
                    return
                }
            }
        }
    }

    private func socketClosed(_ task: URLSessionWebSocketTask) {
        lock.lock(); let err = sfuError; lock.unlock()
        let code = task.closeCode.rawValue
        let reason = task.closeReason.map { String(decoding: $0, as: UTF8.self) } ?? ""
        fail(
            err != nil ? "O SFU recusou a ligação: \(err!)"
            : !joined.isDone ? "Não foi possível ligar ao SFU em \(voice.media.url)."
            : code == 4001 ? "O SFU recusou o token (inválido, expirado ou já usado)."
            : "O SFU fechou a ligação (\(code)\(reason.isEmpty ? "" : ": \(reason)"))."
        )
    }

    private func receive(_ text: String) {
        guard let obj = (try? JSONSerialization.jsonObject(with: Data(text.utf8))) as? [String: Any], let op = obj["op"] as? String else { return }
        let d = obj["d"] as? [String: Any] ?? [:]
        func str(_ k: String) -> String { d[k] as? String ?? "" }
        switch op {
        case "joined": joined.complete(.success(()))
        case "error":  // chega mesmo antes de o SFU fechar
            let m = str("message")
            lock.lock(); sfuError = m.isEmpty ? str("code") : m; lock.unlock()
        case "offer":
            let sdp = str("sdp")
            opsIn.yield { [self] in try await onOffer(sdp) }
        case "answer":
            let sdp = str("sdp")
            opsIn.yield { [self] in
                if pc.signalingState == .haveLocalOffer {
                    try await pc.setRemoteDescription(RTCSessionDescription(type: .answer, sdp: sdp))
                    await flushCandidates()
                }
            }
        case "candidate":
            let c = str("candidate")
            guard !c.isEmpty else { return }
            let ice = RTCIceCandidate(sdp: c, sdpMLineIndex: Int32((d["sdpMLineIndex"] as? NSNumber)?.intValue ?? 0), sdpMid: str("sdpMid"))
            opsIn.yield { [self] in
                if pc.remoteDescription == nil { pending.append(ice) } else { try? await pc.add(ice) }
            }
        default: break
        }
    }

    private func runOps() {
        Task { [weak self, ops] in
            for await op in ops {
                guard let self else { return }
                do {
                    try await op()
                    if self.wantOffer && self.pc.signalingState == .stable { self.wantOffer = false; try await self.offer() }
                } catch {
                    self.fail("Falha na negociação com o SFU: \(error.localizedDescription)")
                }
            }
        }
    }

    private func onOffer(_ sdp: String) async throws {
        if pc.signalingState != .stable {  // colisão: o servidor ignora a nossa, cedemos
            try await pc.setLocalDescription(RTCSessionDescription(type: .rollback, sdp: ""))
            wantOffer = true
        }
        try await pc.setRemoteDescription(RTCSessionDescription(type: .offer, sdp: sdp))
        await flushCandidates()
        let answer = try await pc.answer(for: RTCMediaConstraints(mandatoryConstraints: nil, optionalConstraints: nil))
        try await pc.setLocalDescription(answer)
        send(frame("answer", ["sdp": answer.sdp]))
    }

    private func offer() async throws {
        let o = try await pc.offer(for: RTCMediaConstraints(mandatoryConstraints: nil, optionalConstraints: nil))
        try await pc.setLocalDescription(o)
        send(frame("offer", ["sdp": o.sdp]))
    }

    private func flushCandidates() async {
        let list = pending
        pending = []
        for c in list { try? await pc.add(c) }
    }

    // --- RTCPeerConnectionDelegate (threads do WebRTC) ---

    func peerConnection(_ pc: RTCPeerConnection, didGenerate c: RTCIceCandidate) {
        send(frame("candidate", ["candidate": c.sdp, "sdpMid": c.sdpMid ?? "", "sdpMLineIndex": Int(c.sdpMLineIndex)]))
    }

    func peerConnection(_ pc: RTCPeerConnection, didChange newState: RTCIceGatheringState) {
        if newState == .complete { send(frame("candidate", ["candidate": ""])) }  // fim dos candidatos
    }

    func peerConnection(_ pc: RTCPeerConnection, didChange newState: RTCPeerConnectionState) {
        if newState == .failed { fail("A ligação de mídia ao SFU falhou (sem caminho UDP até ao servidor?).") }
    }

    func peerConnection(_ pc: RTCPeerConnection, didStartReceivingOn transceiver: RTCRtpTransceiver) {
        remote(transceiver.receiver.track, added: true)
    }

    func peerConnection(_ pc: RTCPeerConnection, didRemove rtpReceiver: RTCRtpReceiver) {
        remote(rtpReceiver.track, added: false)
    }

    private func remote(_ track: RTCMediaStreamTrack?, added: Bool) {
        guard let track else { return }
        let parts = track.trackId.split(separator: ":", maxSplits: 1, omittingEmptySubsequences: false)
        guard parts.count == 2, !parts[1].isEmpty else { return }
        onRemote(track, added, String(parts[0]), String(parts[1]))
    }

    func peerConnection(_ pc: RTCPeerConnection, didChange stateChanged: RTCSignalingState) {}
    func peerConnection(_ pc: RTCPeerConnection, didAdd stream: RTCMediaStream) {}
    func peerConnection(_ pc: RTCPeerConnection, didRemove stream: RTCMediaStream) {}
    func peerConnectionShouldNegotiate(_ pc: RTCPeerConnection) {}
    func peerConnection(_ pc: RTCPeerConnection, didChange newState: RTCIceConnectionState) {}
    func peerConnection(_ pc: RTCPeerConnection, didRemove candidates: [RTCIceCandidate]) {}
    func peerConnection(_ pc: RTCPeerConnection, didOpen dataChannel: RTCDataChannel) {}
}

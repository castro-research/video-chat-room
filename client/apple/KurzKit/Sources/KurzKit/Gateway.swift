import Foundation

public enum ConnectionState: Equatable, Sendable {
    /// Parado (antes de `start()`, depois de `stop()` ou após um erro sem volta, como 4009 ou a sessão perdida).
    case disconnected
    /// Primeira ligação.
    case connecting
    /// `ready` ou `resumed` recebido.
    case connected
    /// Caiu e está tentando de novo (com backoff).
    case reconnecting
}

/// Um WebSocket de texto. Abstrai URLSessionWebSocketTask para testar o protocolo sem rede.
protocol WebSocketConnection: AnyObject, Sendable {
    func send(_ text: String) async throws
    func receive() async throws -> String
    func close()
    /// Código de fechamento enviado pelo servidor, quando houve.
    var closeCode: Int? { get }
}

final class URLSessionWebSocket: WebSocketConnection, @unchecked Sendable {
    private let task: URLSessionWebSocketTask
    init(session: URLSession, url: URL) {
        task = session.webSocketTask(with: url)
        task.resume()
    }
    func send(_ text: String) async throws { try await task.send(.string(text)) }
    func receive() async throws -> String {
        switch try await task.receive() {
        case .string(let s): return s
        case .data(let d): return String(decoding: d, as: UTF8.self)
        @unknown default: return ""
        }
    }
    func close() { task.cancel(with: .goingAway, reason: nil) }
    var closeCode: Int? { task.closeCode == .invalid ? nil : task.closeCode.rawValue }
}

/// Cliente do gateway (`contract/gateway.md`): hello, identify/resume, ready, heartbeat, seq por sessão, reconexão com backoff.
///
/// Uso: assine `events()`/`connectionStates()` ANTES de `start()` (quem assina depois perde o passado) e chame `stop()` ao terminar.
/// Eventos: `.ready` (inclusive depois de `.invalidSession`) = refaça o fetch por REST; `.resumed` = nada a refazer.
/// `stop()` é final: encerra os streams. Para reconectar depois, crie outro (`KurzAPI.makeGateway()`).
public actor Gateway {
    typealias TokenProvider = @Sendable (_ forceRefresh: Bool) async throws -> String

    private let url: URL?
    private let tokenProvider: TokenProvider
    private let connect: @Sendable (URL) -> WebSocketConnection
    private let backoff: Backoff
    private let eventsBus = Broadcaster<GatewayEvent>()
    private let statesBus = Broadcaster<ConnectionState>(initial: .disconnected)

    private var runTask: Task<Void, Never>?
    private var heartbeatTask: Task<Void, Never>?
    private var socket: WebSocketConnection?
    private var sessionId: String?
    private var lastSeq = 0
    private var missedAcks = 0
    private var stopped = false

    init(baseURL: String, session: URLSession = .shared, backoff: Backoff = Backoff(), tokenProvider: @escaping TokenProvider) {
        self.url = Gateway.gatewayURL(baseURL)
        self.tokenProvider = tokenProvider
        self.backoff = backoff
        self.connect = { [session] url in URLSessionWebSocket(session: session, url: url) }
    }

    /// Para testes: troca o WebSocket.
    init(url: URL, backoff: Backoff, connect: @escaping @Sendable (URL) -> WebSocketConnection, tokenProvider: @escaping TokenProvider) {
        self.url = url; self.backoff = backoff; self.connect = connect; self.tokenProvider = tokenProvider
    }

    /// `http(s)://host/prefixo` vira `ws(s)://host/prefixo/v1/gateway`.
    static func gatewayURL(_ baseURL: String) -> URL? {
        guard var c = URLComponents(string: baseURL.trimmingSuffix("/") + "/v1/gateway") else { return nil }
        switch c.scheme?.lowercased() {
        case "https": c.scheme = "wss"
        case "http": c.scheme = "ws"
        default: break
        }
        return c.url
    }

    // MARK: API pública

    public nonisolated func events() -> AsyncStream<GatewayEvent> { eventsBus.stream() }
    /// O estado atual primeiro, depois as mudanças.
    public nonisolated func connectionStates() -> AsyncStream<ConnectionState> { statesBus.stream(replayLatest: true) }
    public nonisolated var state: ConnectionState { statesBus.current ?? .disconnected }

    public func start() {
        guard runTask == nil, !stopped else { return }
        setState(.connecting)
        runTask = Task { [weak self] in await self?.run() }
    }

    /// Fecha o socket, cancela heartbeat e reconexão, esquece a sessão e encerra os streams.
    public func stop() {
        stopped = true
        runTask?.cancel(); runTask = nil
        heartbeatTask?.cancel(); heartbeatTask = nil
        socket?.close(); socket = nil
        sessionId = nil; lastSeq = 0
        setState(.disconnected)
        eventsBus.finish(); statesBus.finish()
    }

    /// Mute/câmera/tela do próprio usuário enquanto estiver num canal de voz. Sem ack: o resultado é o `voice.state` que volta.
    public func setVoiceState(muted: Bool, camOn: Bool, sharing: Bool) async throws {
        guard let socket, state == .connected else { throw KurzError("Sem ligação ao servidor em tempo real.", code: "not_connected") }
        try await socket.send(Self.frame(["op": .string("voice.state.set"),
                                          "d": .object(["muted": .bool(muted), "camOn": .bool(camOn), "sharing": .bool(sharing)])]))
    }

    // Para testes.
    var currentSessionId: String? { sessionId }
    var currentSeq: Int { lastSeq }

    // MARK: Laço de reconexão

    private enum Outcome { case dropped(reachedReady: Bool, code: Int?), fatal }

    private func run() async {
        var attempt = 0
        var forceRefresh = false
        var authFailures = 0
        var hadSession = false
        while !Task.isCancelled && !stopped {
            let outcome = await connectOnce(forceRefresh: forceRefresh)
            if Task.isCancelled || stopped { break }
            switch outcome {
            case .fatal:
                setState(.disconnected); runTask = nil; return
            case .dropped(let reached, let code):
                if reached { attempt = 0; authFailures = 0; hadSession = true }
                switch code {
                case 4009, 4002:
                    // Outra conexão assumiu a sessão, ou quadro inválido: reconectar só repetiria o erro.
                    setState(.disconnected); runTask = nil; return
                case 4001:
                    authFailures += 1
                    if authFailures > 1 { setState(.disconnected); runTask = nil; return }
                    forceRefresh = true
                default:
                    forceRefresh = false
                }
                setState(hadSession || attempt > 0 || code != nil ? .reconnecting : .connecting)
                let wait = backoff.delay(attempt: attempt)
                attempt += 1
                do { try await Task.sleep(nanoseconds: UInt64(wait * 1_000_000_000)) } catch { break }
            }
        }
    }

    /// Uma ligação, do upgrade até o socket cair.
    private func connectOnce(forceRefresh: Bool) async -> Outcome {
        guard let url else { return .fatal }
        let ws = connect(url)
        socket = ws
        var reached = false
        defer {
            heartbeatTask?.cancel(); heartbeatTask = nil
            ws.close()
            if socket === ws { socket = nil }
        }
        while !Task.isCancelled {
            let text: String
            do { text = try await ws.receive() } catch { return .dropped(reachedReady: reached, code: ws.closeCode) }
            guard let frame = Frame(text) else { continue }
            switch frame.op {
            case "hello":
                var interval = 30.0
                if case .number(let ms)? = frame.d?["heartbeatInterval"] { interval = ms / 1000 }
                let token: String
                do { token = try await tokenProvider(forceRefresh) }
                catch let e as KurzError where e.isUnauthenticated || (400..<500).contains(e.status) { return .fatal }
                catch { return .dropped(reachedReady: reached, code: nil) }
                if let sid = sessionId {
                    try? await ws.send(Self.frame(["op": .string("resume"),
                                                   "d": .object(["token": .string(token), "sessionId": .string(sid), "seq": .number(Double(lastSeq))])]))
                } else {
                    try? await ws.send(Self.frame(["op": .string("identify"), "d": .object(["token": .string(token)])]))
                }
                startHeartbeat(ws, interval: interval)
            case "ready":
                guard let d = frame.d, let ready = Self.decode(GatewayReady.self, d) else { continue }
                sessionId = ready.sessionId; lastSeq = 0
                reached = true
                setState(.connected)
                eventsBus.yield(.ready(ready))
            case "resumed":
                reached = true
                setState(.connected)
                eventsBus.yield(.resumed)
            case "invalid_session":
                sessionId = nil; lastSeq = 0
                eventsBus.yield(.invalidSession)
                let token = (try? await tokenProvider(false)) ?? ""
                try? await ws.send(Self.frame(["op": .string("identify"), "d": .object(["token": .string(token)])]))
            case "heartbeat_ack":
                missedAcks = 0
            case "dispatch":
                guard let seq = frame.seq, let t = frame.t else { continue }
                if seq <= lastSeq { continue } // já processado: a entrega é pelo menos uma vez
                lastSeq = seq
                eventsBus.yield(GatewayEvent.decode(name: t, data: frame.d ?? .null))
            default:
                continue
            }
        }
        return .dropped(reachedReady: reached, code: nil)
    }

    // MARK: Heartbeat

    private func startHeartbeat(_ ws: WebSocketConnection, interval: TimeInterval) {
        heartbeatTask?.cancel()
        missedAcks = 0
        heartbeatTask = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: UInt64(max(0.01, interval) * 1_000_000_000))
                if Task.isCancelled { return }
                guard let self else { return }
                if await !self.beat(ws) { return }
            }
        }
    }

    /// Falso quando o socket deve ser abandonado.
    private func beat(_ ws: WebSocketConnection) async -> Bool {
        guard socket === ws else { return false }
        if missedAcks >= 2 { ws.close(); return false } // servidor mudo: força a reconexão
        missedAcks += 1
        try? await ws.send(Self.frame(["op": .string("heartbeat")]))
        return true
    }

    // MARK: Utilidades

    private func setState(_ s: ConnectionState) {
        guard statesBus.current != s else { return }
        statesBus.yield(s)
    }

    private struct Frame {
        var op: String
        var t: String?
        var seq: Int?
        var d: JSONValue?
        init?(_ text: String) {
            guard let v = try? JSONDecoder().decode(JSONValue.self, from: Data(text.utf8)), let op = v["op"]?.stringValue else { return nil }
            self.op = op
            t = v["t"]?.stringValue
            if case .number(let n)? = v["seq"] { seq = Int(n) }
            d = v["d"]
        }
    }

    static func frame(_ obj: [String: JSONValue]) -> String {
        String(decoding: (try? JSONEncoder().encode(JSONValue.object(obj))) ?? Data("{}".utf8), as: UTF8.self)
    }

    private static func decode<T: Decodable>(_ type: T.Type, _ v: JSONValue) -> T? {
        guard let raw = try? KurzJSON.encoder().encode(v) else { return nil }
        return try? KurzJSON.decoder().decode(type, from: raw)
    }
}

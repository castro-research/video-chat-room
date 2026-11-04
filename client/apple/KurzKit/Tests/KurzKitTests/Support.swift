@_exported import Foundation
@testable import KurzKit

// MARK: - HTTP falso (URLProtocol), um handler por host para os testes poderem rodar em paralelo

struct MockResponse {
    var status = 200
    var headers: [String: String] = [:]
    var body = Data()
    var delay: TimeInterval = 0
    var transportError: URLError?

    static func json(_ status: Int = 200, _ json: String, headers: [String: String] = [:], delay: TimeInterval = 0) -> MockResponse {
        MockResponse(status: status, headers: headers, body: Data(json.utf8), delay: delay)
    }
    static func error(_ status: Int, _ code: String, _ message: String = "x", headers: [String: String] = [:]) -> MockResponse {
        .json(status, #"{"code":"\#(code)","message":"\#(message)"}"#, headers: headers)
    }
    static let noContent = MockResponse(status: 204)
}

struct RecordedRequest {
    var method: String
    var path: String
    var query: String?
    var headers: [String: String]
    var body: Data
    var bodyJSON: [String: Any]? { (try? JSONSerialization.jsonObject(with: body)) as? [String: Any] }
    var bearer: String? { headers["Authorization"].map { String($0.dropFirst("Bearer ".count)) } }
}

final class MockURLProtocol: URLProtocol, @unchecked Sendable {
    typealias Handler = @Sendable (RecordedRequest) -> MockResponse
    nonisolated(unsafe) private static var handlers: [String: Handler] = [:]
    private static let lock = NSLock()

    static func register(host: String, _ h: @escaping Handler) { lock.lock(); handlers[host] = h; lock.unlock() }
    private static func handler(for host: String) -> Handler? { lock.lock(); defer { lock.unlock() }; return handlers[host] }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func stopLoading() {}

    override func startLoading() {
        let req = request
        var body = req.httpBody ?? Data()
        if let stream = req.httpBodyStream {
            stream.open(); defer { stream.close() }
            var buf = [UInt8](repeating: 0, count: 4096)
            while stream.hasBytesAvailable { let n = stream.read(&buf, maxLength: buf.count); if n <= 0 { break }; body.append(buf, count: n) }
        }
        let rec = RecordedRequest(method: req.httpMethod ?? "GET", path: req.url?.path ?? "", query: req.url?.query,
                                  headers: req.allHTTPHeaderFields ?? [:], body: body)
        guard let h = Self.handler(for: req.url?.host ?? "") else { client?.urlProtocol(self, didFailWithError: URLError(.cannotFindHost)); return }
        let res = h(rec)
        DispatchQueue.global().asyncAfter(deadline: .now() + res.delay) { [self] in
            if let e = res.transportError { client?.urlProtocol(self, didFailWithError: e); return }
            let http = HTTPURLResponse(url: req.url!, statusCode: res.status, httpVersion: "HTTP/1.1", headerFields: res.headers)!
            client?.urlProtocol(self, didReceive: http, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: res.body)
            client?.urlProtocolDidFinishLoading(self)
        }
    }
}

/// Contador thread-safe para os handlers.
final class Counter: @unchecked Sendable {
    private let lock = NSLock()
    private var n = 0
    private var log: [RecordedRequest] = []
    var count: Int { lock.lock(); defer { lock.unlock() }; return n }
    var requests: [RecordedRequest] { lock.lock(); defer { lock.unlock() }; return log }
    @discardableResult func hit(_ r: RecordedRequest? = nil) -> Int { lock.lock(); defer { lock.unlock() }; n += 1; if let r { log.append(r) }; return n }
}

struct MockServer {
    let host: String
    let base: String
    let session: URLSession
    let store = InMemoryTokenStore()

    init(_ handler: @escaping MockURLProtocol.Handler) {
        host = "t\(UUID().uuidString.prefix(8).lowercased()).test"
        base = "http://\(host)"
        let cfg = URLSessionConfiguration.ephemeral
        cfg.protocolClasses = [MockURLProtocol.self]
        session = URLSession(configuration: cfg)
        MockURLProtocol.register(host: host, handler)
    }

    func api() -> KurzAPI { KurzAPI(baseURL: base, store: store, session: session) }

    func seed(access: String = "A0", refresh: String = "R0", expiresIn: TimeInterval = 900) {
        try? store.save(StoredTokens(accessToken: access, refreshToken: refresh, expiresAt: Date().addingTimeInterval(expiresIn)), for: base)
    }
}

// MARK: - JSON de exemplo (formas do contrato 0.6 / servidor real)

enum Sample {
    static let user = #"{"id":"100","username":"ana","name":"Ana","status":"online","subtitle":"oi","hue":210.5}"#
    static func session(access: String, refresh: String, expiresIn: Int = 900) -> String {
        #"{"accessToken":"\#(access)","refreshToken":"\#(refresh)","expiresIn":\#(expiresIn),"user":\#(user)}"#
    }
    static let guild = #"{"id":"200","name":"Casa","ownerId":"100","createdAt":"2026-11-01T12:00:00.123456Z"}"#
    static let message = #"{"id":"300","channelId":"400","author":\#(user),"createdAt":"2026-11-01T12:00:01Z","editedAt":"2026-11-01T12:05:00.5Z","text":"olá","replyToId":"299","reactions":[{"emoji":"👍","count":2,"me":true}],"attachments":[],"nonce":"n1"}"#
}

// MARK: - WebSocket falso

final class FakeSocket: WebSocketConnection, @unchecked Sendable {
    private let lock = NSLock()
    private var sentFrames: [String] = []
    private var isClosed = false
    private let stream: AsyncStream<Result<String, Error>>
    private let cont: AsyncStream<Result<String, Error>>.Continuation
    private var iterator: AsyncStream<Result<String, Error>>.AsyncIterator
    private var code: Int?
    var onSend: (@Sendable (FakeSocket, [String: Any]) -> Void)?

    init() {
        (stream, cont) = AsyncStream<Result<String, Error>>.makeStream()
        iterator = stream.makeAsyncIterator()
    }

    var sent: [[String: Any]] {
        lock.lock(); defer { lock.unlock() }
        return sentFrames.compactMap { (try? JSONSerialization.jsonObject(with: Data($0.utf8))) as? [String: Any] }
    }
    func sent(op: String) -> [[String: Any]] { sent.filter { $0["op"] as? String == op } }
    var closed: Bool { lock.lock(); defer { lock.unlock() }; return isClosed }

    /// Quadro do servidor para o cliente.
    func push(_ json: String) { cont.yield(.success(json)) }
    /// O servidor fecha com um código.
    func drop(code: Int?) { lock.lock(); self.code = code; lock.unlock(); cont.yield(.failure(URLError(.networkConnectionLost))) }

    private func record(_ text: String) -> (@Sendable (FakeSocket, [String: Any]) -> Void)? {
        lock.lock(); defer { lock.unlock() }
        sentFrames.append(text)
        return onSend
    }
    func send(_ text: String) async throws {
        let cb = record(text)
        if let o = (try? JSONSerialization.jsonObject(with: Data(text.utf8))) as? [String: Any] { cb?(self, o) }
    }
    func receive() async throws -> String {
        var it = iterator
        guard let next = await it.next() else { throw URLError(.cancelled) }
        return try next.get()
    }
    func close() { lock.lock(); isClosed = true; lock.unlock(); cont.yield(.failure(URLError(.cancelled))); cont.finish() }
    var closeCode: Int? { lock.lock(); defer { lock.unlock() }; return code }
}

/// Servidor de gateway de mentira: cada conexão recebe `hello` e responde identify/resume como o roteiro mandar.
final class FakeGatewayServer: @unchecked Sendable {
    private let lock = NSLock()
    private(set) var sockets: [FakeSocket] = []
    var resumeAccepted = true
    var sessionCounter = 0
    var heartbeatMs = 30000

    func connect(_ url: URL) -> WebSocketConnection {
        let s = FakeSocket()
        s.onSend = { [unowned self] sock, frame in
            switch frame["op"] as? String {
            case "identify":
                lock.lock(); sessionCounter += 1; let n = sessionCounter; lock.unlock()
                sock.push(#"{"op":"ready","d":{"sessionId":"sess\#(n)","user":{"id":"100"},"guilds":[{"id":"200","name":"Casa","ownerId":"100"}],"heartbeatInterval":\#(heartbeatMs)}}"#)
            case "resume":
                sock.push(resumeAccepted ? #"{"op":"resumed"}"# : #"{"op":"invalid_session"}"#)
            case "heartbeat":
                sock.push(#"{"op":"heartbeat_ack"}"#)
            default: break
            }
        }
        lock.lock(); sockets.append(s); lock.unlock()
        s.push(#"{"op":"hello","d":{"heartbeatInterval":\#(heartbeatMs)}}"#)
        return s
    }
    var count: Int { lock.lock(); defer { lock.unlock() }; return sockets.count }
    func socket(_ i: Int) -> FakeSocket { lock.lock(); defer { lock.unlock() }; return sockets[i] }
}

/// Consome um stream em segundo plano e guarda o que chegou.
final class Collector<T: Sendable>: @unchecked Sendable {
    private let lock = NSLock()
    private var items: [T] = []
    private var task: Task<Void, Never>?
    init(_ stream: AsyncStream<T>) {
        task = Task { [weak self] in for await v in stream { self?.append(v) } }
    }
    private func append(_ v: T) { lock.lock(); items.append(v); lock.unlock() }
    var all: [T] { lock.lock(); defer { lock.unlock() }; return items }
    func cancel() { task?.cancel() }
    deinit { task?.cancel() }
}

import Foundation

public enum AuthState: Equatable, Sendable {
    /// Ainda não se sabe (antes de `restore()`).
    case unknown
    case authenticated
    case unauthenticated(Reason)

    public enum Reason: Equatable, Sendable {
        /// Não há tokens guardados para este servidor.
        case noCredentials
        /// O usuário saiu (logout).
        case loggedOut
        /// O refresh foi recusado (token expirado, revogado ou reutilizado): é preciso entrar de novo.
        case sessionExpired
    }
}

/// Dono dos tokens de um servidor. O refresh é rotativo (o token antigo morre ao usar), por isso há UM refresh por vez (single-flight):
/// chamadas concorrentes esperam o mesmo resultado.
public actor SessionManager {
    private let http: HTTPClient
    private let store: TokenStore
    private let baseKey: String
    private let now: @Sendable () -> Date
    /// Renova antes de expirar quando faltar menos que isto.
    private let leeway: TimeInterval
    private var refreshTask: Task<String, Error>?
    private let states: Broadcaster<AuthState>
    private var refreshCount = 0
    /// Espera antes de repetir um refresh recusado com 401 (ver `performRefresh`).
    private let retryDelay: TimeInterval

    init(http: HTTPClient, store: TokenStore, leeway: TimeInterval = 30, retryDelay: TimeInterval = 1, now: @escaping @Sendable () -> Date = { Date() }) {
        self.http = http; self.store = store; self.baseKey = http.baseURL; self.leeway = leeway; self.now = now; self.retryDelay = retryDelay
        self.states = Broadcaster(initial: .unknown)
    }

    // MARK: Estado

    public nonisolated var authState: AuthState { states.current ?? .unknown }
    /// Estado atual primeiro, depois as mudanças.
    public nonisolated func authStates() -> AsyncStream<AuthState> { states.stream(replayLatest: true) }

    /// Quantos refreshes foram ao servidor (para testes).
    var refreshRequests: Int { refreshCount }

    /// Lê o store e diz se há sessão guardada. Não vai à rede.
    @discardableResult
    public func restore() -> Bool {
        if (try? store.load(for: baseKey)) != nil { states.yield(.authenticated); return true }
        states.yield(.unauthenticated(.noCredentials))
        return false
    }

    // MARK: Entrar e sair

    func establish(_ session: Session) throws {
        try store.save(StoredTokens(accessToken: session.accessToken, refreshToken: session.refreshToken,
                                    expiresAt: now().addingTimeInterval(TimeInterval(session.expiresIn))), for: baseKey)
        states.yield(.authenticated)
    }

    public func logout() async {
        if let t = try? store.load(for: baseKey) {
            // Melhor esforço: se o servidor não responder, a sessão local acaba do mesmo jeito.
            let body = try? JSONEncoder().encode(["refreshToken": t.refreshToken])
            _ = try? await http.send("POST", "/v1/auth/logout", body: body)
        }
        try? store.clear(for: baseKey)
        states.yield(.unauthenticated(.loggedOut))
    }

    // MARK: Tokens

    /// Access token válido, renovando antes se estiver para expirar.
    public func accessToken() async throws -> String {
        guard let t = try store.load(for: baseKey) else {
            states.yield(.unauthenticated(.noCredentials))
            throw KurzError.unauthenticated()
        }
        if t.expiresAt.timeIntervalSince(now()) < leeway { return try await refresh(failing: t.accessToken) }
        return t.accessToken
    }

    /// Chamado quando `failing` recebeu 401 token_expired. Se outro já renovou (o token guardado mudou), usa o novo sem ir ao servidor.
    public func refresh(failing: String?) async throws -> String {
        if let task = refreshTask { return try await task.value }
        guard let current = try store.load(for: baseKey) else {
            states.yield(.unauthenticated(.noCredentials))
            throw KurzError.unauthenticated()
        }
        if let failing, current.accessToken != failing, current.expiresAt.timeIntervalSince(now()) > 0 { return current.accessToken }
        let task = Task { try await self.performRefresh(current.refreshToken) }
        refreshTask = task
        defer { refreshTask = nil }
        return try await task.value
    }

    /// Renova já, mesmo sem expirar (o gateway fechou com 4001).
    public func forceRefresh() async throws -> String {
        guard let t = try store.load(for: baseKey) else { throw KurzError.unauthenticated() }
        return try await refresh(failing: t.accessToken)
    }

    private func performRefresh(_ refreshToken: String) async throws -> String {
        do {
            do {
                return try await requestRefresh(refreshToken)
            } catch let e as KurzError where e.status == 401 {
                // A rotação não é segura para retry: se a resposta com o token novo se perdeu, o servidor já gastou este. Ele tolera a
                // repetição durante uma janela curta, por isso tenta UMA vez mais, se o token guardado ainda é o mesmo que foi enviado.
                try await Task.sleep(nanoseconds: UInt64(retryDelay * 1_000_000_000))
                guard let cur = try? store.load(for: baseKey) else { throw e }
                if cur.refreshToken != refreshToken { return cur.accessToken }  // outro caminho (novo login) já renovou
                return try await requestRefresh(refreshToken)
            }
        } catch let e as KurzError where (400..<500).contains(e.status) && e.status != 429 {
            // Refresh recusado de vez (expirado, revogado, reutilizado): a sessão acabou.
            try? store.clear(for: baseKey)
            states.yield(.unauthenticated(.sessionExpired))
            throw KurzError("Sessão expirada. Entra novamente.", code: e.code, status: e.status, serverMessage: e.serverMessage)
        }
        // Rede fora ou 5xx/429: mantém os tokens, o chamador pode tentar depois.
    }

    private func requestRefresh(_ refreshToken: String) async throws -> String {
        refreshCount += 1
        let body = try JSONEncoder().encode(["refreshToken": refreshToken])
        let data = try await http.send("POST", "/v1/auth/refresh", body: body)
        guard let s = try? KurzJSON.decoder().decode(Session.self, from: data) else { throw KurzError.decoding }
        try establish(s)
        return s.accessToken
    }
}

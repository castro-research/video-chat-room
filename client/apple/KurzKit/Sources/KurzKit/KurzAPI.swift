import Foundation

/// Cliente REST do servidor Kurz (contrato 0.6). Seguro entre threads: guarde uma instância por servidor.
/// Envia o Bearer sozinho, renova o token de forma transparente (veja `SessionManager`) e traduz erros em `KurzError`.
public final class KurzAPI: Sendable {
    public let baseURL: String
    public let sessions: SessionManager
    let http: HTTPClient

    public convenience init(baseURL: String, store: TokenStore = KeychainTokenStore(), session: URLSession = .shared) {
        self.init(baseURL: baseURL, store: store, session: session, refreshRetryDelay: 1)
    }

    init(baseURL: String, store: TokenStore, session: URLSession, refreshRetryDelay: TimeInterval) {
        let http = HTTPClient(baseURL: baseURL, session: session)
        self.http = http
        self.baseURL = http.baseURL
        self.sessions = SessionManager(http: http, store: store, retryDelay: refreshRetryDelay)
    }

    // MARK: Estado de autenticação

    public var authState: AuthState { sessions.authState }
    /// Emite o estado atual e depois cada mudança. `.unauthenticated(.sessionExpired)` = a UI deve mostrar o login.
    public func authStates() -> AsyncStream<AuthState> { sessions.authStates() }
    /// Há tokens guardados? (não valida no servidor; use `me()` para isso).
    @discardableResult public func restoreSession() async -> Bool { await sessions.restore() }

    // MARK: Descoberta

    public func wellKnown() async throws -> ServerInfo { try await get("/.well-known/kurz", auth: false) }

    // MARK: Conta

    public func register(username: String, password: String, name: String? = nil, inviteCode: String? = nil) async throws -> Session {
        var body: [String: JSONValue] = ["username": .string(username), "password": .string(password)]
        if let name { body["name"] = .string(name) }
        if let inviteCode { body["inviteCode"] = .string(inviteCode) }
        let s: Session = try decode(try await http.send("POST", "/v1/auth/register", body: encode(body)))
        try await sessions.establish(s)
        return s
    }

    public func login(username: String, password: String) async throws -> Session {
        let data: Data
        do { data = try await http.send("POST", "/v1/auth/login", body: encode(["username": username, "password": password])) }
        catch let e as KurzError where e.code == "unauthorized" {
            throw KurzError("Utilizador ou palavra-passe incorretos.", code: e.code, status: e.status, serverMessage: e.serverMessage)
        }
        let s: Session = try decode(data)
        try await sessions.establish(s)
        return s
    }

    /// Revoga o refresh token no servidor (melhor esforço) e limpa o store. Sempre acaba em `.unauthenticated(.loggedOut)`.
    public func logout() async { await sessions.logout() }

    public func me() async throws -> User { try await get("/v1/me") }

    public func updateMe(name: String? = nil, subtitle: String? = nil, hue: Double? = nil) async throws -> User {
        var body: [String: JSONValue] = [:]
        if let name { body["name"] = .string(name) }
        if let subtitle { body["subtitle"] = .string(subtitle) }
        if let hue { body["hue"] = .number(hue) }
        return try await send("PATCH", "/v1/me", body: body)
    }

    // MARK: Guilds

    private struct Items<T: Decodable>: Decodable { var items: [T] }

    public func guilds() async throws -> [Guild] { try await (get("/v1/guilds") as Items<Guild>).items }
    public func createGuild(name: String) async throws -> Guild { try await send("POST", "/v1/guilds", body: ["name": .string(name)]) }
    public func guild(_ id: String) async throws -> Guild { try await get("/v1/guilds/\(seg(id))") }
    public func deleteGuild(_ id: String) async throws { try await sendVoid("DELETE", "/v1/guilds/\(seg(id))") }
    public func leaveGuild(_ id: String) async throws { try await sendVoid("DELETE", "/v1/guilds/\(seg(id))/members/@me") }

    // MARK: Convites

    public func createInvite(guildId: String, maxUses: Int? = nil, expiresIn: Int? = nil) async throws -> Invite {
        var body: [String: JSONValue] = [:]
        if let maxUses { body["maxUses"] = .number(Double(maxUses)) }
        if let expiresIn { body["expiresIn"] = .number(Double(expiresIn)) }
        return try await send("POST", "/v1/guilds/\(seg(guildId))/invites", body: body)
    }
    public func invites(guildId: String) async throws -> [Invite] { try await (get("/v1/guilds/\(seg(guildId))/invites") as Items<Invite>).items }
    public func previewInvite(code: String) async throws -> InvitePreview { try await get("/v1/invites/\(seg(code))") }
    /// Entra na guild do convite. Se já é membro, devolve a guild sem gastar um uso.
    public func joinInvite(code: String) async throws -> Guild { try await send("POST", "/v1/invites/\(seg(code))/join") }
    public func revokeInvite(code: String) async throws { try await sendVoid("DELETE", "/v1/invites/\(seg(code))") }

    // MARK: Membros e cargos

    public func members(guildId: String, after: String? = nil, limit: Int? = nil) async throws -> MemberPage {
        try await get("/v1/guilds/\(seg(guildId))/members", query: items(("after", after), ("limit", limit)))
    }
    public func roles(guildId: String) async throws -> [Role] { try await (get("/v1/guilds/\(seg(guildId))/roles") as Items<Role>).items }

    // MARK: Canais

    public func channels(guildId: String) async throws -> [Channel] { try await (get("/v1/guilds/\(seg(guildId))/channels") as Items<Channel>).items }
    public func channel(_ id: String) async throws -> Channel { try await get("/v1/channels/\(seg(id))") }
    public func createChannel(guildId: String, name: String, kind: ChannelKind, parentId: String? = nil) async throws -> Channel {
        var body: [String: JSONValue] = ["name": .string(name), "kind": .string(kind.rawValue)]
        if let parentId { body["parentId"] = .string(parentId) }
        return try await send("POST", "/v1/guilds/\(seg(guildId))/channels", body: body)
    }
    public func renameChannel(_ id: String, name: String) async throws -> Channel {
        try await send("PATCH", "/v1/channels/\(seg(id))", body: ["name": .string(name)])
    }
    /// `parentId`: `nil` não mexe; `.some(nil)` tira da categoria; `.some(id)` move para ela.
    public func updateChannel(_ id: String, name: String? = nil, parentId: String?? = nil, position: Int? = nil) async throws -> Channel {
        var body: [String: JSONValue] = [:]
        if let name { body["name"] = .string(name) }
        if let parentId { body["parentId"] = parentId.map { .string($0) } ?? .null }
        if let position { body["position"] = .number(Double(position)) }
        return try await send("PATCH", "/v1/channels/\(seg(id))", body: body)
    }
    public func deleteChannel(_ id: String) async throws { try await sendVoid("DELETE", "/v1/channels/\(seg(id))") }

    // MARK: Mensagens

    /// Sem cursor: as mais novas. No máximo um entre `before`, `after` e `around`. A página vem sempre da mais antiga para a mais nova.
    public func messages(channelId: String, before: String? = nil, after: String? = nil, around: String? = nil, limit: Int? = nil) async throws -> MessagePage {
        try await get("/v1/channels/\(seg(channelId))/messages", query: items(("before", before), ("after", after), ("around", around), ("limit", limit)))
    }

    /// `nonce` (até 64 chars) volta na sua própria `message.create` para trocar a cópia otimista pela real.
    public func sendMessage(channelId: String, text: String, replyToId: String? = nil, nonce: String? = nil) async throws -> Message {
        var body: [String: JSONValue] = ["text": .string(text)]
        if let replyToId { body["replyToId"] = .string(replyToId) }
        if let nonce { body["nonce"] = .string(nonce) }
        return try await send("POST", "/v1/channels/\(seg(channelId))/messages", body: body)
    }
    public func editMessage(channelId: String, messageId: String, text: String) async throws -> Message {
        try await send("PATCH", "/v1/channels/\(seg(channelId))/messages/\(seg(messageId))", body: ["text": .string(text)])
    }
    public func deleteMessage(channelId: String, messageId: String) async throws {
        try await sendVoid("DELETE", "/v1/channels/\(seg(channelId))/messages/\(seg(messageId))")
    }

    // MARK: Reações

    public func addReaction(channelId: String, messageId: String, emoji: String) async throws {
        try await sendVoid("PUT", "/v1/channels/\(seg(channelId))/messages/\(seg(messageId))/reactions/\(seg(emoji))/@me")
    }
    public func removeReaction(channelId: String, messageId: String, emoji: String) async throws {
        try await sendVoid("DELETE", "/v1/channels/\(seg(channelId))/messages/\(seg(messageId))/reactions/\(seg(emoji))/@me")
    }

    // MARK: Leitura

    /// Marca o canal como lido até `messageId`. Nunca anda para trás.
    public func markRead(channelId: String, messageId: String) async throws {
        try await sendVoid("POST", "/v1/channels/\(seg(channelId))/read", body: ["messageId": .string(messageId)])
    }
    public func readState(channelId: String) async throws -> ReadState { try await get("/v1/channels/\(seg(channelId))/read") }
    public func readStates(guildId: String) async throws -> [ReadState] { try await (get("/v1/guilds/\(seg(guildId))/read") as Items<ReadState>).items }

    // MARK: Voz

    /// 501 `not_supported` se o servidor não anuncia `voice`. O token de mídia vale 60 s e é de uso único: chame de novo para reconectar.
    public func joinVoice(channelId: String) async throws -> VoiceSession { try await send("POST", "/v1/channels/\(seg(channelId))/voice/join") }
    public func leaveVoice(channelId: String) async throws { try await sendVoid("POST", "/v1/channels/\(seg(channelId))/voice/leave") }
    public func voiceParticipants(channelId: String) async throws -> [VoiceParticipant] {
        struct R: Decodable { var participants: [VoiceParticipant] }
        return try await (get("/v1/channels/\(seg(channelId))/voice") as R).participants
    }

    // MARK: Gateway

    /// Cria o cliente do gateway deste servidor, usando os mesmos tokens (renovados pelo `SessionManager`).
    public func makeGateway(session: URLSession = .shared, backoff: Backoff = Backoff()) -> Gateway {
        let sessions = self.sessions
        return Gateway(baseURL: baseURL, session: session, backoff: backoff) { force in
            force ? try await sessions.forceRefresh() : try await sessions.accessToken()
        }
    }

    // MARK: - Internos

    private func seg(_ s: String) -> String { HTTPClient.seg(s) }
    private func encode(_ v: some Encodable) throws -> Data { try KurzJSON.encoder().encode(v) }
    private func decode<T: Decodable>(_ data: Data) throws -> T {
        do { return try KurzJSON.decoder().decode(T.self, from: data) } catch { throw KurzError.decoding }
    }
    private func items(_ pairs: (String, (any CustomStringConvertible)?)...) -> [URLQueryItem] {
        pairs.compactMap { k, v in v.map { URLQueryItem(name: k, value: "\($0)") } }
    }

    private func get<T: Decodable>(_ path: String, query: [URLQueryItem] = [], auth: Bool = true) async throws -> T {
        try decode(try await request("GET", path, query: query, body: nil, auth: auth))
    }
    private func send<T: Decodable>(_ method: String, _ path: String, body: [String: JSONValue]? = nil) async throws -> T {
        try decode(try await request(method, path, query: [], body: try body.map(encode), auth: true))
    }
    private func sendVoid(_ method: String, _ path: String, body: [String: JSONValue]? = nil) async throws {
        _ = try await request(method, path, query: [], body: try body.map(encode), auth: true)
    }

    /// Envia com o Bearer; em 401 `token_expired` renova (single-flight) e repete UMA vez.
    private func request(_ method: String, _ path: String, query: [URLQueryItem], body: Data?, auth: Bool) async throws -> Data {
        guard auth else { return try await http.send(method, path, query: query, body: body) }
        let token = try await sessions.accessToken()
        do {
            return try await http.send(method, path, query: query, body: body, bearer: token)
        } catch let e as KurzError where e.status == 401 && e.code == "token_expired" {
            let fresh = try await sessions.refresh(failing: token)
            return try await http.send(method, path, query: query, body: body, bearer: fresh)
        }
    }
}

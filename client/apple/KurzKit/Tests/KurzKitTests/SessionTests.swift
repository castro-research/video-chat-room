import Testing
@testable import KurzKit

@Suite("Sessão: refresh single-flight")
struct SessionTests {
    /// Servidor que expira "A0": só o token "A1" (e seguintes) passam; refresh rotativo: R0 -> R1, a segunda tentativa com R0 falha.
    func rotatingServer(refreshDelay: TimeInterval = 0.2, refreshes: Counter, meHits: Counter) -> MockServer {
        let used = NSLock()
        nonisolated(unsafe) var usedRefresh = Set<String>()
        return MockServer { r in
            switch r.path {
            case "/v1/auth/refresh":
                refreshes.hit(r)
                let rt = r.bodyJSON?["refreshToken"] as? String ?? ""
                used.lock(); defer { used.unlock() }
                if rt != "R0" || usedRefresh.contains(rt) { return .error(401, "unauthorized", "reuso") }
                usedRefresh.insert(rt)
                return .json(200, Sample.session(access: "A1", refresh: "R1"), delay: refreshDelay)
            default:
                meHits.hit(r)
                return r.bearer == "A1" ? .json(200, Sample.user) : .error(401, "token_expired")
            }
        }
    }

    @Test func dezChamadasConcorrentesCom401FazemUmSoRefresh() async throws {
        let refreshes = Counter(), me = Counter()
        let srv = rotatingServer(refreshes: refreshes, meHits: me)
        srv.seed(access: "A0", refresh: "R0") // ainda "válido" pelo relógio, mas o servidor o rejeita
        let api = srv.api()
        let users = try await withThrowingTaskGroup(of: User.self) { g in
            for _ in 0..<10 { g.addTask { try await api.me() } }
            var out: [User] = []
            for try await u in g { out.append(u) }
            return out
        }
        #expect(users.count == 10)
        #expect(refreshes.count == 1)
        #expect(try srv.store.load(for: srv.base)?.refreshToken == "R1")
        #expect(me.requests.filter { $0.bearer == "A1" }.count == 10)
    }

    @Test func refreshAntecipadoQuandoFaltaPoucoParaExpirar() async throws {
        let refreshes = Counter(), me = Counter()
        let srv = rotatingServer(refreshDelay: 0, refreshes: refreshes, meHits: me)
        srv.seed(access: "A0", refresh: "R0", expiresIn: 5) // < 30 s de folga
        let api = srv.api()
        _ = try await api.me()
        #expect(refreshes.count == 1)
        // O 401 nunca aconteceu: a primeira chamada a /me já foi com o token novo.
        #expect(me.count == 1 && me.requests[0].bearer == "A1")
        _ = try await api.me()
        #expect(refreshes.count == 1)
    }

    @Test func chamadasConcorrentesComTokenPertoDeExpirarTambemFazemUmRefresh() async throws {
        let refreshes = Counter(), me = Counter()
        let srv = rotatingServer(refreshes: refreshes, meHits: me)
        srv.seed(access: "A0", refresh: "R0", expiresIn: 1)
        let api = srv.api()
        try await withThrowingTaskGroup(of: Void.self) { g in
            for _ in 0..<8 { g.addTask { _ = try await api.me() } }
            try await g.waitForAll()
        }
        #expect(refreshes.count == 1)
    }

    @Test func refreshRecusadoLimpaOStoreEEmiteDesautenticado() async throws {
        let srv = MockServer { r in
            r.path == "/v1/auth/refresh" ? .error(401, "unauthorized", "reuso") : .error(401, "token_expired")
        }
        srv.seed()
        let api = srv.api()
        let states = Collector(api.authStates())
        do { _ = try await api.me(); Issue.record("devia falhar") }
        catch let e as KurzError { #expect(e.isUnauthenticated) }
        #expect(try srv.store.load(for: srv.base) == nil)
        #expect(api.authState == .unauthenticated(.sessionExpired))
        await eventually("estado emitido") { states.all.contains(.unauthenticated(.sessionExpired)) }
        // depois disso nenhuma chamada vai à rede
        do { _ = try await api.me(); Issue.record("devia falhar") }
        catch let e as KurzError { #expect(e.code == "unauthenticated") }
    }

    @Test func refreshComRedeForaMantemOsTokens() async throws {
        let srv = MockServer { r in
            r.path == "/v1/auth/refresh" ? MockResponse(transportError: URLError(.notConnectedToInternet)) : .error(401, "token_expired")
        }
        srv.seed()
        let api = srv.api()
        do { _ = try await api.me(); Issue.record("devia falhar") }
        catch let e as KurzError { #expect(e.isNetwork) }
        #expect(try srv.store.load(for: srv.base)?.refreshToken == "R0")
        #expect(api.authState != .unauthenticated(.sessionExpired))
    }

    @Test func refreshCom5xxMantemOsTokens() async throws {
        let srv = MockServer { r in r.path == "/v1/auth/refresh" ? .error(503, "x") : .error(401, "token_expired") }
        srv.seed()
        _ = try? await srv.api().me()
        #expect(try srv.store.load(for: srv.base)?.refreshToken == "R0")
    }

    @Test func umUnicoRetryEmCasoDe401Persistente() async throws {
        let refreshes = Counter(), me = Counter()
        let srv = MockServer { r in
            if r.path == "/v1/auth/refresh" { refreshes.hit(); return .json(200, Sample.session(access: "A1", refresh: "R1")) }
            me.hit(); return .error(401, "token_expired")
        }
        srv.seed()
        do { _ = try await srv.api().me(); Issue.record("devia falhar") } catch let e as KurzError { #expect(e.code == "token_expired") }
        #expect(refreshes.count == 1 && me.count == 2)
    }

    @Test func restoreRefleteOStore() async throws {
        let srv = MockServer { _ in .noContent }
        let api = srv.api()
        #expect(await api.restoreSession() == false)
        #expect(api.authState == .unauthenticated(.noCredentials))
        srv.seed()
        #expect(await api.restoreSession() == true)
        #expect(api.authState == .authenticated)
    }

    @Test func tokensDeServidoresDiferentesNaoSeMisturam() throws {
        let store = InMemoryTokenStore()
        let t = StoredTokens(accessToken: "a", refreshToken: "r", expiresAt: Date())
        try store.save(t, for: "http://a")
        #expect(try store.load(for: "http://b") == nil)
        try store.clear(for: "http://a")
        #expect(try store.load(for: "http://a") == nil)
    }

    @Test func keychainGuardaELe() throws {
        let store = KeychainTokenStore(service: "fyi.kurz.tests.\(UUID().uuidString)")
        let base = "http://kc.test"
        let t = StoredTokens(accessToken: "a", refreshToken: "r", expiresAt: Date(timeIntervalSince1970: 1_800_000_000))
        do { try store.save(t, for: base) }
        catch { Issue.record("Keychain indisponível neste ambiente: \(error)"); return }
        defer { try? store.clear(for: base) }
        #expect(try store.load(for: base) == t)
        let t2 = StoredTokens(accessToken: "a2", refreshToken: "r2", expiresAt: t.expiresAt)
        try store.save(t2, for: base)
        #expect(try store.load(for: base) == t2)
        try store.clear(for: base)
        #expect(try store.load(for: base) == nil)
    }
}

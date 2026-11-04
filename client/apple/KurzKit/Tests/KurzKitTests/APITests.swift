import Testing
@testable import KurzKit

@Suite("KurzAPI com servidor falso")
struct APITests {
    @Test func loginGuardaTokensEMandaBearer() async throws {
        let hits = Counter()
        let srv = MockServer { r in
            hits.hit(r)
            switch r.path {
            case "/v1/auth/login": return .json(200, Sample.session(access: "AT", refresh: "RT"))
            case "/v1/me": return r.bearer == "AT" ? .json(200, Sample.user) : .error(401, "unauthorized")
            default: return .error(404, "not_found")
            }
        }
        let api = srv.api()
        let s = try await api.login(username: "ana", password: "senha1234")
        #expect(s.accessToken == "AT")
        #expect(try srv.store.load(for: srv.base)?.refreshToken == "RT")
        #expect(api.authState == .authenticated)
        let me = try await api.me()
        #expect(me.username == "ana")
        #expect(hits.requests.last?.headers["Authorization"] == "Bearer AT")
        #expect(hits.requests.first?.bodyJSON?["username"] as? String == "ana")
    }

    @Test func loginComSenhaErradaDaMensagemEmPortugues() async throws {
        let srv = MockServer { _ in .error(401, "unauthorized") }
        do { _ = try await srv.api().login(username: "a", password: "b"); Issue.record("devia falhar") }
        catch let e as KurzError { #expect(e.message == "Utilizador ou palavra-passe incorretos." && e.status == 401 && e.code == "unauthorized") }
    }

    @Test func semTokensNaoVaiARede() async throws {
        let hits = Counter()
        let srv = MockServer { _ in hits.hit(); return .json(200, Sample.user) }
        do { _ = try await srv.api().me(); Issue.record("devia falhar") }
        catch let e as KurzError { #expect(e.code == "unauthenticated") }
        #expect(hits.count == 0)
    }

    @Test(arguments: [
        (403, "missing_permission", "Falta-te permissão para esta ação."),
        (403, "banned", "Foste banido desta comunidade."),
        (410, "invite_expired", "Este convite expirou."),
        (404, "invalid_invite", "Convite inválido."),
        (409, "username_taken", "Esse nome de utilizador já está em uso."),
        (409, "owner_cannot_leave", "O dono não pode sair da comunidade. Transfere a propriedade primeiro."),
        (403, "hierarchy", "O teu cargo não é superior ao do alvo."),
        (501, "not_supported", "Este servidor não suporta esta funcionalidade."),
        (404, "not_found", "Não encontrado."),
        (500, "algo_novo", "Erro no servidor (500)."),
        (418, "algo_novo", "O servidor recusou o pedido (418)."),
    ])
    func mapeamentoDeErros(status: Int, code: String, message: String) async throws {
        let srv = MockServer { _ in .error(status, code, "msg do servidor") }
        srv.seed()
        do { _ = try await srv.api().guilds(); Issue.record("devia falhar") }
        catch let e as KurzError {
            #expect(e.code == code && e.status == status && e.message == message && e.serverMessage == "msg do servidor")
        }
    }

    @Test func rateLimitedTrazRetryAfter() async throws {
        let srv = MockServer { _ in .error(429, "rate_limited", headers: ["Retry-After": "7"]) }
        srv.seed()
        do { _ = try await srv.api().sendMessage(channelId: "1", text: "x"); Issue.record("devia falhar") }
        catch let e as KurzError {
            #expect(e.isRateLimited && e.retryAfter == 7 && e.message == "Demasiados pedidos. Tenta novamente em 7 s.")
        }
    }

    @Test func respostaQueNaoEJsonVira500Generico() async throws {
        let srv = MockServer { _ in MockResponse(status: 502, body: Data("<html>bad gateway</html>".utf8)) }
        srv.seed()
        do { _ = try await srv.api().guilds(); Issue.record("devia falhar") }
        catch let e as KurzError { #expect(e.status == 502 && e.code == "http_502" && e.message == "Erro no servidor (502).") }
    }

    @Test func jsonInesperadoViraErroDeDecoding() async throws {
        let srv = MockServer { _ in .json(200, #"{"foo":1}"#) }
        srv.seed()
        do { _ = try await srv.api().me(); Issue.record("devia falhar") }
        catch let e as KurzError { #expect(e.code == "decoding") }
    }

    @Test func servidorForaDoArViraErroDeRede() async throws {
        let srv = MockServer { _ in MockResponse(transportError: URLError(.cannotConnectToHost)) }
        srv.seed()
        do { _ = try await srv.api().me(); Issue.record("devia falhar") }
        catch let e as KurzError { #expect(e.isNetwork && e.message.hasPrefix("Servidor indisponível em")) }
    }

    @Test func mensagensMontamQueryECorpo() async throws {
        let hits = Counter()
        let srv = MockServer { r in
            hits.hit(r)
            return r.method == "GET" ? .json(200, #"{"items":[],"hasMore":false,"hasNewer":false}"#) : .json(201, Sample.message)
        }
        srv.seed()
        let api = srv.api()
        _ = try await api.messages(channelId: "400", before: "300", limit: 25)
        _ = try await api.messages(channelId: "400", around: "300")
        _ = try await api.messages(channelId: "400")
        _ = try await api.sendMessage(channelId: "400", text: "olá", replyToId: "299", nonce: "n1")
        let r = hits.requests
        #expect(r[0].path == "/v1/channels/400/messages" && Set(r[0].query?.split(separator: "&").map(String.init) ?? []) == ["before=300", "limit=25"])
        #expect(r[1].query == "around=300")
        #expect(r[2].query == nil)
        #expect(r[3].method == "POST" && r[3].bodyJSON?["text"] as? String == "olá" && r[3].bodyJSON?["replyToId"] as? String == "299" && r[3].bodyJSON?["nonce"] as? String == "n1")
        #expect(r[3].headers["Content-Type"] == "application/json")
    }

    @Test func reacaoCodificaOEmojiNoCaminho() async throws {
        let hits = Counter()
        let srv = MockServer { r in hits.hit(r); return .noContent }
        srv.seed()
        let api = srv.api()
        try await api.addReaction(channelId: "1", messageId: "2", emoji: "👍")
        try await api.removeReaction(channelId: "1", messageId: "2", emoji: "a/b")
        #expect(hits.requests[0].method == "PUT" && hits.requests[0].path == "/v1/channels/1/messages/2/reactions/👍/@me")
        #expect(hits.requests[1].method == "DELETE" && hits.requests[1].path == "/v1/channels/1/messages/2/reactions/a/b/@me") // path decodificado pelo URL
    }

    @Test func updateChannelDistingueNullDeAusente() async throws {
        let hits = Counter()
        let srv = MockServer { r in hits.hit(r); return .json(200, #"{"id":"1","guildId":"2","name":"x","kind":"text","position":0}"#) }
        srv.seed()
        let api = srv.api()
        _ = try await api.updateChannel("1", name: "novo")
        _ = try await api.updateChannel("1", parentId: .some(nil))
        _ = try await api.updateChannel("1", parentId: .some("9"), position: 2)
        let b = hits.requests.map { $0.bodyJSON ?? [:] }
        #expect(b[0]["parentId"] == nil && b[0]["name"] as? String == "novo")
        #expect(b[1]["parentId"] is NSNull)
        #expect(b[2]["parentId"] as? String == "9" && b[2]["position"] as? Int == 2)
    }

    @Test func wellKnownNaoMandaToken() async throws {
        let hits = Counter()
        let srv = MockServer { r in hits.hit(r); return .json(200, #"{"contract":"0.6","name":"K","registration":"open","capabilities":["text"]}"#) }
        let info = try await srv.api().wellKnown()
        #expect(info.name == "K" && hits.requests[0].path == "/.well-known/kurz" && hits.requests[0].headers["Authorization"] == nil)
    }

    @Test func voiceJoinDecodificaSessao() async throws {
        let srv = MockServer { _ in .json(200, #"{"media":{"url":"ws://x/v1/media","token":"t"},"iceServers":[],"participants":[]}"#) }
        srv.seed()
        let v = try await srv.api().joinVoice(channelId: "9")
        #expect(v.media.token == "t")
    }

    @Test func logoutLimpaOStoreMesmoComServidorFora() async throws {
        let srv = MockServer { _ in MockResponse(transportError: URLError(.notConnectedToInternet)) }
        srv.seed()
        let api = srv.api()
        await api.logout()
        #expect(try srv.store.load(for: srv.base) == nil)
        #expect(api.authState == .unauthenticated(.loggedOut))
    }

    @Test func logoutRevogaORefreshToken() async throws {
        let hits = Counter()
        let srv = MockServer { r in hits.hit(r); return .noContent }
        srv.seed(refresh: "RX")
        await srv.api().logout()
        #expect(hits.requests.first?.path == "/v1/auth/logout" && hits.requests.first?.bodyJSON?["refreshToken"] as? String == "RX")
    }

    @Test func logoutSaiDaVozAntesDeRevogarOsTokens() async throws {
        let hits = Counter()
        let srv = MockServer { r in hits.hit(r); return .noContent }
        srv.seed(access: "AX", refresh: "RX")
        let api = srv.api()
        await api.logout(leavingVoice: "42")
        let paths = hits.requests.map(\.path)
        #expect(paths == ["/v1/channels/42/voice/leave", "/v1/auth/logout"])
        #expect(hits.requests[0].bearer == "AX")  // o leave ainda vai autenticado
        #expect(try srv.store.load(for: srv.base) == nil)
        #expect(api.authState == .unauthenticated(.loggedOut))
    }

    @Test func logoutSegueSeOLeaveDaVozFalhaOuDemora() async throws {
        let failing = MockServer { r in r.path.hasSuffix("/voice/leave") ? .error(500, "internal") : .noContent }
        failing.seed()
        await failing.api().logout(leavingVoice: "42")
        #expect(try failing.store.load(for: failing.base) == nil)

        let slow = MockServer { r in r.path.hasSuffix("/voice/leave") ? .json(200, "{}", delay: 5) : .noContent }
        slow.seed()
        let t0 = Date()
        await slow.api().logout(leavingVoice: "42", voiceTimeout: 0.2)
        #expect(Date().timeIntervalSince(t0) < 2)
        #expect(try slow.store.load(for: slow.base) == nil)
    }

    @Test func logoutComSessaoJaExpiradaNaoPendura() async throws {
        let hits = Counter()
        let srv = MockServer { r in hits.hit(r); return .noContent }  // sem tokens: o leave nem sai do cliente
        let api = srv.api()
        await api.logout(leavingVoice: "42")
        #expect(hits.count == 0)
        #expect(api.authState == .unauthenticated(.loggedOut))
    }

    @Test func baseURLComPrefixoEBarraFinal() async throws {
        let hits = Counter()
        let srv = MockServer { r in hits.hit(r); return .json(200, Sample.user) }
        srv.seed()
        let api = KurzAPI(baseURL: srv.base + "/api/", store: srv.store, session: srv.session)
        try? srv.store.save(StoredTokens(accessToken: "A", refreshToken: "R", expiresAt: Date().addingTimeInterval(900)), for: api.baseURL)
        _ = try await api.me()
        #expect(hits.requests.first?.path == "/api/v1/me")
    }
}

@Suite("Backoff e URL do gateway")
struct BackoffTests {
    @Test func cresceExponencialmenteECapa() {
        let b = Backoff(base: 1, cap: 30)
        #expect(b.delay(attempt: 0, jitter: 1) == 1)
        #expect(b.delay(attempt: 1, jitter: 1) == 2)
        #expect(b.delay(attempt: 3, jitter: 1) == 8)
        #expect(b.delay(attempt: 10, jitter: 1) == 30)
        #expect(b.delay(attempt: 1000, jitter: 1) == 30)
    }
    @Test func jitterFicaEntreMetadeEOTeto() {
        let b = Backoff(base: 1, cap: 30)
        #expect(b.delay(attempt: 3, jitter: 0) == 4)
        for _ in 0..<200 { let d = b.delay(attempt: 4); #expect(d >= 8 && d <= 16) }
        let samples = Set((0..<50).map { _ in b.delay(attempt: 4) })
        #expect(samples.count > 1)
    }
    @Test func urlDoGateway() {
        #expect(Gateway.gatewayURL("http://localhost:8080")?.absoluteString == "ws://localhost:8080/v1/gateway")
        #expect(Gateway.gatewayURL("https://k.example.com/")?.absoluteString == "wss://k.example.com/v1/gateway")
        #expect(Gateway.gatewayURL("https://k.example.com/api")?.absoluteString == "wss://k.example.com/api/v1/gateway")
    }
}

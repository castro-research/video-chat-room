import Testing
@testable import KurzKit

/// End-to-end contra um servidor Kurz REAL. Só roda com `KURZ_E2E_URL=http://localhost:PORTA` (veja README / `make test-kit`).
/// Sem a variável, os testes aparecem como SKIPPED (não como passados).
let e2eURL: String? = {
    let v = ProcessInfo.processInfo.environment["KURZ_E2E_URL"]
    return (v?.isEmpty ?? true) ? nil : v
}()
private let skipReason: Comment = "defina KURZ_E2E_URL=http://localhost:PORTA com um servidor Kurz de pé (veja KurzKit/README.md)"

private func uid() -> String { String(UUID().uuidString.replacingOccurrences(of: "-", with: "").lowercased().prefix(10)) }

private func ensure(_ cond: Bool, _ msg: String) throws {
    if !cond { Issue.record(Comment(rawValue: msg)); throw KurzError(msg) }
}

private func newAPI(_ base: String) -> KurzAPI { KurzAPI(baseURL: base, store: InMemoryTokenStore()) }

private func waitEvent(_ c: Collector<GatewayEvent>, _ what: String, timeout: TimeInterval = 6, _ match: @escaping @Sendable (GatewayEvent) -> Bool) async -> GatewayEvent? {
    await eventually(timeout: timeout, Comment(rawValue: what)) { c.all.contains(where: match) }
    return c.all.first(where: match)
}

private func expectError(_ code: String, status: Int? = nil, _ body: () async throws -> Void) async {
    do { try await body(); Issue.record("esperava erro \(code)") }
    catch let e as KurzError {
        #expect(e.code == code, "código \(e.code) (esperava \(code)): \(e.message)")
        if let status { #expect(e.status == status) }
        #expect(!e.message.isEmpty)
    } catch { Issue.record("erro inesperado \(error)") }
}

@Suite("E2E contra o servidor Go", .serialized)
struct E2ETests {
    @Test("fluxo completo: contas, guild, convite, canais, mensagens, reações, cursores, leitura, gateway",
          .enabled(if: e2eURL != nil, skipReason))
    func fluxoCompleto() async throws {
        let base = e2eURL!
        let ana = newAPI(base), bob = newAPI(base)

        // Descoberta
        let info = try await ana.wellKnown()
        #expect(info.contractMajor == 0 && info.supportsRealtime && info.registration == .open, "\(info)")

        // Contas
        let anaName = "ana_\(uid())", bobName = "bob_\(uid())"
        let sa = try await ana.register(username: anaName, password: "senha-ana-123", name: "Ana")
        let sb = try await bob.register(username: bobName, password: "senha-bob-123")
        #expect(sa.user.username == anaName && sa.user.name == "Ana" && sb.user.name == bobName && !sa.user.id.isEmpty)
        await expectError("username_taken", status: 409) { _ = try await newAPI(base).register(username: anaName, password: "outra-senha-1") }
        let me = try await ana.me()
        #expect(me.id == sa.user.id)
        let upd = try await ana.updateMe(name: "Ana Silva", subtitle: "codando", hue: 120)
        #expect(upd.name == "Ana Silva" && upd.subtitle == "codando" && upd.hue == 120)
        let relogin = try await newAPI(base).login(username: anaName, password: "senha-ana-123")
        #expect(relogin.user.id == sa.user.id)
        await expectError("unauthorized", status: 401) { _ = try await newAPI(base).login(username: anaName, password: "errada-errada") }

        // Guild e canais
        let guild = try await ana.createGuild(name: "Casa \(uid())")
        #expect(guild.ownerId == sa.user.id)
        #expect(try await ana.guilds().map(\.id).contains(guild.id))
        #expect(try await bob.guilds().isEmpty)
        await expectError("not_found", status: 404) { _ = try await bob.guild(guild.id) } // quem não é membro não vê
        var channels = try await ana.channels(guildId: guild.id)
        let geral = try #require(channels.first { $0.name == "geral" })
        #expect(geral.kind == .text)
        let roles = try await ana.roles(guildId: guild.id)
        #expect(roles.first?.everyone == true && roles.first?.id == guild.id)

        // Convite
        let invite = try await ana.createInvite(guildId: guild.id, maxUses: 5, expiresIn: 3600)
        #expect(invite.guildId == guild.id && invite.maxUses == 5 && invite.expiresAt != nil && invite.uses == 0)
        let preview = try await bob.previewInvite(code: invite.code)
        #expect(preview.guild.id == guild.id)
        await expectError("invalid_invite", status: 404) { _ = try await bob.previewInvite(code: "nao-existe") }
        // gateways antes do join do bob: ele recebe o próprio member.join? (não: não era membro). Abre depois.
        let joined = try await bob.joinInvite(code: invite.code)
        #expect(joined.id == guild.id)
        #expect(try await bob.joinInvite(code: invite.code).id == guild.id) // idempotente
        let members = try await ana.members(guildId: guild.id)
        #expect(members.total == 2 && Set(members.items.map(\.user.id)) == [sa.user.id, sb.user.id])
        let pageA = try await ana.members(guildId: guild.id, limit: 1)
        #expect(pageA.items.count == 1 && pageA.next != nil)
        let pageB = try await ana.members(guildId: guild.id, after: pageA.next, limit: 1)
        #expect(pageB.items.count == 1 && pageB.items[0].id != pageA.items[0].id)

        // Gateways
        let gwA = ana.makeGateway(), gwB = bob.makeGateway(backoff: Backoff(base: 0.6, cap: 2))
        let evA = Collector(gwA.events()), evB = Collector(gwB.events())
        await gwA.start(); await gwB.start()
        defer { Task { await gwA.stop(); await gwB.stop() } }
        guard case .ready(let readyB)? = await waitEvent(evB, "ready do bob", { if case .ready = $0 { return true } else { return false } }) else { throw KurzError("sem ready (bob)") }
        _ = await waitEvent(evA, "ready da ana", { if case .ready = $0 { return true } else { return false } })
        #expect(readyB.userId == sb.user.id && readyB.guilds.map(\.id) == [guild.id] && !readyB.sessionId.isEmpty)

        // Canais (permissões)
        await expectError("missing_permission", status: 403) { _ = try await bob.createChannel(guildId: guild.id, name: "intruso", kind: .text) }
        let cat = try await ana.createChannel(guildId: guild.id, name: "Projetos", kind: .category)
        let proj = try await ana.createChannel(guildId: guild.id, name: "projeto", kind: .text, parentId: cat.id)
        let sala = try await ana.createChannel(guildId: guild.id, name: "sala", kind: .voice)
        #expect(cat.kind == .category && proj.parentId == cat.id && sala.kind == .voice)
        let chEvt = await waitEvent(evB, "channel.create do bob") { if case .channelCreate(let c) = $0 { return c.id == proj.id } else { return false } }
        #expect(chEvt != nil)
        let renamed = try await ana.renameChannel(proj.id, name: "projeto-x")
        #expect(renamed.name == "projeto-x" && renamed.parentId == cat.id)
        let moved = try await ana.updateChannel(proj.id, parentId: .some(nil))
        #expect(moved.parentId == nil)
        channels = try await bob.channels(guildId: guild.id)
        #expect(Set(channels.map(\.id)).isSuperset(of: [geral.id, cat.id, proj.id, sala.id]))
        #expect(try await bob.channel(geral.id).id == geral.id)

        // Mensagens em tempo real
        let m1 = try await ana.sendMessage(channelId: geral.id, text: "oi bob", nonce: "nonce-1")
        #expect(m1.nonce == "nonce-1" && m1.text == "oi bob" && m1.author.id == sa.user.id)
        let gotB = await waitEvent(evB, "message.create no bob") { if case .messageCreate(let m) = $0 { return m.id == m1.id } else { return false } }
        guard case .messageCreate(let mb)? = gotB else { throw KurzError("bob não recebeu message.create") }
        #expect(mb.text == "oi bob" && mb.nonce == nil, "nonce só vai para o remetente: \(String(describing: mb.nonce))")
        let gotA = await waitEvent(evA, "message.create na ana (eco com nonce)") { if case .messageCreate(let m) = $0 { return m.id == m1.id } else { return false } }
        if case .messageCreate(let ma)? = gotA { #expect(ma.nonce == "nonce-1") }

        // Resposta, edição, permissões de edição
        let reply = try await bob.sendMessage(channelId: geral.id, text: "oi ana", replyToId: m1.id)
        #expect(reply.replyToId == m1.id)
        await expectError("forbidden", status: 403) { _ = try await bob.editMessage(channelId: geral.id, messageId: m1.id, text: "hack") }
        await expectError("missing_permission", status: 403) { try await bob.deleteMessage(channelId: geral.id, messageId: m1.id) }
        let edited = try await ana.editMessage(channelId: geral.id, messageId: m1.id, text: "oi bob (editado)")
        #expect(edited.editedAt != nil && edited.text == "oi bob (editado)")
        let upEvt = await waitEvent(evB, "message.update") { if case .messageUpdate(let u) = $0 { return u.id == m1.id } else { return false } }
        if case .messageUpdate(let u)? = upEvt { #expect(u.text == "oi bob (editado)" && u.channelId == geral.id) }

        // Reações
        try await bob.addReaction(channelId: geral.id, messageId: m1.id, emoji: "👍")
        try await bob.addReaction(channelId: geral.id, messageId: m1.id, emoji: "👍") // idempotente
        let reAdd = await waitEvent(evA, "reaction.add") { if case .reactionAdd(let r) = $0 { return r.messageId == m1.id } else { return false } }
        if case .reactionAdd(let r)? = reAdd { #expect(r.emoji == "👍" && r.userId == sb.user.id) }
        var page = try await bob.messages(channelId: geral.id)
        let withReaction = try #require(page.items.first { $0.id == m1.id })
        #expect(withReaction.reactions == [Reaction(emoji: "👍", count: 1, me: true)])
        #expect(try await ana.messages(channelId: geral.id).items.first { $0.id == m1.id }?.reactions.first?.me == false)
        try await bob.removeReaction(channelId: geral.id, messageId: m1.id, emoji: "👍")
        #expect(await waitEvent(evA, "reaction.remove") { if case .reactionRemove(let r) = $0 { return r.messageId == m1.id } else { return false } } != nil)

        // Cursores: mais 6 mensagens (total 8)
        var ids = [m1.id, reply.id]
        for i in 1...6 { ids.append(try await ana.sendMessage(channelId: geral.id, text: "msg \(i)").id) }
        page = try await bob.messages(channelId: geral.id, limit: 3)
        #expect(page.items.map(\.id) == Array(ids.suffix(3)) && page.hasMore && !page.hasNewer)
        let older = try await bob.messages(channelId: geral.id, before: page.items[0].id, limit: 3)
        #expect(older.items.map(\.id) == Array(ids[2..<5]) && older.hasMore && older.hasNewer)
        let newer = try await bob.messages(channelId: geral.id, after: ids[2], limit: 2)
        #expect(newer.items.map(\.id) == [ids[3], ids[4]] && newer.hasNewer)
        let mid = ids[4]
        let around = try await bob.messages(channelId: geral.id, around: mid, limit: 4)
        #expect(around.items.map(\.id).contains(mid) && around.items.count == 4)
        let all = try await bob.messages(channelId: geral.id, limit: 100)
        #expect(all.items.map(\.id) == ids && !all.hasMore && !all.hasNewer)
        await expectError("validation", status: 422) { _ = try await bob.messages(channelId: geral.id, before: "1", after: "2") }

        // Leitura
        var rs = try await bob.readState(channelId: geral.id)
        #expect(rs.unread == 7 && rs.lastReadMessageId == nil, "unread=\(rs.unread)") // 7 de outros autores (a resposta é dele)
        try await bob.markRead(channelId: geral.id, messageId: ids[4])
        rs = try await bob.readState(channelId: geral.id)
        #expect(rs.lastReadMessageId == ids[4] && rs.unread == 3, "unread=\(rs.unread)")
        let guildRS = try await bob.readStates(guildId: guild.id)
        #expect(guildRS.contains { $0.channelId == geral.id && $0.unread == 3 })

        // Apagar
        try await ana.deleteMessage(channelId: geral.id, messageId: ids[7])
        let delId = ids[7]
        let delEvt = await waitEvent(evB, "message.delete") { if case .messageDelete(let d) = $0 { return d.id == delId } else { return false } }
        if case .messageDelete(let d)? = delEvt { #expect(d.channelId == geral.id) }
        await expectError("not_found", status: 404) { try await ana.deleteMessage(channelId: geral.id, messageId: ids[7]) }

        // Canal apagado
        try await ana.deleteChannel(proj.id)
        #expect(await waitEvent(evB, "channel.delete") { if case .channelDelete(let c) = $0 { return c.id == proj.id } else { return false } } != nil)

        // Resume: derruba o socket do bob, ana escreve durante a queda, bob recebe tudo ao voltar com a MESMA sessão
        let sessionBefore = await gwB.currentSessionId
        let seqBefore = await gwB.currentSeq
        #expect(sessionBefore == readyB.sessionId && seqBefore > 0)
        let countBefore = evB.all.count
        await gwB.debugDropConnection()
        let during = try await ana.sendMessage(channelId: geral.id, text: "enquanto você estava fora")
        await eventually(timeout: 10, "resumed") { evB.all.dropFirst(countBefore).contains(.resumed) }
        let tail = Array(evB.all.dropFirst(countBefore))
        #expect(!tail.contains(.invalidSession) && !tail.contains { if case .ready = $0 { return true } else { return false } }, "esperava resume, não novo identify: \(tail)")
        #expect(await waitEvent(evB, "message.create perdido durante a queda") { if case .messageCreate(let m) = $0 { return m.id == during.id } else { return false } } != nil)
        #expect(await gwB.currentSessionId == sessionBefore && gwB.state == .connected)

        // member.leave chega a quem fica
        try await bob.leaveGuild(guild.id)
        let leave = await waitEvent(evA, "member.leave") { if case .memberLeave(let l) = $0 { return l.userId == sb.user.id } else { return false } }
        if case .memberLeave(let l)? = leave { #expect(l.reason == "leave") }
        await expectError("not_found", status: 404) { _ = try await bob.channels(guildId: guild.id) }
        await expectError("owner_cannot_leave", status: 409) { try await ana.leaveGuild(guild.id) }

        // Convite esgotado/expirado → ban
        let one = try await ana.createInvite(guildId: guild.id, maxUses: 1)
        let carl = newAPI(base)
        _ = try await carl.register(username: "carl_\(uid())", password: "senha-carl-123")
        _ = try await carl.joinInvite(code: one.code)
        await expectError("invite_expired", status: 410) { _ = try await bob.joinInvite(code: one.code) }

        // Logout
        await bob.logout()
        #expect(bob.authState == .unauthenticated(.loggedOut))
        await expectError("unauthenticated") { _ = try await bob.me() }
    }

    @Test("refresh rotativo de verdade: forceRefresh troca os tokens; reuso do refresh antigo derruba a sessão",
          .enabled(if: e2eURL != nil, skipReason))
    func refreshReal() async throws {
        let base = e2eURL!
        let store = InMemoryTokenStore()
        let api = KurzAPI(baseURL: base, store: store)
        _ = try await api.register(username: "ref_\(uid())", password: "senha-ref-123")
        let first = try #require(try store.load(for: api.baseURL))

        // Várias chamadas concorrentes pedindo refresh ao mesmo tempo: um só vai ao servidor
        let tokens = try await withThrowingTaskGroup(of: String.self) { g in
            for _ in 0..<8 { g.addTask { try await api.sessions.refresh(failing: first.accessToken) } }
            var out = Set<String>()
            for try await t in g { out.insert(t) }
            return out
        }
        #expect(tokens.count == 1, "todas as chamadas devem usar o mesmo token novo")
        let second = try #require(try store.load(for: api.baseURL))
        #expect(second.refreshToken != first.refreshToken)
        #expect(await api.sessions.refreshRequests == 1)
        _ = try await api.me() // o token novo funciona

        // Reuso do refresh antigo (rotativo): o servidor recusa e a UI é avisada
        try store.save(StoredTokens(accessToken: "velho", refreshToken: first.refreshToken, expiresAt: Date().addingTimeInterval(-5)), for: api.baseURL)
        do { _ = try await api.me(); Issue.record("devia falhar") }
        catch let e as KurzError { #expect(e.isUnauthenticated, "\(e)") }
        #expect(api.authState == .unauthenticated(.sessionExpired))
        #expect(try store.load(for: api.baseURL) == nil)
    }

    @Test("gateway: token inválido fecha com 4001 e o cliente para depois de uma renovação", .enabled(if: e2eURL != nil, skipReason))
    func gatewayTokenInvalido() async throws {
        let base = e2eURL!
        let store = InMemoryTokenStore()
        let api = KurzAPI(baseURL: base, store: store)
        _ = try await api.register(username: "gw_\(uid())", password: "senha-gw-1234")
        let t = try #require(try store.load(for: api.baseURL))
        // access token adulterado, mas ainda "válido" pelo relógio local
        try store.save(StoredTokens(accessToken: "lixo", refreshToken: t.refreshToken, expiresAt: Date().addingTimeInterval(900)), for: api.baseURL)
        let gw = api.makeGateway(backoff: Backoff(base: 0.05, cap: 0.1))
        let events = Collector(gw.events())
        await gw.start()
        // 4001 → refresh forçado → reconecta com token bom → ready
        let ready = await waitEvent(events, "ready depois de renovar", timeout: 8) { if case .ready = $0 { return true } else { return false } }
        #expect(ready != nil, "o gateway devia renovar o token após 4001 e conectar")
        await gw.stop()
    }

    @Test("voz: join/leave/estado (ou 501 not_supported se o servidor não tem SFU)", .enabled(if: e2eURL != nil, skipReason))
    func voz() async throws {
        let base = e2eURL!
        let ana = newAPI(base), bob = newAPI(base)
        let info = try await ana.wellKnown()
        _ = try await ana.register(username: "va_\(uid())", password: "senha-voz-123")
        let sb = try await bob.register(username: "vb_\(uid())", password: "senha-voz-123")
        let guild = try await ana.createGuild(name: "Voz \(uid())")
        let invite = try await ana.createInvite(guildId: guild.id)
        _ = try await bob.joinInvite(code: invite.code)
        let sala = try await ana.createChannel(guildId: guild.id, name: "sala", kind: .voice)
        let geral = try #require(try await ana.channels(guildId: guild.id).first { $0.kind == .text })

        print("[e2e] voz: servidor \(info.supportsVoice ? "COM" : "SEM") capability voice")
        guard info.supportsVoice else {
            await expectError("not_supported", status: 501) { _ = try await ana.joinVoice(channelId: sala.id) }
            return
        }
        let gwA = ana.makeGateway()
        let evA = Collector(gwA.events())
        await gwA.start()
        defer { Task { await gwA.stop() } }
        _ = await waitEvent(evA, "ready", { if case .ready = $0 { return true } else { return false } })

        await expectError("validation", status: 422) { _ = try await bob.joinVoice(channelId: geral.id) } // canal de texto
        let v = try await bob.joinVoice(channelId: sala.id)
        #expect(v.media.url.hasPrefix("ws") && !v.media.token.isEmpty && !v.iceServers.isEmpty, "\(v)")
        #expect(v.participants.contains { $0.user.id == sb.user.id })
        let joinEvt = await waitEvent(evA, "voice.state connected") { if case .voiceState(let s) = $0 { return s.connected && s.participant.user.id == sb.user.id } else { return false } }
        #expect(joinEvt != nil)
        #expect(try await ana.voiceParticipants(channelId: sala.id).map(\.user.id) == [sb.user.id])
        // token de mídia: JWT de 3 partes
        #expect(v.media.token.split(separator: ".").count == 3)

        let gwB = bob.makeGateway()
        let evB = Collector(gwB.events())
        await gwB.start()
        defer { Task { await gwB.stop() } }
        guard case .ready(let rb)? = await waitEvent(evB, "ready bob", { if case .ready = $0 { return true } else { return false } }) else { throw KurzError("sem ready") }
        #expect(rb.voice.contains { $0.channelId == sala.id && $0.participant.user.id == sb.user.id }, "ready.voice deve trazer o estado atual")
        try await gwB.setVoiceState(muted: true, camOn: true, sharing: false)
        let st = await waitEvent(evA, "voice.state muted+cam") { if case .voiceState(let s) = $0 { return s.connected && s.participant.muted && s.participant.camOn } else { return false } }
        #expect(st != nil)
        try await bob.leaveVoice(channelId: sala.id)
        try await bob.leaveVoice(channelId: sala.id) // idempotente
        let left = await waitEvent(evA, "voice.state left") { if case .voiceState(let s) = $0 { return !s.connected && s.participant.user.id == sb.user.id } else { return false } }
        #expect(left != nil)
        #expect(try await ana.voiceParticipants(channelId: sala.id).isEmpty)
    }
}

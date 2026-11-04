import Testing
@testable import KurzKit

@Suite("Modelos do contrato 0.6")
struct ModelsTests {
    let dec = KurzJSON.decoder()
    func decode<T: Decodable>(_ t: T.Type, _ s: String) throws -> T { try dec.decode(t, from: Data(s.utf8)) }

    @Test func user() throws {
        let u = try decode(User.self, Sample.user)
        #expect(u.id == "100" && u.username == "ana" && u.status == .online && u.hue == 210.5 && u.subtitle == "oi" && u.avatarUrl == nil)
    }

    @Test func userStatusDesconhecidoViraOffline() throws {
        #expect(try decode(User.self, #"{"id":"1","username":"abc","name":"A","status":"dnd"}"#).status == .offline)
        #expect(try decode(User.self, #"{"id":"1","username":"abc","name":"A"}"#).status == .offline)
    }

    @Test func sessionEGuildComDatasComEPreFracao() throws {
        let s = try decode(Session.self, Sample.session(access: "a", refresh: "r"))
        #expect(s.expiresIn == 900 && s.user.name == "Ana")
        let g = try decode(Guild.self, Sample.guild)
        #expect(g.ownerId == "100")
        #expect(abs(g.createdAt.timeIntervalSince1970 - 1_793_534_400.123456) < 0.001)
        let nano = try decode(Guild.self, #"{"id":"1","name":"x","ownerId":"2","createdAt":"2026-11-01T12:00:00.123456789Z"}"#)
        #expect(nano.createdAt > g.createdAt.addingTimeInterval(-1))
    }

    @Test func message() throws {
        let m = try decode(Message.self, Sample.message)
        #expect(m.id == "300" && m.channelId == "400" && m.replyToId == "299" && m.nonce == "n1" && m.editedAt != nil)
        #expect(m.reactions == [Reaction(emoji: "👍", count: 2, me: true)])
        #expect(m.author.username == "ana")
    }

    @Test func messageSemOpcionaisNemListas() throws {
        let m = try decode(Message.self, #"{"id":"1","channelId":"2","author":{"id":"3","username":"bob","name":"Bob","status":"offline"},"createdAt":"2026-11-01T12:00:01Z","text":"x"}"#)
        #expect(m.reactions.isEmpty && m.attachments.isEmpty && m.replyToId == nil && m.editedAt == nil && m.nonce == nil)
    }

    @Test func messagePage() throws {
        let p = try decode(MessagePage.self, #"{"items":[\#(Sample.message)],"hasMore":true,"hasNewer":false}"#)
        #expect(p.items.count == 1 && p.hasMore && !p.hasNewer)
    }

    @Test func channelEKindDesconhecido() throws {
        let c = try decode(Channel.self, #"{"id":"1","guildId":"2","name":"geral","kind":"text","position":0}"#)
        #expect(c.kind == .text && c.parentId == nil)
        let v = try decode(Channel.self, #"{"id":"1","guildId":"2","name":"sala","kind":"voice","parentId":"9","position":3}"#)
        #expect(v.kind == .voice && v.parentId == "9" && v.position == 3)
        #expect(try decode(Channel.self, #"{"id":"1","guildId":"2","name":"x","kind":"forum","position":0}"#).kind == .unknown)
    }

    @Test func roleEPermissoes() throws {
        let r = try decode(Role.self, #"{"id":"1","name":"mod","permissions":1031,"position":2,"everyone":false}"#)
        #expect(r.permissions.contains(.viewChannel) && r.permissions.contains(.sendMessages) && r.permissions.contains(.administrator))
        #expect(!r.permissions.contains(.connect))
        #expect(r.permissions.allows(.banMembers)) // administrator vale por tudo
        #expect(!Permissions([.viewChannel]).allows(.speak))
    }

    @Test func memberEMemberPage() throws {
        let p = try decode(MemberPage.self, #"{"items":[{"user":\#(Sample.user),"joinedAt":"2026-11-01T12:00:00Z","roleIds":["5"]}],"total":1}"#)
        #expect(p.items[0].roleIds == ["5"] && p.total == 1 && p.next == nil && p.items[0].id == "100")
    }

    @Test func inviteEPreview() throws {
        let i = try decode(Invite.self, #"{"code":"abc","guildId":"2","creatorId":"3","uses":0,"createdAt":"2026-11-01T12:00:00Z"}"#)
        #expect(i.maxUses == nil && i.expiresAt == nil)
        let p = try decode(InvitePreview.self, #"{"code":"abc","expiresAt":"2026-12-01T00:00:00Z","guild":\#(Sample.guild)}"#)
        #expect(p.guild.name == "Casa" && p.expiresAt != nil)
    }

    @Test func readState() throws {
        let r = try decode(ReadState.self, #"{"channelId":"1","unread":3}"#)
        #expect(r.lastReadMessageId == nil && r.unread == 3)
    }

    @Test func serverInfo() throws {
        let s = try decode(ServerInfo.self, #"{"contract":"0.6","name":"Kurz","registration":"open","capabilities":["text","realtime","voice"]}"#)
        #expect(s.contractMajor == 0 && s.supportsVoice && s.supportsRealtime && !s.supports("search") && s.registration == .open)
    }

    @Test func voiceSessionCompativelComOApp() throws {
        let json = #"{"media":{"url":"ws://localhost:9999/v1/media","token":"jwt"},"iceServers":[{"urls":["stun:stun.l.google.com:19302"]},{"urls":["turn:t:3478"],"username":"u","credential":"c"}],"participants":[{"user":\#(Sample.user),"muted":true,"camOn":false,"sharing":false}]}"#
        let v = try decode(VoiceSession.self, json)
        #expect(v.media.url == "ws://localhost:9999/v1/media" && v.media.token == "jwt")
        #expect(v.iceServers.count == 2 && v.iceServers[1].username == "u" && v.iceServers[1].credential == "c")
        #expect(v.participants.first?.muted == true)
        // O VoiceSession do app só tem media + iceServers; o JSON do app antigo (sem participants/iceServers) também decodifica.
        let old = try decode(VoiceSession.self, #"{"media":{"url":"ws://x","token":"t"}}"#)
        #expect(old.iceServers.isEmpty && old.participants.isEmpty)
    }

    @Test func voiceState() throws {
        let v = try decode(VoiceState.self, #"{"channelId":"9","connected":true,"participant":{"user":\#(Sample.user),"muted":false,"camOn":true,"sharing":false}}"#)
        #expect(v.connected && v.participant.camOn && v.participant.id == "100")
    }

    @Test func apiError() throws {
        let e = try decode(ApiError.self, #"{"code":"missing_permission","message":"x"}"#)
        #expect(e.code == "missing_permission")
    }

    // MARK: eventos do gateway

    @Test func eventosTipados() throws {
        func ev(_ t: String, _ d: String) -> GatewayEvent {
            GatewayEvent.decode(name: t, data: try! JSONDecoder().decode(JSONValue.self, from: Data(d.utf8)))
        }
        if case .messageCreate(let m) = ev("message.create", Sample.message) { #expect(m.text == "olá") } else { Issue.record("message.create") }
        if case .messageUpdate(let u) = ev("message.update", #"{"id":"1","channelId":"2","text":"novo","editedAt":"2026-11-01T12:00:00Z"}"#) { #expect(u.text == "novo") } else { Issue.record("message.update") }
        if case .messageDelete(let d) = ev("message.delete", #"{"id":"1","channelId":"2"}"#) { #expect(d.id == "1") } else { Issue.record("message.delete") }
        if case .reactionAdd(let r) = ev("reaction.add", #"{"channelId":"2","messageId":"1","userId":"3","emoji":"👍"}"#) { #expect(r.emoji == "👍") } else { Issue.record("reaction.add") }
        if case .memberLeave(let l) = ev("member.leave", #"{"guildId":"1","userId":"2","reason":"kick"}"#) { #expect(l.reason == "kick") } else { Issue.record("member.leave") }
        if case .channelDelete(let c) = ev("channel.delete", #"{"id":"1","guildId":"2"}"#) { #expect(c.guildId == "2") } else { Issue.record("channel.delete") }
        if case .roleDelete(let r) = ev("role.delete", #"{"guildId":"1","roleId":"2"}"#) { #expect(r.roleId == "2") } else { Issue.record("role.delete") }
        if case .guildCreate(let g) = ev("guild.create", #"{"userId":"1","guild":\#(Sample.guild)}"#) { #expect(g.guild.id == "200") } else { Issue.record("guild.create") }
        if case .channelPermissions(let p) = ev("channel.permissions", #"{"channelId":"1","guildId":"2","override":{"kind":"role","targetId":"2","allow":1,"deny":2}}"#) { #expect(p.override.deny == .sendMessages) } else { Issue.record("channel.permissions") }
        if case .memberJoin(let m) = ev("member.join", #"{"guildId":"1","userId":"100","member":{"user":\#(Sample.user),"joinedAt":"2026-11-01T12:00:00Z","roleIds":[]}}"#) { #expect(m.member.user.name == "Ana") } else { Issue.record("member.join") }
    }

    @Test func eventoDesconhecidoOuMalFormadoViraUnknown() throws {
        let d = try JSONDecoder().decode(JSONValue.self, from: Data(#"{"x":1}"#.utf8))
        #expect(GatewayEvent.decode(name: "typing.start", data: d) == .unknown(name: "typing.start", data: d))
        #expect(GatewayEvent.decode(name: "message.create", data: d) == .unknown(name: "message.create", data: d))
    }

    @Test func aplicaMessageUpdate() throws {
        let m = try decode(Message.self, Sample.message)
        let u = MessageUpdate(id: m.id, channelId: m.channelId, text: "editado", editedAt: Date())
        #expect(m.applying(u).text == "editado")
    }
}

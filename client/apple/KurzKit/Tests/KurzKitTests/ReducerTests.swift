import Testing
@testable import KurzKit

/// Fixtures: JSON → modelos do contrato (os modelos só têm init de decode).
enum Fx {
    static let dec = KurzJSON.decoder()
    static func decode<T: Decodable>(_ s: String) -> T { try! dec.decode(T.self, from: Data(s.utf8)) }

    static func user(_ id: String, _ name: String = "U") -> String { #"{"id":"\#(id)","username":"u\#(id)","name":"\#(name)","status":"online"}"# }
    static func message(_ id: String, ch: String = "c1", by: String = "2", text: String = "oi", nonce: String? = nil, reactions: String = "[]") -> Message {
        let n = nonce.map { #","nonce":"\#($0)""# } ?? ""
        return decode(#"{"id":"\#(id)","channelId":"\#(ch)","author":\#(user(by)),"createdAt":"2026-11-01T12:00:00Z","text":"\#(text)","reactions":\#(reactions)\#(n)}"#)
    }
    static func channel(_ id: String, _ name: String, kind: String = "text", parent: String? = nil, pos: Int = 0, guild: String = "g1") -> Channel {
        let p = parent.map { #","parentId":"\#($0)""# } ?? ""
        return decode(#"{"id":"\#(id)","guildId":"\#(guild)","name":"\#(name)","kind":"\#(kind)","position":\#(pos)\#(p)}"#)
    }
    static func guild(_ id: String, _ name: String = "G") -> Guild { decode(#"{"id":"\#(id)","name":"\#(name)","ownerId":"1","createdAt":"2026-11-01T12:00:00Z"}"#) }
    static func member(_ id: String, roles: [String] = []) -> Member {
        let r = roles.map { #""\#($0)""# }.joined(separator: ",")
        return decode(#"{"user":\#(user(id)),"joinedAt":"2026-11-01T12:00:00Z","roleIds":[\#(r)]}"#)
    }
    static func role(_ id: String, everyone: Bool = false) -> Role { Role(id: id, name: "x", permissions: [], position: 1, everyone: everyone) }
    static func page(_ ms: [Message], more: Bool = false) -> MessagePage { MessagePage(items: ms, hasMore: more, hasNewer: false) }
    static func part(_ id: String, muted: Bool = false) -> VoiceParticipant { decode(#"{"user":\#(user(id)),"muted":\#(muted),"camOn":false,"sharing":false}"#) }
    static func vstate(_ ch: String, _ uid: String, connected: Bool = true, muted: Bool = false) -> VoiceState {
        VoiceState(channelId: ch, connected: connected, participant: part(uid, muted: muted))
    }
    static func reaction(_ msg: String, _ uid: String, _ emoji: String = "👍", ch: String = "c1") -> ReactionEvent {
        ReactionEvent(channelId: ch, messageId: msg, userId: uid, emoji: emoji)
    }
}

@Suite("Reducers: lista de mensagens")
struct MessageListTests {
    @Test func ordenaPorIdComTamanho() {
        var l = MessageList()
        l.replace(with: Fx.page([Fx.message("100"), Fx.message("9"), Fx.message("20")]))
        #expect(l.messages.map(\.id) == ["9", "20", "100"])
        l.upsert(Fx.message("50"))
        #expect(l.messages.map(\.id) == ["9", "20", "50", "100"])
    }

    @Test func upsertIdempotente() {
        var l = MessageList()
        l.upsert(Fx.message("1", text: "a")); l.upsert(Fx.message("1", text: "b"))
        #expect(l.messages.count == 1 && l.messages[0].text == "b")
    }

    @Test func nonceTrocaAOtimistaPelaReal() {
        var l = MessageList()
        l.addPending(PendingMessage(nonce: "n1", text: "olá"))
        #expect(l.rows.count == 1)
        l.upsert(Fx.message("10", text: "olá", nonce: "n1"))
        #expect(l.pending.isEmpty && l.rows.count == 1)
        if case .sent(let m) = l.rows[0] { #expect(m.id == "10") } else { Issue.record("esperava .sent") }
        // a resposta REST com o mesmo id não duplica
        l.upsert(Fx.message("10", text: "olá", nonce: "n1"))
        #expect(l.messages.count == 1)
    }

    @Test func falhaERetentativa() {
        var l = MessageList()
        l.addPending(PendingMessage(nonce: "n1", text: "x"))
        l.failPending(nonce: "n1")
        #expect(l.pending[0].failed)
        l.retryPending(nonce: "n1")
        #expect(!l.pending[0].failed)
        l.dropPending(nonce: "n1")
        #expect(l.pending.isEmpty)
    }

    @Test func refetchMantemOtimistasAindaNaoConfirmadas() {
        var l = MessageList()
        l.addPending(PendingMessage(nonce: "a", text: "1")); l.addPending(PendingMessage(nonce: "b", text: "2"))
        l.replace(with: Fx.page([Fx.message("5", nonce: "a")]))
        #expect(l.pending.map(\.nonce) == ["b"])
    }

    @Test func paginaParaTras() {
        var l = MessageList()
        l.replace(with: Fx.page([Fx.message("30"), Fx.message("40")], more: true))
        #expect(l.hasMore && l.firstId == "30" && l.lastId == "40")
        l.prepend(Fx.page([Fx.message("10"), Fx.message("20"), Fx.message("30")], more: false))
        #expect(l.messages.map(\.id) == ["10", "20", "30", "40"] && !l.hasMore)
    }

    @Test func editaEApaga() {
        var l = MessageList()
        l.replace(with: Fx.page([Fx.message("1"), Fx.message("2")]))
        l.apply(MessageUpdate(id: "1", channelId: "c1", text: "novo", editedAt: Date()))
        #expect(l.message("1")?.text == "novo" && l.message("1")?.editedAt != nil)
        l.apply(MessageUpdate(id: "99", channelId: "c1", text: "x", editedAt: Date())) // desconhecida: ignora
        l.remove(id: "2"); l.remove(id: "2")
        #expect(l.messages.map(\.id) == ["1"])
    }

    @Test func reacoes() {
        var l = MessageList()
        l.replace(with: Fx.page([Fx.message("1")]))
        l.addReaction(Fx.reaction("1", "7"), me: "2")
        l.addReaction(Fx.reaction("1", "2"), me: "2")
        l.addReaction(Fx.reaction("1", "2"), me: "2") // repetida do próprio: não conta de novo
        #expect(l.messages[0].reactions == [Reaction(emoji: "👍", count: 2, me: true)])
        l.removeReaction(Fx.reaction("1", "2"), me: "2")
        #expect(l.messages[0].reactions == [Reaction(emoji: "👍", count: 1, me: false)])
        l.removeReaction(Fx.reaction("1", "7"), me: "2")
        #expect(l.messages[0].reactions.isEmpty)
        l.addReaction(Fx.reaction("zzz", "7"), me: "2") // mensagem não carregada
    }
}

@Suite("Reducers: canais e voz")
struct ChannelTreeTests {
    @Test func ordemSoltosCategoriasFilhos() {
        let cs = [
            Fx.channel("5", "voz-b", kind: "voice", parent: "2", pos: 1), Fx.channel("4", "texto-a", parent: "2", pos: 0),
            Fx.channel("2", "Projetos", kind: "category", pos: 1), Fx.channel("1", "geral", pos: 0),
            Fx.channel("3", "Social", kind: "category", pos: 0), Fx.channel("6", "forum", kind: "forum"),
            Fx.channel("7", "órfão", parent: "999", pos: 5),
        ]
        let s = ChannelTree.sections(cs)
        #expect(s.map { $0.category?.name ?? "-" } == ["-", "Social", "Projetos"])
        #expect(s[0].channels.map(\.name) == ["geral", "órfão"])
        #expect(s[1].channels.isEmpty)
        #expect(s[2].channels.map(\.name) == ["texto-a", "voz-b"])
        #expect(ChannelTree.firstText(cs)?.name == "geral")
    }

    @Test func rosterVozUpsertSaidaEMudancaDeCanal() {
        var r = VoiceRoster()
        r.reset([Fx.vstate("v1", "1"), Fx.vstate("v1", "2")])
        #expect(r.participants(in: "v1").count == 2)
        r.apply(Fx.vstate("v1", "2", muted: true))
        #expect(r.participants(in: "v1").count == 2 && r.participants(in: "v1")[1].muted)
        r.apply(Fx.vstate("v2", "2")) // muda de canal sem o `connected:false` antes
        #expect(r.participants(in: "v1").map(\.id) == ["1"] && r.channel(of: "2") == "v2")
        r.apply(Fx.vstate("v1", "1", connected: false)); r.apply(Fx.vstate("v1", "1", connected: false))
        #expect(r.participants(in: "v1").isEmpty && r.byChannel["v1"] == nil)
        r.set([], in: "v2")
        #expect(r.byChannel.isEmpty)
    }

    @Test func naoLidos() {
        var u = UnreadCounts()
        let cs = [Fx.channel("1", "a"), Fx.channel("2", "b")]
        u.increment("1"); u.increment("1"); u.increment("9")
        #expect(u.count("1") == 2 && u.total(in: cs) == 2)
        u.set([ReadState(channelId: "2", lastReadMessageId: nil, unread: 3)], for: cs)
        #expect(u.count("1") == 0 && u.count("2") == 3 && u.count("9") == 1)
        u.clear("2")
        #expect(u.total(in: cs) == 0)
    }
}

@Suite("Reducers: Workspace e eventos")
struct WorkspaceTests {
    func base() -> Workspace {
        var w = Workspace(meId: "1")
        w.setGuilds([Fx.guild("g1"), Fx.guild("g2")])
        w.setChannels([Fx.channel("c1", "geral"), Fx.channel("v1", "sala", kind: "voice")], guildId: "g1")
        w.setChannels([Fx.channel("c9", "outra", guild: "g2")], guildId: "g2")
        return w
    }

    @Test func mensagemDeOutroContaNaoLidaEDoProprioNao() {
        var w = base()
        w.apply(.messageCreate(Fx.message("10", by: "2")))
        w.apply(.messageCreate(Fx.message("11", by: "1")))
        #expect(w.unreadCount(channel: "c1") == 1 && w.unreadCount(guild: "g1") == 1 && w.unreadCount(guild: "g2") == 0)
        w.viewing = "c1"
        w.apply(.messageCreate(Fx.message("12", by: "2")))
        #expect(w.unreadCount(channel: "c1") == 1)
    }

    @Test func eventoRepetidoOuJaNoRestNaoDuplicaNemContaDeNovo() {
        var w = base()
        w.setMessages(Fx.page([Fx.message("10", by: "2")]), channelId: "c1")
        w.apply(.messageCreate(Fx.message("10", by: "2")))
        w.apply(.messageCreate(Fx.message("11", by: "2")))
        w.apply(.messageCreate(Fx.message("11", by: "2")))
        #expect(w.list("c1").messages.map(\.id) == ["10", "11"])
        #expect(w.unreadCount(channel: "c1") == 1) // o 10 já estava; o 11 conta uma vez (o repetido já estava na lista)
    }

    @Test func nonceDoProprioReconciliaViaGateway() {
        var w = base()
        w.setMessages(Fx.page([]), channelId: "c1")
        w.update("c1") { $0.addPending(PendingMessage(nonce: "n", text: "olá")) }
        w.apply(.messageCreate(Fx.message("10", by: "1", text: "olá", nonce: "n")))
        #expect(w.list("c1").pending.isEmpty && w.list("c1").messages.count == 1 && w.unreadCount(channel: "c1") == 0)
    }

    @Test func updateDeleteReacao() {
        var w = base()
        w.setMessages(Fx.page([Fx.message("10")]), channelId: "c1")
        w.apply(.messageUpdate(MessageUpdate(id: "10", channelId: "c1", text: "ed", editedAt: Date())))
        w.apply(.reactionAdd(Fx.reaction("10", "3")))
        #expect(w.list("c1").messages[0].text == "ed" && w.list("c1").messages[0].reactions.count == 1)
        w.apply(.messageDelete(MessageDelete(id: "10", channelId: "c1")))
        #expect(w.list("c1").messages.isEmpty)
    }

    @Test func readyEInvalidSessionPedemRefetch() {
        var w = base()
        #expect(w.apply(.invalidSession) == [.everything])
        #expect(w.apply(.resumed).isEmpty)
        #expect(w.apply(.unknown(name: "x", data: .null)).isEmpty)
    }

    @Test func canaisCriarAtualizarApagar() {
        var w = base()
        let novo = Fx.channel("c2", "novo", pos: 3)
        #expect(w.apply(.channelCreate(novo)) == [.guild("g1")])
        w.apply(.channelCreate(novo))
        #expect(w.channels["g1"]?.count == 3)
        w.apply(.channelUpdate(Fx.channel("c2", "renomeado", pos: 3)))
        #expect(w.channel("c2")?.name == "renomeado")
        w.setMessages(Fx.page([Fx.message("1", ch: "c2")]), channelId: "c2")
        w.apply(.channelDelete(ChannelDeleteEvent(id: "c2", guildId: "g1")))
        #expect(w.channel("c2") == nil && w.messages["c2"] == nil)
    }

    @Test func guildsEMembros() {
        var w = base()
        w.setMembers([Fx.member("1")], guildId: "g1")
        let join = MemberEvent(guildId: "g1", userId: "5", member: Fx.member("5"))
        w.apply(.memberJoin(join)); w.apply(.memberJoin(join))
        #expect(w.members["g1"]?.count == 2)
        w.apply(.memberJoin(MemberEvent(guildId: "g2", userId: "5", member: Fx.member("5")))) // lista não carregada: ignora
        #expect(w.members["g2"] == nil)
        w.apply(.memberLeave(MemberLeaveEvent(guildId: "g1", userId: "5", reason: "leave")))
        #expect(w.members["g1"]?.map(\.id) == ["1"])
        #expect(w.apply(.roleUpdate(RoleEvent(guildId: "g1", role: Fx.role("r")))).isEmpty) // não tenho este cargo
        w.apply(.guildUpdate(Fx.guild("g1", "Novo")))
        #expect(w.guild("g1")?.name == "Novo")
        // eu saí / fui banido: a guild some com tudo dentro
        w.setMessages(Fx.page([Fx.message("1")]), channelId: "c1")
        w.apply(.memberLeave(MemberLeaveEvent(guildId: "g1", userId: "1", reason: "kick")))
        #expect(w.guild("g1") == nil && w.channels["g1"] == nil && w.messages["c1"] == nil)
        w.apply(.guildDelete(GuildDeleteEvent(id: "g2")))
        #expect(w.guilds.isEmpty)
    }

    @Test func guildCriadaPedeCanais() {
        var w = Workspace(meId: "1")
        #expect(w.apply(.guildCreate(GuildCreateEvent(userId: "1", guild: Fx.guild("g7")))) == [.guild("g7")])
        #expect(w.guilds.map(\.id) == ["g7"])
    }

    @Test func vozPeloGateway() {
        var w = base()
        w.apply(.voiceState(Fx.vstate("v1", "2")))
        #expect(w.voice.participants(in: "v1").count == 1)
        w.apply(.memberLeave(MemberLeaveEvent(guildId: "g1", userId: "2", reason: "kick")))
        #expect(w.voice.participants(in: "v1").isEmpty)
    }
}

@Suite("Reducers: membros e cargos sem refetch desnecessário")
struct MemberRefetchTests {
    func ready() -> Workspace {
        var w = Workspace(meId: "1")
        w.setGuilds([Fx.guild("g1")])
        w.setMembers([Fx.member("1", roles: ["mine"]), Fx.member("2")], guildId: "g1")
        return w
    }

    @Test func memberUpdateDeOutroAplicaLocalSemRefetch() {
        var w = ready()
        let out = w.apply(.memberUpdate(MemberEvent(guildId: "g1", userId: "2", member: Fx.member("2", roles: ["r9"]))))
        #expect(out.isEmpty)
        #expect(w.member("2", in: "g1")?.roleIds == ["r9"])
        #expect(w.members["g1"]?.count == 2)
    }

    @Test func memberUpdateDeOutroDeMembroNovoFazUpsert() {
        var w = ready()
        #expect(w.apply(.memberUpdate(MemberEvent(guildId: "g1", userId: "7", member: Fx.member("7")))).isEmpty)
        #expect(w.members["g1"]?.map(\.id) == ["1", "2", "7"])
        // lista não carregada: nada a aplicar, nem a reler
        #expect(w.apply(.memberUpdate(MemberEvent(guildId: "g9", userId: "7", member: Fx.member("7")))).isEmpty)
        #expect(w.members["g9"] == nil)
    }

    @Test func memberUpdateDoProprioPedeRefetchEAtualizaMeusCargos() {
        var w = ready()
        let out = w.apply(.memberUpdate(MemberEvent(guildId: "g1", userId: "1", member: Fx.member("1", roles: ["a", "b"]))))
        #expect(out == [.guild("g1")])
        #expect(w.myRoles["g1"] == ["a", "b"])
        #expect(w.member("1", in: "g1")?.roleIds == ["a", "b"])
    }

    @Test func cargoAlheioNaoPedeRefetchMasMeuPede() {
        var w = ready()
        #expect(w.apply(.roleCreate(RoleEvent(guildId: "g1", role: Fx.role("novo")))).isEmpty)
        #expect(w.apply(.roleUpdate(RoleEvent(guildId: "g1", role: Fx.role("other")))).isEmpty)
        #expect(w.apply(.roleUpdate(RoleEvent(guildId: "g1", role: Fx.role("mine")))) == [.guild("g1")])
        #expect(w.apply(.roleUpdate(RoleEvent(guildId: "g1", role: Fx.role("ev", everyone: true)))) == [.guild("g1")])
    }

    @Test func cargoApagadoSaiDosMembrosEPedeRefetchSoSeEraMeu() {
        var w = ready()
        w.apply(.memberUpdate(MemberEvent(guildId: "g1", userId: "2", member: Fx.member("2", roles: ["other", "keep"]))))
        #expect(w.apply(.roleDelete(RoleDeleteEvent(guildId: "g1", roleId: "other"))).isEmpty)
        #expect(w.member("2", in: "g1")?.roleIds == ["keep"])
        #expect(w.apply(.roleDelete(RoleDeleteEvent(guildId: "g1", roleId: "mine"))) == [.guild("g1")])
        #expect(w.member("1", in: "g1")?.roleIds.isEmpty == true && w.myRoles["g1"]?.isEmpty == true)
    }

    @Test func semSaberMeusCargosSeAssumeQueAfeta() {
        var w = Workspace(meId: "1")
        w.setGuilds([Fx.guild("g1")])
        #expect(w.apply(.roleUpdate(RoleEvent(guildId: "g1", role: Fx.role("x")))) == [.guild("g1")])
    }

    @Test func portaoDeCargaDeduplica() {
        var g = LoadGate()
        let first = g.begin("g1")
        let dup = g.begin("g1")                  // em voo: o segundo pedido não carrega
        let other = g.begin("g2")                // outra guild é independente
        let forcedWhileLoading = g.begin("g1", force: true) // nem a releitura se sobrepõe
        #expect(first && !dup && other && !forcedWhileLoading)
        g.finish("g1")
        let alreadyLoaded = g.begin("g1", loaded: true)
        let reload = g.begin("g1", loaded: true, force: true) // releitura explícita
        #expect(!alreadyLoaded && reload && g.isLoading("g1"))
    }

    @Test func ecoDeReacaoDoProprioNaoContaEmDobro() {
        var l = MessageList()
        l.upsert(Fx.message("1"))
        // gateway antes do REST
        l.addReaction(Fx.reaction("1", "2"), me: "2"); l.addReaction(Fx.reaction("1", "2"), me: "2")
        #expect(l.messages[0].reactions == [Reaction(emoji: "👍", count: 1, me: true)])
        // remover: o eco e o REST local chegam em qualquer ordem
        l.removeReaction(Fx.reaction("1", "2"), me: "2"); l.removeReaction(Fx.reaction("1", "2"), me: "2")
        #expect(l.messages[0].reactions.isEmpty)
        // a de outro, com outra do próprio já presente, continua a somar
        l.addReaction(Fx.reaction("1", "2"), me: "2"); l.addReaction(Fx.reaction("1", "7"), me: "2")
        l.removeReaction(Fx.reaction("1", "2"), me: "2"); l.removeReaction(Fx.reaction("1", "2"), me: "2")
        #expect(l.messages[0].reactions == [Reaction(emoji: "👍", count: 1, me: false)])
    }
}

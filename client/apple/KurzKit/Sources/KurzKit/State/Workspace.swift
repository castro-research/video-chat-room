import Foundation

/// O que o estado local não consegue saber sozinho e pede ao REST depois de um evento.
public enum Refetch: Hashable, Sendable {
    /// `ready` ou `invalid_session`: refaça tudo (guilds, canais, não-lidos, voz, e o que está aberto).
    case everything
    /// Canais e não-lidos desta guild: mudaram as minhas permissões, os meus cargos ou a ordem dos canais. (Os membros não se relêem:
    /// os eventos `member.*` já os trazem.)
    case guild(String)
}

/// Evita cargas repetidas da mesma chave (p. ex. os membros de uma guild): uma em voo por vez e nenhuma se já está carregada.
public struct LoadGate: Equatable, Sendable {
    private var inFlight: Set<String> = []
    public init() {}

    /// true se o chamador deve carregar `key` agora (e depois chamar `finish`). `loaded` = os dados já estão no estado; `force` ignora isso
    /// (releitura), mas nunca sobrepõe uma carga em voo.
    public mutating func begin(_ key: String, loaded: Bool = false, force: Bool = false) -> Bool {
        if inFlight.contains(key) || (loaded && !force) { return false }
        inFlight.insert(key)
        return true
    }

    public mutating func finish(_ key: String) { inFlight.remove(key) }
    public func isLoading(_ key: String) -> Bool { inFlight.contains(key) }
}

/// Todo o estado de dados do utilizador autenticado, como valor puro: o app só o embrulha num ObservableObject e faz o I/O.
/// `apply(_:)` aplica um evento do gateway de forma idempotente e diz o que ainda é preciso reler.
public struct Workspace: Equatable, Sendable {
    public var meId: String
    public private(set) var guilds: [Guild] = []
    /// guildId -> canais (sem ordem; use `ChannelTree`).
    public private(set) var channels: [String: [Channel]] = [:]
    /// guildId -> membros, só das guilds cuja lista foi pedida.
    public private(set) var members: [String: [Member]] = [:]
    /// guildId -> os meus cargos (sem @everyone), conhecidos quando a minha linha de membro foi vista (lista ou evento).
    public private(set) var myRoles: [String: Set<String>] = [:]
    public private(set) var messages: [String: MessageList] = [:]
    public private(set) var unread = UnreadCounts()
    public private(set) var voice = VoiceRoster()
    /// Canal aberto com a lista no fim: mensagens novas nele não contam como não-lidas.
    public var viewing: String?

    public init(meId: String) { self.meId = meId }

    // MARK: Consultas

    public func guild(_ id: String) -> Guild? { guilds.first { $0.id == id } }
    public func channel(_ id: String) -> Channel? { channels.values.lazy.flatMap { $0 }.first { $0.id == id } }
    public func guildId(ofChannel id: String) -> String? { channel(id)?.guildId }
    public func sections(_ guildId: String) -> [ChannelSection] { ChannelTree.sections(channels[guildId] ?? []) }
    public func list(_ channelId: String) -> MessageList { messages[channelId] ?? MessageList() }
    public func unreadCount(channel id: String) -> Int { unread.count(id) }
    public func unreadCount(guild id: String) -> Int { unread.total(in: channels[id] ?? []) }
    public func member(_ userId: String, in guildId: String) -> Member? { members[guildId]?.first { $0.id == userId } }

    // MARK: Cargas do REST

    public mutating func setGuilds(_ list: [Guild]) {
        let keep = Set(list.map(\.id))
        for g in guilds where !keep.contains(g.id) { drop(guild: g.id) }
        guilds = list
    }

    public mutating func setChannels(_ list: [Channel], guildId: String) {
        let old = Set((channels[guildId] ?? []).map(\.id)), new = Set(list.map(\.id))
        for id in old.subtracting(new) { drop(channel: id) }
        channels[guildId] = list
    }

    public mutating func setReadStates(_ states: [ReadState], guildId: String) {
        unread.set(states, for: channels[guildId] ?? [])
    }

    public mutating func setMembers(_ list: [Member], guildId: String) {
        members[guildId] = list
        if let me = list.first(where: { $0.id == meId }) { myRoles[guildId] = Set(me.roleIds) }
    }
    public mutating func setMessages(_ page: MessagePage, channelId: String) { messages[channelId, default: MessageList()].replace(with: page) }
    public mutating func prependMessages(_ page: MessagePage, channelId: String) { messages[channelId, default: MessageList()].prepend(page) }
    public mutating func setVoice(_ participants: [VoiceParticipant], channelId: String) { voice.set(participants, in: channelId) }
    public mutating func clearUnread(_ channelId: String) { unread.clear(channelId) }

    public mutating func update(_ channelId: String, _ body: (inout MessageList) -> Void) {
        body(&messages[channelId, default: MessageList()])
    }

    // MARK: Eventos

    @discardableResult
    public mutating func apply(_ event: GatewayEvent) -> [Refetch] {
        switch event {
        case .ready(let r):
            voice.reset(r.voice)
            return [.everything]
        case .invalidSession:
            return [.everything]
        case .resumed, .unknown:
            return []

        case .messageCreate(let m):
            // Já na lista = o REST ou um evento anterior já a trouxe; não conta duas vezes.
            let known = messages[m.channelId]?.contains(m.id) ?? false
            if !known && m.author.id != meId && viewing != m.channelId { unread.increment(m.channelId) }
            if messages[m.channelId] != nil { messages[m.channelId]!.upsert(m) }
        case .messageUpdate(let u): messages[u.channelId]?.apply(u)
        case .messageDelete(let d): messages[d.channelId]?.remove(id: d.id)
        case .reactionAdd(let e): messages[e.channelId]?.addReaction(e, me: meId)
        case .reactionRemove(let e): messages[e.channelId]?.removeReaction(e, me: meId)

        case .guildCreate(let e):
            upsert(e.guild)
            return [.guild(e.guild.id)]
        case .guildUpdate(let g): upsert(g)
        case .guildDelete(let d): drop(guild: d.id)

        case .memberJoin(let e):
            if members[e.guildId] != nil { upsert(e.member, guildId: e.guildId) }
            if e.member.id == meId { myRoles[e.guildId] = Set(e.member.roleIds) }
        case .memberUpdate(let e):
            // O evento traz o Member inteiro: aplica-se na hora. Só os MEUS cargos mudam o que eu vejo (canais, permissões) e pedem releitura.
            if members[e.guildId] != nil { upsert(e.member, guildId: e.guildId) }
            if e.member.id == meId || e.userId == meId {
                myRoles[e.guildId] = Set(e.member.roleIds)
                return [.guild(e.guildId)]
            }
        case .memberLeave(let e):
            if e.userId == meId { drop(guild: e.guildId); return [] }
            members[e.guildId]?.removeAll { $0.id == e.userId }
            voice.removeUser(e.userId, in: Set((channels[e.guildId] ?? []).map(\.id)))

        case .roleCreate: break  // um cargo novo ainda não tem membros: não muda nada para ninguém
        case .roleUpdate(let e):
            if affectsMe(role: e.role.id, everyone: e.role.everyone, guildId: e.guildId) { return [.guild(e.guildId)] }
        case .roleDelete(let e):
            let mine = affectsMe(role: e.roleId, everyone: false, guildId: e.guildId)
            if let n = members[e.guildId]?.count { for i in 0..<n { members[e.guildId]![i].roleIds.removeAll { $0 == e.roleId } } }
            myRoles[e.guildId]?.remove(e.roleId)
            if mine { return [.guild(e.guildId)] }
        case .channelPermissions(let e): return [.guild(e.guildId)]

        case .channelCreate(let c), .channelUpdate(let c):
            ChannelTree.upsert(c, in: &channels[c.guildId, default: []])
            return [.guild(c.guildId)]
        case .channelDelete(let d):
            channels[d.guildId]?.removeAll { $0.id == d.id }
            drop(channel: d.id)

        case .voiceState(let s): voice.apply(s)
        }
        return []
    }

    // MARK: Internos

    private mutating func upsert(_ g: Guild) {
        if let i = guilds.firstIndex(where: { $0.id == g.id }) { guilds[i] = g } else { guilds.append(g) }
    }

    /// O cargo muda as minhas permissões? @everyone sempre; os outros só se eu o tenho. Sem saber os meus cargos, assume que sim.
    private func affectsMe(role id: String, everyone: Bool, guildId: String) -> Bool {
        guard !everyone, let mine = myRoles[guildId] else { return true }
        return mine.contains(id)
    }

    private mutating func upsert(_ m: Member, guildId: String) {
        if let i = members[guildId]?.firstIndex(where: { $0.id == m.id }) { members[guildId]![i] = m } else { members[guildId, default: []].append(m) }
    }

    private mutating func drop(channel id: String) {
        messages[id] = nil; unread.clear(id); voice.removeChannel(id)
        if viewing == id { viewing = nil }
    }

    private mutating func drop(guild id: String) {
        for c in channels[id] ?? [] { drop(channel: c.id) }
        channels[id] = nil; members[id] = nil; myRoles[id] = nil
        guilds.removeAll { $0.id == id }
    }
}

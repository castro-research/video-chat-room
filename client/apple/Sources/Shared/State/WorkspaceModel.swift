import Foundation
import KurzKit

/// Os dados do utilizador autenticado num servidor: guilds, canais, membros, mensagens, não-lidos e voz. A lógica (reducers) vive no
/// KurzKit (`Workspace`); aqui só se faz o I/O (REST + gateway), a seleção e os estados de UI. Tudo reage ao gateway.
@MainActor
final class WorkspaceModel: ObservableObject {
    let api: KurzAPI
    let info: ServerInfo?
    let meId: String

    @Published private(set) var ws: Workspace
    @Published private(set) var selectedGuildId: String?
    /// O canal de texto aberto.
    @Published private(set) var selectedChannelId: String?
    @Published private(set) var connection: ConnectionState = .connecting
    @Published private(set) var loadingGuilds = true
    @Published private(set) var loadingMessages = false
    @Published private(set) var loadingOlder = false
    @Published var replyTo: Message?
    @Published var editing: Message?
    @Published var notice: String?

    private var gateway: Gateway?
    private var tasks: [Task<Void, Never>] = []
    private var refetching = false
    private var refetchAgain = false
    private var lastMarked: [String: String] = [:]
    /// Uma carga de membros por guild de cada vez (selectGuild, a lista aberta e a releitura geral pedem a mesma).
    private var memberLoads = LoadGate()

    init(api: KurzAPI, me: User, info: ServerInfo?) {
        self.api = api; self.info = info; self.meId = me.id
        ws = Workspace(meId: me.id)
    }

    var supportsVoice: Bool { info?.supportsVoice ?? false }
    /// Sem `realtime` não há gateway: os dados só se atualizam ao abrir/trocar.
    var usesGateway: Bool { info?.supportsRealtime ?? true }
    var selectedGuild: Guild? { selectedGuildId.flatMap(ws.guild) }
    var selectedChannel: Channel? { selectedChannelId.flatMap(ws.channel) }
    var sections: [ChannelSection] { selectedGuildId.map(ws.sections) ?? [] }
    var members: [Member] { (selectedGuildId.flatMap { ws.members[$0] } ?? []).sorted { $0.user.name.localizedCaseInsensitiveCompare($1.user.name) == .orderedAscending } }
    var messages: MessageList { selectedChannelId.map(ws.list) ?? MessageList() }

    // MARK: Ciclo de vida

    func start() {
        guard tasks.isEmpty else { return }
        guard usesGateway else {
            tasks = [Task { await refetchEverything() }]
            return
        }
        let gw = api.makeGateway()
        gateway = gw
        // os streams assinam-se ANTES do start (senão perde-se o primeiro ready)
        let events = gw.events(), states = gw.connectionStates()
        tasks = [
            Task { [weak self] in for await e in events { self?.handle(e) } },
            Task { [weak self] in for await s in states { self?.connection = s } },
        ]
        Task { await gw.start() }
    }

    func stop() {
        tasks.forEach { $0.cancel() }
        tasks = []
        let gw = gateway
        gateway = nil
        Task { await gw?.stop() }
    }

    func setVoiceState(muted: Bool, camOn: Bool, sharing: Bool) {
        guard let gw = gateway else { return }
        Task { try? await gw.setVoiceState(muted: muted, camOn: camOn, sharing: sharing) }
    }

    // MARK: Eventos

    private func handle(_ event: GatewayEvent) {
        let refetch = ws.apply(event)
        for r in refetch {
            switch r {
            case .everything: Task { await refetchEverything() }
            case .guild(let id): Task { await refetchGuild(id) }
            }
        }
        if case .messageCreate(let m) = event, m.channelId == ws.viewing, m.author.id != meId { markRead() }
        if case .guildCreate(let e) = event, e.userId == meId { selectGuild(e.guild.id) }
        fixSelection()
    }

    /// Guild ou canal que sumiram (saí, apagaram, perdi a permissão): escolhe outro.
    private func fixSelection() {
        if let g = selectedGuildId, ws.guild(g) == nil { selectedGuildId = nil; selectedChannelId = nil; ws.viewing = nil }
        if selectedGuildId == nil, let first = ws.guilds.first { selectGuild(first.id) }
        if let c = selectedChannelId, ws.channel(c) == nil { selectedChannelId = nil; ws.viewing = nil }
    }

    // MARK: Cargas do REST

    /// Depois de `ready` (a primeira vez e a cada reconexão sem resume): relê tudo e deixa os eventos seguintes aplicarem-se por cima.
    func refetchEverything() async {
        if refetching { refetchAgain = true; return }
        refetching = true
        defer { refetching = false }
        repeat {
            refetchAgain = false
            do {
                let guilds = try await api.guilds()
                ws.setGuilds(guilds)
                loadingGuilds = false
                await withTaskGroup(of: Void.self) { group in
                    // depois de `ready` os membros já carregados (ou da guild aberta) podem ter perdido eventos: relê-os
                    for g in guilds { group.addTask { await self.loadGuild(g.id, reloadMembers: true) } }
                }
                fixSelection()
                if let c = selectedChannelId { await loadMessages(c) }
            } catch {
                loadingGuilds = false
                report(error)
            }
        } while refetchAgain
    }

    func refetchGuild(_ id: String) async {
        await loadGuild(id)
        fixSelection()
    }

    /// Canais e não-lidos; com [reloadMembers] relê também os membros da guild aberta ou já carregados (refetch por evento não os relê).
    private func loadGuild(_ id: String, reloadMembers: Bool = false) async {
        do {
            async let channels = api.channels(guildId: id)
            async let reads = api.readStates(guildId: id)
            let (cs, rs) = try await (channels, reads)
            ws.setChannels(cs, guildId: id)
            ws.setReadStates(rs, guildId: id)
            if reloadMembers && (id == selectedGuildId || ws.members[id] != nil) { await loadMembers(id, force: true) }
        } catch {
            report(error)
        }
    }

    /// Carrega os membros de [guildId] se ainda não estão (nem estão a ser) carregados; [force] relê os já carregados.
    func loadMembers(_ guildId: String, force: Bool = false) async {
        guard memberLoads.begin(guildId, loaded: ws.members[guildId] != nil, force: force) else { return }
        defer { memberLoads.finish(guildId) }
        do {
            var all: [Member] = []
            var after: String?
            repeat {
                let page = try await api.members(guildId: guildId, after: after, limit: 100)
                all += page.items
                after = page.next
            } while after != nil && all.count < 2000
            ws.setMembers(all, guildId: guildId)
        } catch {
            report(error)
        }
    }

    // MARK: Seleção

    func selectGuild(_ id: String, openFirstText: Bool = false) {
        guard selectedGuildId != id else { return }
        selectedGuildId = id
        selectedChannelId = nil; ws.viewing = nil
        replyTo = nil; editing = nil
        Task { await loadMembers(id) }
        if openFirstText, let c = ChannelTree.firstText(ws.channels[id] ?? []) { openChannel(c.id) }
    }

    func openChannel(_ id: String) {
        guard let c = ws.channel(id), c.kind == .text else { return }
        if selectedChannelId != id { replyTo = nil; editing = nil }
        selectedGuildId = c.guildId
        selectedChannelId = id
        ws.viewing = id
        Task { await loadMessages(id) }
    }

    /// iPhone: voltou à lista; já não se está a ver o canal.
    func closeChannel() {
        selectedChannelId = nil; ws.viewing = nil
        replyTo = nil; editing = nil
    }

    /// A lista está no fim (ou não): só no fim as mensagens novas contam como lidas.
    func setAtBottom(_ atBottom: Bool) {
        ws.viewing = atBottom ? selectedChannelId : nil
        if atBottom { markRead() }
    }

    // MARK: Mensagens

    func loadMessages(_ channelId: String) async {
        loadingMessages = true
        defer { loadingMessages = false }
        do {
            let page = try await api.messages(channelId: channelId, limit: 50)
            ws.setMessages(page, channelId: channelId)
            if channelId == ws.viewing { markRead() }
        } catch {
            report(error)
        }
    }

    /// Rolou para cima até ao início: carrega as mais antigas.
    func loadOlder() async {
        guard let ch = selectedChannelId, !loadingOlder else { return }
        let list = ws.list(ch)
        guard list.hasMore, let first = list.firstId else { return }
        loadingOlder = true
        defer { loadingOlder = false }
        do {
            ws.prependMessages(try await api.messages(channelId: ch, before: first, limit: 50), channelId: ch)
        } catch {
            report(error)
        }
    }

    func markRead() {
        guard let ch = ws.viewing, let last = ws.list(ch).lastId, lastMarked[ch] != last else { return }
        lastMarked[ch] = last
        ws.clearUnread(ch)
        Task { do { try await api.markRead(channelId: ch, messageId: last) } catch { lastMarked[ch] = nil } }
    }

    /// Envio otimista: a mensagem aparece já como "a enviar" e é trocada pela real (mesmo `nonce`) quando volta.
    func send(_ text: String) {
        let t = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let ch = selectedChannelId, !t.isEmpty else { return }
        let p = PendingMessage(nonce: UUID().uuidString, text: t, replyToId: replyTo?.id)
        replyTo = nil
        ws.update(ch) { $0.addPending(p) }
        deliver(p, in: ch)
    }

    func retry(_ p: PendingMessage) {
        guard let ch = selectedChannelId else { return }
        ws.update(ch) { $0.retryPending(nonce: p.nonce) }
        deliver(p, in: ch)
    }

    func discard(_ p: PendingMessage) {
        guard let ch = selectedChannelId else { return }
        ws.update(ch) { $0.dropPending(nonce: p.nonce) }
    }

    private func deliver(_ p: PendingMessage, in ch: String) {
        Task {
            do {
                let m = try await api.sendMessage(channelId: ch, text: p.text, replyToId: p.replyToId, nonce: p.nonce)
                ws.update(ch) { $0.upsert(m); $0.dropPending(nonce: p.nonce) }
            } catch {
                ws.update(ch) { $0.failPending(nonce: p.nonce) }
                report(error)
            }
        }
    }

    func edit(_ m: Message, text: String) {
        let t = text.trimmingCharacters(in: .whitespacesAndNewlines)
        editing = nil
        guard !t.isEmpty, t != m.text else { return }
        Task {
            do {
                let updated = try await api.editMessage(channelId: m.channelId, messageId: m.id, text: t)
                ws.update(m.channelId) { $0.upsert(updated) }
            } catch { report(error) }
        }
    }

    func delete(_ m: Message) {
        Task {
            do {
                try await api.deleteMessage(channelId: m.channelId, messageId: m.id)
                ws.update(m.channelId) { $0.remove(id: m.id) }
            } catch { report(error) }
        }
    }

    /// Liga/desliga a reação própria. O evento do gateway chega a seguir; o reducer não conta a do próprio duas vezes.
    func toggleReaction(_ m: Message, emoji: String) {
        let mine = m.reactions.first { $0.emoji == emoji }?.me ?? false
        let e = ReactionEvent(channelId: m.channelId, messageId: m.id, userId: meId, emoji: emoji)
        Task {
            do {
                if mine { try await api.removeReaction(channelId: m.channelId, messageId: m.id, emoji: emoji) }
                else { try await api.addReaction(channelId: m.channelId, messageId: m.id, emoji: emoji) }
                ws.update(m.channelId) { if mine { $0.removeReaction(e, me: meId) } else { $0.addReaction(e, me: meId) } }
            } catch { report(error) }
        }
    }

    // MARK: Guilds, canais, convites

    func createGuild(_ name: String) async -> Bool {
        do {
            let g = try await api.createGuild(name: name)
            ws.apply(.guildCreate(GuildCreateEvent(userId: meId, guild: g)))
            await loadGuild(g.id)
            selectGuild(g.id, openFirstText: true)
            return true
        } catch { report(error); return false }
    }

    /// Entra com o código de um convite (ou o link que o contém); false (com aviso) se falhou.
    func join(code: String) async -> Bool {
        let c = code.trimmingCharacters(in: .whitespacesAndNewlines).split(separator: "/").last.map(String.init) ?? ""
        guard !c.isEmpty else { notice = "Escreve o código do convite."; return false }
        do {
            let g = try await api.joinInvite(code: c)
            ws.apply(.guildCreate(GuildCreateEvent(userId: meId, guild: g)))
            await loadGuild(g.id)
            selectGuild(g.id, openFirstText: true)
            return true
        } catch { report(error); return false }
    }

    func createChannel(name: String, kind: ChannelKind, parentId: String?) async -> Bool {
        guard let g = selectedGuildId else { return false }
        do {
            let c = try await api.createChannel(guildId: g, name: name, kind: kind, parentId: parentId)
            ws.apply(.channelCreate(c))
            return true
        } catch { report(error); return false }
    }

    func createInvite(maxUses: Int?, expiresIn: Int?) async -> Invite? {
        guard let g = selectedGuildId else { return nil }
        do { return try await api.createInvite(guildId: g, maxUses: maxUses, expiresIn: expiresIn) } catch { report(error); return nil }
    }

    func leaveSelectedGuild() async {
        guard let g = selectedGuildId else { return }
        do {
            try await api.leaveGuild(g)
            ws.apply(.memberLeave(MemberLeaveEvent(guildId: g, userId: meId, reason: "leave")))
            fixSelection()
        } catch { report(error) }
    }

    // MARK: Erros

    /// Avisos ao utilizador. A sessão expirada não passa por aqui: o AuthModel volta ao ecrã de entrada.
    private func report(_ error: Error) {
        if error is CancellationError { return }
        if let e = error as? KurzError, e.isUnauthenticated { return }
        notice = error.localizedDescription
    }
}

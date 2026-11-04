import KurzKit
import SwiftUI

/// Lista de canais da comunidade aberta: categorias colapsáveis, texto com não-lidos, voz com quem lá está, e em baixo o utilizador.
/// No iPhone também troca de comunidade (menu do cabeçalho); no Mac isso é a GuildBar.
struct ChannelListView: View {
    @EnvironmentObject var app: AppModel
    @EnvironmentObject var wm: WorkspaceModel
    @Binding var sheet: AppSheet?
    /// iPhone: abre o canal de texto (empurra a vista do canal na pilha).
    var onOpenText: (() -> Void)?
    /// iPhone: a sala de voz ocupa o ecrã todo; a linha da sala ligada (e o botão da barra) voltam a mostrá-la.
    var onShowRoom: (() -> Void)?
    @State private var collapsed = Set<String>()

    var body: some View {
        VStack(spacing: 0) {
            header
            ConnectionBanner(state: wm.usesGateway ? wm.connection : .connected)
            if let n = app.notice { NoticeBar(text: n) { app.notice = nil } }
            if let n = app.session.notice { NoticeBar(text: n) { app.session.notice = nil } }
            if let n = wm.notice { NoticeBar(text: n) { wm.notice = nil } }
            channels
            if app.inVoice { connectedBar }
            Divider().overlay(P.border)
            footer
        }
        .background(P.sidebar)
    }

    // MARK: Cabeçalho

    private func item(_ title: String, _ icon: String, destructive: Bool = false, _ action: @escaping () -> Void) -> some View {
        Button(role: destructive ? .destructive : nil, action: action) { Label(title, systemImage: icon) }
    }

    private var header: some View {
        Menu {
#if os(iOS)
            Section("Comunidades") {
                ForEach(wm.ws.guilds) { g in
                    Button { wm.selectGuild(g.id) } label: {
                        if g.id == wm.selectedGuildId { Label(g.name, systemImage: "checkmark") } else { Text(g.name) }
                    }
                }
            }
            item("Criar comunidade", "plus") { sheet = .createGuild }
            item("Entrar com convite", "link") { sheet = .joinGuild }
            item("Servidores", "server.rack") { sheet = .servers }
#endif
            if wm.selectedGuild != nil {
                Section(wm.selectedGuild?.name ?? "") {
                    item("Convidar pessoas", "person.badge.plus") { sheet = .invite }
                    item("Criar canal", "number") { sheet = .createChannel(parent: nil) }
#if os(iOS)
                    item("Membros", "person.2") { sheet = .members }
#endif
                    item("Sair da comunidade", "rectangle.portrait.and.arrow.right", destructive: true) { Task { await wm.leaveSelectedGuild() } }
                }
            }
        } label: {
            HStack(spacing: 10) {
#if os(iOS)
                if let g = wm.selectedGuild { Avatar(name: g.name, hue: g.avatarHue, size: 32, rounded: 8) }
#endif
                Text(wm.selectedGuild?.name ?? (wm.loadingGuilds ? "A carregar…" : app.servers.label))
                    .font(.system(size: 15, weight: .semibold)).foregroundColor(P.text).lineLimit(1)
                Image(systemName: "chevron.down").font(.system(size: 11)).foregroundColor(P.textMuted)
                Spacer()
            }.padding(.horizontal, 16).frame(height: 56).contentShape(Rectangle())
        }
        .menuStyle(.button).buttonStyle(.plain).menuIndicator(.hidden)
    }

    // MARK: Canais

    @ViewBuilder private var channels: some View {
        let sections = wm.sections
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 2) {
                if wm.ws.guilds.isEmpty && !wm.loadingGuilds {
                    emptyGuilds
                } else {
                    ForEach(sections) { section in
                        if let cat = section.category {
                            categoryHeader(cat)
                        }
                        if section.category.map({ !collapsed.contains($0.id) }) ?? true {
                            ForEach(section.channels) { ch in
                                if ch.kind == .voice { voiceRow(ch) } else { textRow(ch) }
                            }
                        }
                    }
                }
            }.padding(8)
        }
    }

    private var emptyGuilds: some View {
        VStack(spacing: 10) {
            Text("Ainda não estás em nenhuma comunidade.").font(.system(size: 13)).foregroundColor(P.textMuted).multilineTextAlignment(.center)
            Button("Criar comunidade") { sheet = .createGuild }
            Button("Entrar com convite") { sheet = .joinGuild }
        }.frame(maxWidth: .infinity).padding(.top, 24)
    }

    private func categoryHeader(_ cat: Channel) -> some View {
        let open = !collapsed.contains(cat.id)
        return HStack(spacing: 4) {
            Button {
                if open { collapsed.insert(cat.id) } else { collapsed.remove(cat.id) }
            } label: {
                HStack(spacing: 4) {
                    Image(systemName: open ? "chevron.down" : "chevron.right").font(.system(size: 9, weight: .bold))
                    Text(cat.name.uppercased()).font(.system(size: 11, weight: .semibold)).lineLimit(1)
                    Spacer()
                }.foregroundColor(P.textMuted).contentShape(Rectangle())
            }.buttonStyle(.plain)
            Button { sheet = .createChannel(parent: cat) } label: {
                Image(systemName: "plus").font(.system(size: 11)).foregroundColor(P.textMuted).frame(width: 22, height: 22)
            }.buttonStyle(.plain).help("Criar canal em \(cat.name)")
        }.padding(.horizontal, 6).padding(.top, 10).padding(.bottom, 2)
    }

    private func textRow(_ ch: Channel) -> some View {
        let unread = wm.ws.unreadCount(channel: ch.id)
        let on = wm.selectedChannelId == ch.id && !app.showingVoice
        return Button {
            app.showingVoice = false
            wm.openChannel(ch.id)
            onOpenText?()
        } label: {
            HStack(spacing: 8) {
                Image(systemName: "number").font(.system(size: 12)).foregroundColor(on || unread > 0 ? P.text : P.textMuted)
                Text(ch.name).font(.system(size: 13, weight: unread > 0 ? .bold : .regular)).foregroundColor(on || unread > 0 ? P.text : P.textMuted).lineLimit(1)
                Spacer()
                if unread > 0 { UnreadBadge(count: unread) }
            }.padding(.horizontal, 8).frame(height: 32).hoverRow(selected: on)
        }.buttonStyle(.plain)
    }

    private func voiceRow(_ ch: Channel) -> some View {
        let connected = app.voiceChannel?.id == ch.id
        let people = app.people(in: ch.id)
        let enabled = wm.supportsVoice
        return VStack(alignment: .leading, spacing: 0) {
            Button {
                if connected, let show = onShowRoom { show() } else { app.joinVoice(ch) }
            } label: {
                HStack(spacing: 8) {
                    Image(systemName: "speaker.wave.2").font(.system(size: 13)).foregroundColor(connected ? P.text : P.textMuted)
                    Text(ch.name).font(.system(size: 13)).foregroundColor(connected ? P.text : P.textMuted).lineLimit(1)
                    Spacer()
                    if !enabled { Text("sem voz").font(.system(size: 11)).foregroundColor(P.textFaint) }
                    else if !people.isEmpty { Text("\(people.count)").font(.system(size: 11)).foregroundColor(P.textFaint) }
                }.padding(.horizontal, 8).frame(height: 32).hoverRow(selected: connected && app.showingVoice)
            }.buttonStyle(.plain).disabled(!enabled).opacity(enabled ? 1 : 0.5)
            if !enabled {
                Text("Este servidor não tem voz.").font(.system(size: 11)).foregroundColor(P.textFaint).padding(.leading, 28).padding(.bottom, 2)
            }
            ForEach(people) { pt in
                HStack(spacing: 8) {
                    Avatar(name: pt.user.name, hue: pt.user.avatarHue, size: 20)
                    Text(pt.user.name).font(.system(size: 12)).foregroundColor(P.textMuted).lineLimit(1)
                    Spacer()
                    if pt.muted { Image(systemName: "mic.slash").font(.system(size: 10)).foregroundColor(P.textFaint) }
                    if pt.camOn { Image(systemName: "video").font(.system(size: 10)).foregroundColor(P.textFaint) }
                    if pt.sharing { Image(systemName: "rectangle.on.rectangle").font(.system(size: 10)).foregroundColor(P.textFaint) }
                }.padding(.leading, 28).padding(.trailing, 8).frame(height: 26)
            }
        }
    }

    // MARK: Voz ligada e rodapé

    private var connectedBar: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text((app.session.reconnecting ? "A reconectar · " : "Voz ligada · ") + (app.voiceChannel?.name ?? ""))
                .font(.system(size: 12, weight: .semibold)).foregroundColor(app.session.reconnecting ? P.idle : P.online).lineLimit(1)
            HStack(spacing: 16) {
                Button("Ver sala") { if let show = onShowRoom { show() } else { app.showingVoice = true } }.buttonStyle(.plain).font(.system(size: 12)).foregroundColor(P.text)
                Button("Sair") { app.leaveVoice() }.buttonStyle(.plain).font(.system(size: 12)).foregroundColor(P.danger)
            }
        }.frame(maxWidth: .infinity, alignment: .leading).padding(10).background(P.input, in: RoundedRectangle(cornerRadius: 8)).padding(8)
    }

    private var footer: some View {
        HStack(spacing: 10) {
            Button { sheet = .profile } label: {
                HStack(spacing: 10) {
                    Avatar(name: app.meName, hue: app.auth.user?.avatarHue ?? 0, size: 36)
                    Text(app.meName).font(.system(size: 13, weight: .semibold)).foregroundColor(P.text).lineLimit(1)
                    Spacer()
                }.contentShape(Rectangle())
            }.buttonStyle(.plain).help("Perfil")
            if app.inVoice {
                Button { app.setMuted(!app.muted) } label: {
                    Image(systemName: app.muted ? "mic.slash" : "mic").font(.system(size: 15)).foregroundColor(app.muted ? P.danger : P.textMuted).frame(width: 32, height: 32)
                }.buttonStyle(.plain).help("Microfone")
            }
        }.padding(.horizontal, 16).frame(height: 60)
    }
}

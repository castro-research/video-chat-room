import KurzKit
import SwiftUI

#if os(macOS)

/// Barra de comunidades (Mac): um avatar por guild, com o matiz e os não-lidos; em baixo, criar/entrar e servidores.
struct GuildBar: View {
    @EnvironmentObject var app: AppModel
    @EnvironmentObject var wm: WorkspaceModel
    @Binding var sheet: AppSheet?

    var body: some View {
        VStack(spacing: 8) {
            ScrollView {
                VStack(spacing: 8) {
                    ForEach(wm.ws.guilds) { g in
                        let selected = g.id == wm.selectedGuildId
                        let unread = wm.ws.unreadCount(guild: g.id)
                        Button { wm.selectGuild(g.id, openFirstText: true); app.showingVoice = false } label: {
                            Avatar(name: g.name, hue: g.avatarHue, size: 44, rounded: selected ? 14 : 22)
                                .overlay(alignment: .topTrailing) { if unread > 0 && !selected { UnreadBadge(count: unread).offset(x: 6, y: -4) } }
                                .overlay(RoundedRectangle(cornerRadius: selected ? 14 : 22).stroke(selected ? P.accent : .clear, lineWidth: 2))
                        }.buttonStyle(.plain).help(g.name).accessibilityLabel(g.name)
                    }
                }.padding(.top, 10).frame(maxWidth: .infinity)
            }
            Menu {
                Button("Criar comunidade") { sheet = .createGuild }
                Button("Entrar com convite") { sheet = .joinGuild }
            } label: {
                Image(systemName: "plus").font(.system(size: 16)).foregroundColor(P.online).frame(width: 44, height: 44).background(P.input, in: Circle())
            }.menuStyle(.borderlessButton).menuIndicator(.hidden).fixedSize().help("Criar ou entrar numa comunidade")
            Button { sheet = .servers } label: {
                Avatar(name: app.servers.label, hue: app.servers.hue, size: 32, rounded: 8)
                    .overlay(alignment: .bottomTrailing) { HealthDot(ok: app.servers.health[app.servers.selected]).padding(2).background(P.sidebar, in: Circle()).offset(x: 3, y: 3) }
            }.buttonStyle(.plain).help("Servidor: \(app.servers.label)").padding(.bottom, 10)
        }
        .frame(width: 68).background(P.sidebar.opacity(0.6))
    }
}
#endif

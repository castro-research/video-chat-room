import SwiftUI

/// Barra lateral: o servidor ligado, as suas salas com quem lá está, e o utilizador com o botão do microfone.
struct SidebarView: View {
    @EnvironmentObject var app: AppModel
    @Binding var serversOpen: Bool
    @Binding var createOpen: Bool
    /// iPhone: a sala ocupa o ecrã todo; a linha da sala ligada (e o botão da barra) voltam a mostrá-la.
    var onShowRoom: (() -> Void)?

    var body: some View {
        VStack(spacing: 0) {
            header
            if let n = app.notice { NoticeBar(text: n) { app.notice = nil } }
            if let n = app.session.notice { NoticeBar(text: n) { app.session.notice = nil } }
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 4) {
                    HStack {
                        Text("Salas").font(.system(size: 13, weight: .semibold)).foregroundColor(P.text)
                        Spacer()
                        if !app.rooms.rooms.isEmpty {
                            Button { createOpen = true } label: { Image(systemName: "plus").font(.system(size: 13)).foregroundColor(P.textMuted).frame(width: 24, height: 24) }
                                .buttonStyle(.plain).help("Criar canal")
                        }
                    }.padding(.horizontal, 8).padding(.top, 8)
                    if let e = app.rooms.error { Text(e).font(.system(size: 11)).foregroundColor(P.danger).padding(.horizontal, 8) }
                    ForEach(app.rooms.rooms) { RoomRow(room: $0, onShowRoom: onShowRoom) }
                    if app.rooms.rooms.isEmpty && app.rooms.error == nil {
                        Button { createOpen = true } label: {
                            HStack { Text("Criar canal").foregroundColor(P.text); Spacer(); Image(systemName: "plus.circle").foregroundColor(P.textMuted) }
                                .font(.system(size: 13)).padding(12).background(P.input, in: RoundedRectangle(cornerRadius: 8))
                        }.buttonStyle(.plain).padding(.horizontal, 8)
                    }
                }.padding(.horizontal, 8)
            }
            if app.inVoice { connectedBar }
            Divider().overlay(P.border)
            footer
        }
        .background(P.sidebar)
    }

    private var header: some View {
        Button { serversOpen = true } label: {
            HStack(spacing: 10) {
                Avatar(name: app.servers.label, hue: app.servers.hue, size: 32, rounded: 8)
                    .overlay(alignment: .bottomTrailing) {
                        HealthDot(ok: app.servers.health[app.servers.selected]).padding(2).background(P.sidebar, in: Circle()).offset(x: 3, y: 3)
                    }
                Text(app.servers.label).font(.system(size: 15, weight: .semibold)).foregroundColor(P.text).lineLimit(1)
                Image(systemName: "chevron.down").font(.system(size: 11)).foregroundColor(P.textMuted)
                Spacer()
            }.padding(.horizontal, 16).frame(height: 56).contentShape(Rectangle())
        }.buttonStyle(.plain)
    }

    private var connectedBar: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text((app.session.reconnecting ? "A reconectar · " : "Voz ligada · ") + (app.shownRoom?.name ?? ""))
                .font(.system(size: 12, weight: .semibold)).foregroundColor(app.session.reconnecting ? P.idle : P.online).lineLimit(1)
            HStack(spacing: 16) {
                if let show = onShowRoom { Button("Ver sala", action: show).buttonStyle(.plain).font(.system(size: 12)).foregroundColor(P.text) }
                Button("Sair") { app.leaveVoice() }.buttonStyle(.plain).font(.system(size: 12)).foregroundColor(P.danger)
            }
        }.frame(maxWidth: .infinity, alignment: .leading).padding(10).background(P.input, in: RoundedRectangle(cornerRadius: 8)).padding(8)
    }

    private var footer: some View {
        HStack(spacing: 10) {
            Avatar(name: app.guest.name, hue: app.guest.hue, size: 36)
            Text(app.guest.name).font(.system(size: 13, weight: .semibold)).foregroundColor(P.text).lineLimit(1)
            Spacer()
            Button { app.setMuted(!app.muted) } label: {
                Image(systemName: app.muted ? "mic.slash" : "mic").font(.system(size: 15)).foregroundColor(app.muted ? P.danger : P.textMuted).frame(width: 32, height: 32)
            }.buttonStyle(.plain).help("Microfone")
        }.padding(.horizontal, 16).frame(height: 60)
    }
}

private struct RoomRow: View {
    @EnvironmentObject var app: AppModel
    let room: Room
    var onShowRoom: (() -> Void)?

    var body: some View {
        let connected = app.roomId == room.id
        VStack(alignment: .leading, spacing: 0) {
            Button { if connected, let show = onShowRoom { show() } else { app.joinRoom(room.id) } } label: {
                HStack(spacing: 8) {
                    Image(systemName: "speaker.wave.2").font(.system(size: 13)).foregroundColor(connected ? P.text : P.textMuted)
                    Text(room.name).font(.system(size: 13)).foregroundColor(connected ? P.text : P.textMuted).lineLimit(1)
                    Spacer()
                    Text("\(room.id) · \(app.people(in: room).count)").font(.system(size: 11)).foregroundColor(P.textFaint).lineLimit(1)
                }.padding(.horizontal, 8).frame(height: 32).hoverRow(selected: connected)
            }.buttonStyle(.plain)
            ForEach(app.people(in: room), id: \.userId) { pt in
                HStack(spacing: 8) {
                    Avatar(name: pt.name, hue: hue(pt.userId), size: 20)
                    Text(pt.name).font(.system(size: 12)).foregroundColor(P.textMuted).lineLimit(1)
                    Spacer()
                    if !pt.mic { Image(systemName: "mic.slash").font(.system(size: 10)).foregroundColor(P.textFaint) }
                    if pt.camera { Image(systemName: "video").font(.system(size: 10)).foregroundColor(P.textFaint) }
                    if pt.screen { Image(systemName: "rectangle.on.rectangle").font(.system(size: 10)).foregroundColor(P.textFaint) }
                }.padding(.leading, 28).padding(.trailing, 8).frame(height: 26)
            }
        }
    }

    private func hue(_ userId: String) -> Double { app.hue(of: userId) }
}

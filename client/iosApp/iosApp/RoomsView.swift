import SwiftUI
import KurzShared

/// Lista de salas do servidor ligado (como o ChannelSidebar do Compose): cabeçalho com o servidor, salas com quem lá está, barra da sala ligada.
struct RoomsView: View {
    @EnvironmentObject var store: Store
    let s: KState
    @State private var serversOpen = false
    @State private var createOpen = false

    var body: some View {
        VStack(spacing: 0) {
            header
            if let n = s.notice { NoticeBar(text: n) { store.kurz.dismissNotice() } }
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 6) {
                    HStack {
                        Text("Salas").font(.system(size: 13, weight: .semibold)).foregroundColor(P.text)
                        Spacer()
                        if !s.rooms.isEmpty {
                            Button { createOpen = true } label: { Image(systemName: "plus").font(.system(size: 14)).foregroundColor(P.textMuted).frame(width: 28, height: 28) }
                                .accessibilityLabel("Criar canal")
                        }
                    }.padding(.horizontal, 8).padding(.top, 8)
                    if let e = s.listError { Text(e).font(.system(size: 11)).foregroundColor(P.danger).padding(.horizontal, 8) }
                    ForEach(s.rooms, id: \.id) { RoomRow(room: $0) }
                    if s.rooms.isEmpty && s.listError == nil {
                        Button { createOpen = true } label: {
                            HStack { Text("Criar canal").foregroundColor(P.text); Spacer(); Image(systemName: "plus.circle").foregroundColor(P.textMuted) }
                                .font(.system(size: 13)).padding(12).background(P.input, in: RoundedRectangle(cornerRadius: 8))
                        }.padding(.horizontal, 8)
                    }
                }.padding(.horizontal, 8)
            }
            if s.inRoom { connectedBar }
            Divider().overlay(P.border)
            footer
        }
        .background(P.sidebar.ignoresSafeArea())
        .sheet(isPresented: $serversOpen) { ServersSheet(s: s) }
        .onChange(of: serversOpen) { store.kurz.setServersOpen(open: $0) }
        .sheet(isPresented: $createOpen) { CreateRoomSheet() }
    }

    private var header: some View {
        Button { serversOpen = true } label: {
            HStack(spacing: 10) {
                Avatar(name: s.serverLabel, hue: s.serverHue, size: 32, rounded: 8)
                    .overlay(alignment: .bottomTrailing) { HealthDot(health: s.serverHealth).padding(2).background(P.sidebar, in: Circle()).offset(x: 3, y: 3) }
                Text(s.serverLabel).font(.system(size: 15, weight: .semibold)).foregroundColor(P.text).lineLimit(1)
                Image(systemName: "chevron.down").font(.system(size: 12)).foregroundColor(P.textMuted)
                Spacer()
            }.padding(.horizontal, 16).frame(height: 56)
        }.buttonStyle(.plain)
    }

    private var connectedBar: some View {
        VStack(spacing: 8) {
            Text((s.reconnecting ? "A reconectar · " : "Voz ligada · ") + (s.roomName ?? ""))
                .font(.system(size: 12, weight: .semibold)).foregroundColor(s.reconnecting ? P.idle : P.online).lineLimit(1).frame(maxWidth: .infinity, alignment: .leading)
            HStack(spacing: 8) {
                Button("Ver sala") { store.kurz.showRoom() }.foregroundColor(P.text).frame(maxWidth: .infinity)
                Button("Sair") { store.kurz.leaveRoom() }.foregroundColor(P.danger).frame(maxWidth: .infinity)
            }.font(.system(size: 13))
        }.padding(10).background(P.input, in: RoundedRectangle(cornerRadius: 8)).padding(8)
    }

    private var footer: some View {
        HStack(spacing: 10) {
            Avatar(name: s.myName, hue: 230, size: 36)
            Text(s.myName).font(.system(size: 13, weight: .semibold)).foregroundColor(P.text).lineLimit(1)
            Spacer()
            Button { store.kurz.toggleMute() } label: {
                Image(systemName: s.muted ? "mic.slash" : "mic").font(.system(size: 16)).foregroundColor(s.muted ? P.danger : P.textMuted).frame(width: 36, height: 36)
            }.accessibilityLabel("Microfone")
        }.padding(.horizontal, 16).frame(height: 60)
    }
}

private struct RoomRow: View {
    @EnvironmentObject var store: Store
    let room: KRoom

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Button { room.connected ? store.kurz.showRoom() : store.kurz.joinRoom(id: room.id) } label: {
                HStack(spacing: 8) {
                    Image(systemName: "speaker.wave.2").font(.system(size: 14)).foregroundColor(room.connected ? P.text : P.textMuted)
                    Text(room.name).font(.system(size: 14)).foregroundColor(room.connected ? P.text : P.textMuted).lineLimit(1)
                    Spacer()
                    Text("\(room.id) · \(room.people.count)").font(.system(size: 11)).foregroundColor(P.textFaint).lineLimit(1)
                }
                .padding(.horizontal, 8).frame(height: 40)
                .background(room.connected ? P.selected : Color.clear, in: RoundedRectangle(cornerRadius: 6))
            }.buttonStyle(.plain)
            ForEach(room.people, id: \.id) { pt in
                HStack(spacing: 8) {
                    Avatar(name: pt.name, hue: pt.hue, size: 20)
                    Text(pt.name).font(.system(size: 12)).foregroundColor(P.textMuted).lineLimit(1)
                    Spacer()
                    if !pt.mic { Image(systemName: "mic.slash").font(.system(size: 10)).foregroundColor(P.textFaint) }
                    if pt.camera { Image(systemName: "video").font(.system(size: 10)).foregroundColor(P.textFaint) }
                    if pt.screen { Image(systemName: "rectangle.on.rectangle").font(.system(size: 10)).foregroundColor(P.textFaint) }
                }.padding(.leading, 32).padding(.trailing, 8).frame(height: 28)
            }
        }
    }
}

/// Aviso de permissão/dispositivo em falta ou de falha de ligação; toque para fechar, fecha sozinho ao fim de alguns segundos.
struct NoticeBar: View {
    let text: String
    let dismiss: () -> Void

    var body: some View {
        Button(action: dismiss) {
            HStack(spacing: 10) {
                Image(systemName: "exclamationmark.circle").foregroundColor(P.danger)
                Text(text).font(.system(size: 12.5)).foregroundColor(P.text).multilineTextAlignment(.leading)
                Spacer()
                Image(systemName: "xmark").font(.system(size: 11)).foregroundColor(P.textMuted)
            }.padding(.horizontal, 16).padding(.vertical, 10).background(P.danger.opacity(0.12))
        }.buttonStyle(.plain)
        .task(id: text) { try? await Task.sleep(nanoseconds: 8_000_000_000); dismiss() }
    }
}

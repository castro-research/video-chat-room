import SwiftUI

/// Sala de voz no telemóvel: grelha de mosaicos; tocar num põe-no em destaque (com zoom por pinça), tocar no destacado volta à grelha.
struct VoiceRoomView: View {
    @EnvironmentObject var app: AppModel
    let onBack: () -> Void
    @State private var focus: String?

    var body: some View {
        let room = app.shownRoom
        let people = room.map { app.people(in: $0) } ?? []
        var slots: [(Tile, Participant)] = []
        for p in people {
            slots.append((Tile(userId: p.userId, name: p.name, kind: .person), p))
            if p.screen && p.userId != app.guest.userId { slots.append((Tile(userId: p.userId, name: p.name, kind: .screen), p)) }
        }
        let big = slots.first { $0.0.id == focus }
        return VStack(spacing: 0) {
            header(count: people.count)
            Divider().overlay(P.border)
            if let n = app.notice { NoticeBar(text: n) { app.notice = nil } }
            if let big {
                TileView(tile: big.0, person: big.1, focused: true).modifier(Zoomable { focus = nil }).padding(16)
                ScrollView(.horizontal) {
                    HStack(spacing: 12) {
                        ForEach(slots.filter { $0.0.id != big.0.id }, id: \.0.id) { t in
                            TileView(tile: t.0, person: t.1).frame(width: 128, height: 72).onTapGesture { focus = t.0.id }
                        }
                    }.padding(.horizontal, 16).padding(.bottom, 16)
                }.frame(height: 88)
            } else {
                ScrollView {
                    LazyVGrid(columns: [GridItem(.adaptive(minimum: 170), spacing: 16)], spacing: 16) {
                        ForEach(slots, id: \.0.id) { t in TileView(tile: t.0, person: t.1).aspectRatio(16 / 9, contentMode: .fit).onTapGesture { focus = t.0.id } }
                    }.padding(16)
                    if people.count <= 1 {
                        Text("Ainda ninguém mais está nesta sala. Quem entrar aparece aqui.").font(.system(size: 13)).foregroundColor(P.textFaint).multilineTextAlignment(.center).padding(.bottom, 24)
                    }
                }
            }
            Divider().overlay(P.border)
            controls
        }.background(P.main.ignoresSafeArea())
    }

    private func header(count: Int) -> some View {
        HStack(spacing: 8) {
            Button(action: onBack) { Image(systemName: "chevron.left").foregroundColor(P.textMuted).frame(width: 36, height: 36) }.accessibilityLabel("Voltar")
            Image(systemName: "speaker.wave.2").font(.system(size: 14)).foregroundColor(P.textMuted)
            Text(app.shownRoom?.name ?? "A entrar…").font(.system(size: 15, weight: .semibold)).foregroundColor(P.text).lineLimit(1)
            Text("· \(count) \(count == 1 ? "pessoa" : "pessoas")").font(.system(size: 12)).foregroundColor(P.textFaint).lineLimit(1)
            if app.session.reconnecting { Text("· a reconectar…").font(.system(size: 12, weight: .semibold)).foregroundColor(P.idle).lineLimit(1) }
            Spacer()
        }.padding(.horizontal, 8).frame(height: 56)
    }

    private var controls: some View {
        HStack(spacing: 16) {
            ctl(app.muted ? "mic.slash" : "mic", "Microfone", on: !app.muted) { app.setMuted(!app.muted) }
            ctl(app.camOn ? "video" : "video.slash", "Câmara", on: app.camOn) { app.setCamera(!app.camOn) }
            if app.camOn && CameraCapture.cameras.count > 1 { ctl("camera.rotate", "Trocar de câmara", on: true) { app.flipCamera() } }
            RoutePicker().frame(width: 48, height: 48).background(P.input, in: Circle()).accessibilityLabel("Saída de áudio")
            Button { app.leaveVoice() } label: {
                HStack(spacing: 8) { Image(systemName: "phone.down"); Text("Sair").font(.system(size: 13, weight: .semibold)) }
                    .foregroundColor(P.danger).padding(.horizontal, 20).frame(height: 48).background(P.danger.opacity(0.14), in: Capsule())
            }
        }.frame(height: 80).frame(maxWidth: .infinity)
    }

    private func ctl(_ icon: String, _ label: String, on: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: icon).font(.system(size: 18)).foregroundColor(on ? P.text : P.textMuted).frame(width: 48, height: 48)
                .background(on ? P.selected : P.input, in: Circle())
        }.accessibilityLabel(label)
    }
}

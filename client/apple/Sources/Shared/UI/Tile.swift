import SwiftUI
import WebRTC

/// Um mosaico da sala: a pessoa (câmara ou avatar) ou o ecrã que ela partilha.
struct Tile: Identifiable, Hashable {
    enum Kind { case person, screen }
    let userId: String
    let name: String
    let kind: Kind
    var id: String { "\(userId):\(kind == .screen ? "screen" : "camera")" }
}

struct TileView: View {
    @EnvironmentObject var app: AppModel
    let tile: Tile
    let person: Participant
    var focused = false

    var body: some View {
        let mine = tile.userId == app.guest.userId
        let speaking = tile.kind == .person && mine && !app.muted && app.session.micLevel > 0.05
        ZStack(alignment: .bottomLeading) {
            P.sidebar
            content(mine: mine).frame(maxWidth: .infinity, maxHeight: .infinity)
            HStack(spacing: 6) {
                if tile.kind == .person && !person.mic { Image(systemName: "mic.slash").font(.system(size: 11)).foregroundColor(.white) }
                Text(tile.kind == .screen ? "\(tile.name) · ecrã" : tile.name).font(.system(size: 12, weight: .medium)).foregroundColor(.white).lineLimit(1)
            }
            .padding(.horizontal, 8).padding(.vertical, 4).background(.black.opacity(0.55), in: RoundedRectangle(cornerRadius: 6)).padding(8)
        }
        .overlay(alignment: .topTrailing) {
            if !mine && tile.kind == .person { VolumeButton(userId: tile.userId).padding(8) }
        }
        .overlay(alignment: .bottom) {
            if mine && tile.kind == .person && !app.muted {
                GeometryReader { g in
                    Rectangle().fill(P.online).frame(width: g.size.width * app.session.micLevel, height: 3).frame(maxHeight: .infinity, alignment: .bottom)
                }.allowsHitTesting(false)
            }
        }
        .clipShape(RoundedRectangle(cornerRadius: 10))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(speaking ? P.online : P.border, lineWidth: speaking ? 3 : 1))
    }

    @ViewBuilder private func content(mine: Bool) -> some View {
        if let track = track(mine: mine) {
            // o ecrã próprio mostra-se ajustado; a câmara própria em espelho não, para manter simples
            VideoView(track: track, fit: tile.kind == .screen)
        } else {
            Avatar(name: tile.name, hue: app.hue(of: tile.userId), size: focused ? 120 : 72)
        }
    }

    private func track(mine: Bool) -> RTCVideoTrack? {
        let screen = tile.kind == .screen
        if mine {
#if os(macOS)
            if screen { return app.local.screen.track }
#endif
            return screen ? nil : (app.camOn ? app.local.camera.track : nil)
        }
        let key = "\(tile.userId):\(screen ? "screen" : "camera")"
        return app.session.videoKeys.contains(key) ? app.session.videoTrack(key) : nil
    }
}

/// Volume de uma pessoa só para quem ouve (0–200%).
struct VolumeButton: View {
    @EnvironmentObject var app: AppModel
    let userId: String
    @State private var open = false

    var body: some View {
        let v = app.session.volume(userId)
        Button { open.toggle() } label: {
            Image(systemName: v == 0 ? "speaker.slash" : "speaker.wave.2").font(.system(size: 12)).foregroundColor(.white)
                .frame(width: 26, height: 26).background(.black.opacity(0.55), in: Circle())
        }.buttonStyle(.plain).help("Volume")
        .popover(isPresented: $open) {
            VStack(spacing: 8) {
                Text("Volume · \(Int(v * 100))%").font(.system(size: 12))
                Slider(value: Binding(get: { Double(app.session.volume(userId)) }, set: { app.session.setVolume(userId, Float($0)) }), in: 0...2).frame(width: 180)
            }.padding(14).compactPopover()
        }
    }
}

extension View {
    /// No iPhone o popover vira folha por defeito; a partir do iOS 16.4 mantém-se como popover.
    @ViewBuilder func compactPopover() -> some View {
#if os(iOS)
        if #available(iOS 16.4, *) { presentationCompactAdaptation(.popover) } else { self }
#else
        self
#endif
    }
}

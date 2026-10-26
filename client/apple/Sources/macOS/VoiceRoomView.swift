import SwiftUI

/// A sala ligada: grelha de mosaicos (pessoas e ecrãs partilhados) com a barra de controlos; clicar num mosaico dá-lhe foco.
struct VoiceRoomView: View {
    @EnvironmentObject var app: AppModel
    @Binding var sharePicker: Bool
    @State private var focus: String?

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 8) {
                Image(systemName: "speaker.wave.2").foregroundColor(P.textMuted)
                Text(app.shownRoom?.name ?? "").font(.system(size: 15, weight: .semibold)).foregroundColor(P.text)
                Spacer()
            }.padding(.horizontal, 16).frame(height: 48)
            Divider().overlay(P.border)
            if let room = app.shownRoom { grid(room) } else { Spacer() }
            ControlsBar(sharePicker: $sharePicker)
        }.background(P.main)
    }

    private func grid(_ room: Room) -> some View {
        let people = app.people(in: room)
        var tiles: [(Tile, Participant)] = []
        for p in people {
            tiles.append((Tile(userId: p.userId, name: p.name, kind: .person), p))
            if p.screen { tiles.append((Tile(userId: p.userId, name: p.name, kind: .screen), p)) }
        }
        let main = tiles.first { $0.0.id == focus }
        return Group {
            if let main {
                VStack(spacing: 8) {
                    TileView(tile: main.0, person: main.1, focused: true).onTapGesture { focus = nil }
                    ScrollView(.horizontal) {
                        HStack(spacing: 8) {
                            ForEach(tiles.filter { $0.0.id != main.0.id }, id: \.0.id) { t in
                                TileView(tile: t.0, person: t.1).frame(width: 200, height: 120).onTapGesture { focus = t.0.id }
                            }
                        }
                    }.frame(height: 120)
                }
            } else {
                GeometryReader { g in
                    let cols = tiles.count <= 1 ? 1 : tiles.count <= 4 ? 2 : 3
                    let rows = (tiles.count + cols - 1) / cols
                    let w = (g.size.width - CGFloat(cols - 1) * 8) / CGFloat(cols)
                    let h = (g.size.height - CGFloat(rows - 1) * 8) / CGFloat(max(rows, 1))
                    LazyVGrid(columns: Array(repeating: GridItem(.fixed(w), spacing: 8), count: cols), spacing: 8) {
                        ForEach(tiles, id: \.0.id) { t in
                            TileView(tile: t.0, person: t.1).frame(width: w, height: h).onTapGesture { focus = t.0.id }
                        }
                    }
                }
            }
        }.padding(12)
    }
}

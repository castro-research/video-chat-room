import SwiftUI
import KurzShared

/// Sala de voz: grelha de mosaicos (pessoas e partilhas de ecrã); tocar num põe-no em destaque, tocar no destacado volta à grelha.
struct VoiceRoomView: View {
    @EnvironmentObject var store: Store
    let s: KState
    @State private var focus: String?

    private struct Slot: Identifiable { let id: String; let person: KPerson; let screen: Bool }

    private var slots: [Slot] {
        s.people.map { Slot(id: "u:\($0.id)", person: $0, screen: false) }
            + s.people.filter { $0.screen && !$0.isMe }.map { Slot(id: "screen:\($0.id)", person: $0, screen: true) }
    }

    var body: some View {
        let all = slots
        let big = all.first { $0.id == focus }
        VStack(spacing: 0) {
            header
            Divider().overlay(P.border)
            if let n = s.notice { NoticeBar(text: n) { store.kurz.dismissNotice() } }
            if let big {
                tile(big).padding(16).onTapGesture { focus = nil }
                ScrollView(.horizontal) {
                    HStack(spacing: 12) {
                        ForEach(all.filter { $0.id != big.id }) { sl in tile(sl).frame(width: 128, height: 72).onTapGesture { focus = sl.id } }
                    }.padding(.horizontal, 16).padding(.bottom, 16)
                }.frame(height: 88)
            } else {
                ScrollView {
                    LazyVGrid(columns: [GridItem(.adaptive(minimum: 170), spacing: 16)], spacing: 16) {
                        ForEach(all) { sl in tile(sl).aspectRatio(16 / 9, contentMode: .fit).onTapGesture { focus = sl.id } }
                    }.padding(16)
                    if s.people.count <= 1 {
                        Text("Ainda ninguém mais está nesta sala. Quem entrar aparece aqui.").font(.system(size: 13)).foregroundColor(P.textFaint).multilineTextAlignment(.center).padding(.bottom, 24)
                    }
                }
            }
            Divider().overlay(P.border)
            controls
        }
        .background(P.main.ignoresSafeArea())
    }

    private var header: some View {
        HStack(spacing: 8) {
            Button { store.kurz.hideRoom() } label: { Image(systemName: "chevron.left").foregroundColor(P.textMuted).frame(width: 36, height: 36) }.accessibilityLabel("Voltar")
            Image(systemName: "speaker.wave.2").font(.system(size: 14)).foregroundColor(P.textMuted)
            Text(s.roomName ?? "A entrar…").font(.system(size: 15, weight: .semibold)).foregroundColor(P.text).lineLimit(1)
            Text("· \(s.people.count) \(s.people.count == 1 ? "pessoa" : "pessoas")").font(.system(size: 12)).foregroundColor(P.textFaint).lineLimit(1)
            if s.reconnecting { Text("· a reconectar…").font(.system(size: 12, weight: .semibold)).foregroundColor(P.idle).lineLimit(1) }
            Spacer()
        }.padding(.horizontal, 8).frame(height: 56)
    }

    @ViewBuilder
    private func tile(_ sl: Slot) -> some View {
        let p = sl.person
        let source = sl.screen ? "screen" : "camera"
        let hasVideo = s.videoKeys.contains("\(p.id):\(source)") && (sl.screen || (p.isMe ? s.camOn : p.camera))
        let speaking = !sl.screen && p.isMe && s.micLevel > 0.3
        ZStack {
            (sl.screen ? Color.black : P.sidebar)
            if !sl.screen { GeometryReader { g in Avatar(name: p.name, hue: p.hue, size: min(88, g.size.height * 0.4)).position(x: g.size.width / 2, y: g.size.height / 2) } }
            if hasVideo {
                let video = VideoView(userId: p.id, source: source, mirrored: p.isMe && !sl.screen, fit: sl.screen)
                if big(sl) { video.modifier(Zoomable { focus = nil }) } else { video }
            }
        }
        .overlay(alignment: .bottomLeading) {
            Text(sl.screen ? "\(p.name) · ecrã" : p.isMe ? "\(p.name) (tu)" : p.name)
                .font(.system(size: 12)).foregroundColor(.white).lineLimit(1).padding(.horizontal, 8).padding(.vertical, 3)
                .background(Color.black.opacity(0.55), in: RoundedRectangle(cornerRadius: 6)).padding(10)
        }
        .overlay(alignment: .topTrailing) {
            if !sl.screen && !p.mic {
                Image(systemName: "mic.slash").font(.system(size: 12)).foregroundColor(P.danger).frame(width: 24, height: 24).background(Color.black.opacity(0.55), in: Circle()).padding(10)
            }
        }
        .overlay(alignment: .topLeading) {
            if !sl.screen && !p.isMe && s.canSetVolume { VolumeButton(person: p).padding(10) }
        }
        .overlay(alignment: .bottom) {
            if !sl.screen && p.isMe && !s.muted { GeometryReader { g in Rectangle().fill(P.accent).frame(width: g.size.width * CGFloat(s.micLevel), height: 3).position(x: g.size.width * CGFloat(s.micLevel) / 2, y: g.size.height - 1.5) } }
        }
        .clipShape(RoundedRectangle(cornerRadius: 12))
        .overlay(RoundedRectangle(cornerRadius: 12).stroke(big(sl) ? P.accent : speaking ? P.accent : P.border, lineWidth: big(sl) || speaking ? 2 : 1))
        .contentShape(RoundedRectangle(cornerRadius: 12))
    }

    private func big(_ sl: Slot) -> Bool { focus == sl.id }

    private var controls: some View {
        HStack(spacing: 16) {
            ctl(s.muted ? "mic.slash" : "mic", "Microfone", on: !s.muted) { store.kurz.toggleMute() }
            ctl(s.camOn ? "video" : "video.slash", "Câmara", on: s.camOn) { store.kurz.toggleCamera() }
            Button { store.kurz.leaveRoom() } label: {
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

/// Botão no mosaico de outro participante: abre um slider com o volume com que o ouves (só para ti), de 0 a 200 %.
private struct VolumeButton: View {
    @EnvironmentObject var store: Store
    let person: KPerson
    @State private var open = false
    @State private var value: Float = 1

    var body: some View {
        Button { value = person.volume; open = true } label: {
            Image(systemName: person.volume == 0 ? "speaker.slash" : "speaker.wave.2").font(.system(size: 12)).foregroundColor(.white)
                .frame(width: 24, height: 24).background(Color.black.opacity(0.55), in: Circle())
        }
        .accessibilityLabel("Volume")
        .popover(isPresented: $open) {
            HStack(spacing: 10) {
                Slider(value: Binding(get: { Double(value) }, set: { value = Float($0); store.kurz.setVolume(userId: person.id, volume: value) }), in: 0...2)
                Text("\(Int((value * 100).rounded()))%").font(.system(size: 12)).monospacedDigit().frame(width: 44)
            }.padding(16).frame(width: 260).compactPopover()
        }
    }
}

private extension View {
    /// No iPhone o popover vira folha por defeito; a partir do iOS 16.4 mantém-se como popover.
    @ViewBuilder func compactPopover() -> some View {
        if #available(iOS 16.4, *) { presentationCompactAdaptation(.popover) } else { self }
    }
}

/// Pinça para ampliar (até 5x) e arrasta para mexer; tocar com zoom volta ao tamanho normal, sem zoom chama [onTap].
private struct Zoomable: ViewModifier {
    let onTap: () -> Void
    @State private var scale: CGFloat = 1
    @State private var base: CGFloat = 1
    @State private var offset: CGSize = .zero
    @State private var baseOffset: CGSize = .zero

    func body(content: Content) -> some View {
        GeometryReader { g in
            let limit = { (s: CGFloat) in CGSize(width: g.size.width * (s - 1) / 2, height: g.size.height * (s - 1) / 2) }
            let clamp = { (o: CGSize, s: CGFloat) in
                CGSize(width: min(max(o.width, -limit(s).width), limit(s).width), height: min(max(o.height, -limit(s).height), limit(s).height))
            }
            content
                .frame(width: g.size.width, height: g.size.height)
                .scaleEffect(scale).offset(offset)
                .contentShape(Rectangle())
                .gesture(MagnificationGesture()
                    .onChanged { scale = min(max(base * $0, 1), 5); offset = clamp(baseOffset, scale) }
                    .onEnded { _ in base = scale; baseOffset = offset })
                .simultaneousGesture(DragGesture(minimumDistance: 8)
                    .onChanged { if scale > 1 { offset = clamp(CGSize(width: baseOffset.width + $0.translation.width, height: baseOffset.height + $0.translation.height), scale) } }
                    .onEnded { _ in baseOffset = offset })
                .onTapGesture {
                    if scale > 1 { withAnimation(.easeOut(duration: 0.2)) { scale = 1; base = 1; offset = .zero; baseOffset = .zero } } else { onTap() }
                }
        }
    }
}

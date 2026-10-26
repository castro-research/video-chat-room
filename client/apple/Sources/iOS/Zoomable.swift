import AVKit
import SwiftUI

/// Pinça para ampliar (até 5x) e arrasta para mexer; tocar com zoom volta ao tamanho normal, sem zoom chama [onTap].
struct Zoomable: ViewModifier {
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

/// Seletor de saída de áudio do sistema: altifalante, auscultadores, AirPods e outros Bluetooth/AirPlay.
struct RoutePicker: UIViewRepresentable {
    func makeUIView(context: Context) -> AVRoutePickerView {
        let v = AVRoutePickerView()
        v.tintColor = UIColor(P.textMuted)
        v.activeTintColor = UIColor(P.accent)
        v.prioritizesVideoDevices = false
        return v
    }

    func updateUIView(_ v: AVRoutePickerView, context: Context) {}
}

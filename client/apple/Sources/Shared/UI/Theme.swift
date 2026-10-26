import SwiftUI
#if canImport(AppKit)
import AppKit
#else
import UIKit
#endif

// Cores da referência (client/reference): claro e escuro seguem o sistema.
extension Color {
    init(hex: UInt32) {
        self.init(.sRGB, red: Double((hex >> 16) & 255) / 255, green: Double((hex >> 8) & 255) / 255, blue: Double(hex & 255) / 255)
    }

    static func dyn(_ light: UInt32, _ dark: UInt32) -> Color {
#if canImport(AppKit)
        Color(nsColor: NSColor(name: nil) { appearance in
            let isDark = appearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua
            return NSColor(Color(hex: isDark ? dark : light))
        })
#else
        Color(UIColor { $0.userInterfaceStyle == .dark ? UIColor(Color(hex: dark)) : UIColor(Color(hex: light)) })
#endif
    }
}

enum P {
    static let sidebar = Color.dyn(0xF7F8FB, 0x1A1B1F)
    static let main = Color.dyn(0xFFFFFF, 0x16171A)
    static let border = Color.dyn(0xE6E8EF, 0x26282D)
    static let text = Color.dyn(0x16181D, 0xF2F3F5)
    static let textMuted = Color.dyn(0x5E6472, 0xB0B5C0)
    static let textFaint = Color.dyn(0x9AA0AE, 0x727887)
    static let hover = Color.dyn(0xECEEF4, 0x24262B)
    static let selected = Color.dyn(0xE9EBF2, 0x2B2D33)
    static let input = Color.dyn(0xF4F5F9, 0x212226)
    static let accent = Color(hex: 0x5865F2)
    static let online = Color(hex: 0x23A55A)
    static let idle = Color(hex: 0xF0B232)
    static let danger = Color.dyn(0xE5484D, 0xFF6B6F)
}

/// Avatar redondo com gradiente por matiz e iniciais.
struct Avatar: View {
    let name: String
    let hue: Double
    var size: CGFloat = 36
    var rounded: CGFloat?

    var body: some View {
        let h = hue.truncatingRemainder(dividingBy: 360) / 360
        let h2 = (hue + 30).truncatingRemainder(dividingBy: 360) / 360
        let initials = name.split(whereSeparator: { $0 == " " || $0 == "-" }).prefix(2).compactMap { $0.first.map(String.init) }.joined().uppercased()
        RoundedRectangle(cornerRadius: rounded ?? size / 2)
            .fill(LinearGradient(colors: [Color(hue: h, saturation: 0.55, brightness: 0.95), Color(hue: h2, saturation: 0.75, brightness: 0.72)], startPoint: .topLeading, endPoint: .bottomTrailing))
            .frame(width: size, height: size)
            .overlay(Text(initials).font(.system(size: size * 0.38, weight: .semibold)).foregroundColor(.white))
    }
}

/// Verde = responde ao /healthz, vermelho = não responde, cinza = ainda por saber.
struct HealthDot: View {
    let ok: Bool?
    var body: some View {
        Circle().fill(ok == true ? P.online : ok == false ? P.danger : P.textFaint).frame(width: 8, height: 8)
            .help(ok == true ? "Servidor a responder" : ok == false ? "Servidor sem resposta" : "A verificar o servidor")
    }
}

/// Fundo que realça ao passar o rato, para linhas clicáveis.
struct HoverRow: ViewModifier {
    var selected = false
    var radius: CGFloat = 6
    @State private var hovering = false

    func body(content: Content) -> some View {
        content
            .background((selected ? P.selected : hovering ? P.hover : Color.clear), in: RoundedRectangle(cornerRadius: radius))
            .contentShape(RoundedRectangle(cornerRadius: radius))
            .onHover { hovering = $0 }
    }
}

extension View {
    func hoverRow(selected: Bool = false, radius: CGFloat = 6) -> some View { modifier(HoverRow(selected: selected, radius: radius)) }
}

/// Aviso de permissão/dispositivo em falta ou de falha de ligação; clique para fechar, fecha sozinho ao fim de alguns segundos.
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

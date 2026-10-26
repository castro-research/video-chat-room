import SwiftUI

// Cores da referência (ui/Theme.kt): claro e escuro seguem o sistema.
extension Color {
    init(hex: UInt32) {
        self.init(.sRGB, red: Double((hex >> 16) & 255) / 255, green: Double((hex >> 8) & 255) / 255, blue: Double(hex & 255) / 255)
    }

    static func dyn(_ light: UInt32, _ dark: UInt32) -> Color {
        Color(UIColor { $0.userInterfaceStyle == .dark ? UIColor(Color(hex: dark)) : UIColor(Color(hex: light)) })
    }
}

enum P {
    static let sidebar = Color.dyn(0xF7F8FB, 0x1A1B1F)
    static let main = Color.dyn(0xFFFFFF, 0x16171A)
    static let border = Color.dyn(0xE6E8EF, 0x26282D)
    static let text = Color.dyn(0x16181D, 0xF2F3F5)
    static let textMuted = Color.dyn(0x5E6472, 0xB0B5C0)
    static let textFaint = Color.dyn(0x9AA0AE, 0x727887)
    static let selected = Color.dyn(0xE9EBF2, 0x2B2D33)
    static let input = Color.dyn(0xF4F5F9, 0x212226)
    static let accent = Color(hex: 0x5865F2)
    static let online = Color(hex: 0x23A55A)
    static let idle = Color(hex: 0xF0B232)
    static let danger = Color.dyn(0xE5484D, 0xFF6B6F)
}

/// Avatar redondo com gradiente por matiz e iniciais (como o Avatar do Compose).
struct Avatar: View {
    let name: String
    let hue: Float
    var size: CGFloat = 36
    var rounded: CGFloat? = nil

    var body: some View {
        let h = Double(hue.truncatingRemainder(dividingBy: 360)) / 360
        let h2 = Double((hue + 30).truncatingRemainder(dividingBy: 360)) / 360
        let initials = name.split(whereSeparator: { $0 == " " || $0 == "-" }).prefix(2).compactMap { $0.first.map(String.init) }.joined().uppercased()
        let shape = RoundedRectangle(cornerRadius: rounded ?? size / 2)
        shape.fill(LinearGradient(colors: [Color(hue: h, saturation: 0.55, brightness: 0.95), Color(hue: h2, saturation: 0.75, brightness: 0.72)], startPoint: .topLeading, endPoint: .bottomTrailing))
            .frame(width: size, height: size)
            .overlay(Text(initials).font(.system(size: size * 0.38, weight: .semibold)).foregroundColor(.white))
    }
}

/// Verde = responde ao /healthz, vermelho = não responde, cinza = ainda por saber.
struct HealthDot: View {
    let health: Int32
    var body: some View {
        Circle().fill(health > 0 ? P.online : health == 0 ? P.danger : P.textFaint).frame(width: 8, height: 8)
            .accessibilityLabel(health > 0 ? "Servidor a responder" : health == 0 ? "Servidor sem resposta" : "A verificar o servidor")
    }
}

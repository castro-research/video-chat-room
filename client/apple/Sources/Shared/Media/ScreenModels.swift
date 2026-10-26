import Foundation
#if canImport(AppKit)
import AppKit
typealias PlatformImage = NSImage
#else
import UIKit
typealias PlatformImage = UIImage
#endif

/// Resolução da partilha de ecrã: altura máxima em px; [source] não reduz (a da própria fonte).
enum ScreenResolution: Int, CaseIterable, Identifiable {
    case p720 = 720, p1080 = 1080, p1440 = 1440, source = 0
    var id: Int { rawValue }
    var label: String { self == .source ? "Fonte" : "\(rawValue)p" }
}

/// Qualidade da partilha de ecrã: resolução e quadros por segundo.
struct ScreenQuality: Equatable {
    var resolution: ScreenResolution
    var fps: Int

    static let fpsOptions = [5, 15, 30, 60]
    /// Os dois atalhos: código (texto nítido, pouco movimento) e partilha normal.
    static let pairCoding = ScreenQuality(resolution: .p1440, fps: 15)
    static let normal = ScreenQuality(resolution: .p1080, fps: 30)

    var label: String { "\(resolution.label) · \(fps)fps" }
}

/// Uma coisa que se pode partilhar: um ecrã inteiro ou uma janela. [id] é do sistema.
struct ScreenSource: Identifiable, Hashable {
    let id: String
    let name: String
    let window: Bool
}


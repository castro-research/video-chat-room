#if os(macOS)
import AppKit
import WebRTC

/// Lista ecrãs e janelas com miniaturas, pelo capturador de ecrã do WebRTC. No macOS listar janelas pede a permissão de
/// gravação do ecrã, por isso só se actualiza quando se vai escolher ([refresh]). As miniaturas chegam uma a uma.
final class ScreenSources {
    /// O WebRTC só aceita o delegado no init (e guarda-o fraco): fica aqui, a reencaminhar para esta classe.
    private final class Forwarder: NSObject, RTCDesktopMediaListDelegate {
        var changed: () -> Void = {}
        var thumbnail: (RTCDesktopSource) -> Void = { _ in }
        func didDesktopSourceAdded(_ source: RTCDesktopSource) { changed() }
        func didDesktopSourceRemoved(_ source: RTCDesktopSource) { changed() }
        func didDesktopSourceNameChanged(_ source: RTCDesktopSource) { changed() }
        func didDesktopSourceThumbnailChanged(_ source: RTCDesktopSource) { thumbnail(source) }
    }

    private let forwarder = Forwarder()
    private let screens: RTCDesktopMediaList
    private let windows: RTCDesktopMediaList
    private var byId: [String: RTCDesktopSource] = [:]

    /// Avisado na thread principal quando a lista ou uma miniatura mudou.
    var onChange: ((_ sources: [ScreenSource], _ thumbs: [String: PlatformImage]) -> Void)?
    private(set) var sources: [ScreenSource] = []
    private(set) var thumbs: [String: PlatformImage] = [:]

    init() {
        screens = RTCDesktopMediaList(type: .screen, delegate: forwarder)
        windows = RTCDesktopMediaList(type: .window, delegate: forwarder)
        forwarder.changed = { [weak self] in self?.rebuild() }
        forwarder.thumbnail = { [weak self] s in
            self?.thumbs[s.sourceId] = s.thumbnail
            self?.notify()
        }
    }

    func refresh() {
        thumbs = [:]
        _ = screens.updateSourceList(true, updateAllThumbnails: true)
        _ = windows.updateSourceList(true, updateAllThumbnails: true)
        rebuild()
    }

    /// O objecto nativo de [id], para abrir a captura.
    func source(_ id: String) -> RTCDesktopSource? { byId[id] }

    /// O primeiro ecrã (a escolha quando não se escolheu nada).
    var firstScreen: ScreenSource? { sources.first { !$0.window } }

    private func rebuild() {
        var list: [ScreenSource] = []
        byId = [:]
        for (i, s) in screens.getSources().enumerated() {
            byId[s.sourceId] = s
            list.append(ScreenSource(id: s.sourceId, name: s.name.isEmpty ? "Ecrã \(i + 1)" : s.name, window: false))
        }
        for s in windows.getSources() where !s.name.isEmpty {
            byId[s.sourceId] = s
            list.append(ScreenSource(id: s.sourceId, name: s.name, window: true))
        }
        sources = list
        for s in byId.values { thumbs[s.sourceId] = s.thumbnail }
        notify()
    }

    private func notify() {
        DispatchQueue.main.async { [sources, thumbs, onChange] in onChange?(sources, thumbs) }
    }
}
#endif

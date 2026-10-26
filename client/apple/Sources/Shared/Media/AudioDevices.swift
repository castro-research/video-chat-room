import Foundation
import WebRTC

/// Um microfone ou uma saída de áudio. [id] é o do sistema; nil nas escolhas guarda "o padrão do sistema".
struct AudioDevice: Identifiable, Hashable {
    let id: String
    let name: String
}

/// Escolha de microfone e de saída de áudio, pelo AudioDeviceModule do WebRTC. A escolha fica lembrada entre arranques;
/// se o dispositivo lembrado já não existe, volta ao padrão.
#if os(macOS)
final class AudioDevices {
    private let defaults = UserDefaults.standard
    private var adm: RTCAudioDeviceModule { MediaEngine.shared.adm }

    var inputs: [AudioDevice] { adm.inputDevices.filter { !$0.isDefault }.map { AudioDevice(id: $0.deviceId, name: $0.name) } }
    var outputs: [AudioDevice] { adm.outputDevices.filter { !$0.isDefault }.map { AudioDevice(id: $0.deviceId, name: $0.name) } }

    /// Nome do dispositivo que o sistema usa como padrão (para a entrada "Padrão do sistema" dizer qual é).
    var defaultInputName: String? { adm.inputDevices.first { $0.isDefault }?.name }
    var defaultOutputName: String? { adm.outputDevices.first { $0.isDefault }?.name }

    var inputId: String? { defaults.string(forKey: "mic") }
    var outputId: String? { defaults.string(forKey: "speaker") }

    /// Aplica as escolhas lembradas (o ADM novo começa nos padrões); chamar antes de abrir o microfone.
    func restore() {
        select(input: inputId)
        select(output: outputId)
    }

    @discardableResult
    func select(input id: String?) -> Bool {
        let dev = id.flatMap { id in adm.inputDevices.first { $0.deviceId == id } } ?? RTCIODevice.defaultDevice(with: .input)
        let ok = adm.trySetInputDevice(dev)
        if ok { remember("mic", id) }
        return ok
    }

    @discardableResult
    func select(output id: String?) -> Bool {
        let dev = id.flatMap { id in adm.outputDevices.first { $0.deviceId == id } } ?? RTCIODevice.defaultDevice(with: .output)
        let ok = adm.trySetOutputDevice(dev)
        if ok { remember("speaker", id) }
        return ok
    }

    private func remember(_ key: String, _ id: String?) {
        if let id { defaults.set(id, forKey: key) } else { defaults.removeObject(forKey: key) }
    }
}
#else
/// No iOS a saída escolhe-se pelo selector do sistema (AVRoutePickerView) e o microfone segue a rota: nada a listar.
final class AudioDevices {
    var inputs: [AudioDevice] { [] }
    var outputs: [AudioDevice] { [] }
    var defaultInputName: String? { nil }
    var defaultOutputName: String? { nil }
    var inputId: String? { nil }
    var outputId: String? { nil }
    func restore() {}
    @discardableResult func select(input id: String?) -> Bool { true }
    @discardableResult func select(output id: String?) -> Bool { true }
}
#endif

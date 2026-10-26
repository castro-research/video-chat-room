import SwiftUI

/// Barra de baixo da sala: microfone, câmara, partilha e sair; os menus escolhem os dispositivos.
struct ControlsBar: View {
    @EnvironmentObject var app: AppModel
    @Binding var sharePicker: Bool

    var body: some View {
        HStack(spacing: 12) {
            Spacer()
            split("mic" + (app.muted ? ".slash" : ""), on: !app.muted, help: "Microfone", action: { app.setMuted(!app.muted) }) {
                Section("Microfone") {
                    Picker("Microfone", selection: Binding(get: { app.micId ?? "" }, set: { app.selectMic($0.isEmpty ? nil : $0) })) {
                        Text(app.local.devices.defaultInputName.map { "Padrão (\($0))" } ?? "Padrão").tag("")
                        ForEach(app.local.devices.inputs) { Text($0.name).tag($0.id) }
                    }.pickerStyle(.inline).labelsHidden()
                }
                Section("Saída de áudio") {
                    Picker("Saída de áudio", selection: Binding(get: { app.local.devices.outputId ?? "" }, set: { app.selectSpeaker($0.isEmpty ? nil : $0) })) {
                        Text(app.local.devices.defaultOutputName.map { "Padrão (\($0))" } ?? "Padrão").tag("")
                        ForEach(app.local.devices.outputs) { Text($0.name).tag($0.id) }
                    }.pickerStyle(.inline).labelsHidden()
                }
            }
            split("video" + (app.camOn ? "" : ".slash"), on: app.camOn, help: "Câmara", action: { app.setCamera(!app.camOn) }) {
                Picker("Câmara", selection: Binding(get: { app.cameraId ?? "" }, set: { app.selectCamera($0) })) {
                    ForEach(CameraCapture.cameras) { Text($0.name).tag($0.id) }
                }.pickerStyle(.inline).labelsHidden()
            }
            round(app.sharing ? "stop.fill" : "rectangle.on.rectangle", on: app.sharing, tint: P.danger, help: app.sharing ? "Parar partilha" : "Partilhar ecrã") {
                if app.sharing { app.stopSharing() } else { app.refreshScreenSources(); sharePicker = true }
            }
            if app.sharing {
                Button("Alterar") { app.refreshScreenSources(); sharePicker = true }.buttonStyle(.link)
            }
            Button { app.leaveVoice() } label: {
                Image(systemName: "phone.down.fill").foregroundColor(.white).frame(width: 44, height: 36).background(P.danger, in: Capsule())
            }.buttonStyle(.plain).help("Sair da sala")
            Spacer()
        }.padding(.vertical, 12).background(P.sidebar)
    }

    private func round(_ symbol: String, on: Bool, tint: Color = P.accent, help: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: symbol).font(.system(size: 15)).foregroundColor(on ? .white : P.text)
                .frame(width: 40, height: 40).background(on ? tint : P.input, in: Circle())
        }.buttonStyle(.plain).help(help)
    }

    /// Botão redondo com a seta dos dispositivos colada, na mesma cápsula.
    private func split<M: View>(_ symbol: String, on: Bool, help: String, action: @escaping () -> Void, @ViewBuilder menu: () -> M) -> some View {
        HStack(spacing: 0) {
            Button(action: action) {
                Image(systemName: symbol).font(.system(size: 15)).frame(width: 42, height: 40)
            }.buttonStyle(.plain).help(help)
            Menu { menu() } label: {
                Image(systemName: "chevron.down").font(.system(size: 9, weight: .bold)).frame(width: 22, height: 40).contentShape(Rectangle())
            }.menuStyle(.button).buttonStyle(.plain).menuIndicator(.hidden).fixedSize()
        }
        .foregroundColor(on ? .white : P.text)
        .background(on ? P.accent : P.input, in: Capsule())
    }
}

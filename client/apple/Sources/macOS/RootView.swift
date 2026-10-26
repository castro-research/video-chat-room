import SwiftUI

/// A janela: barra lateral à esquerda, a sala ligada (ou um convite a entrar) à direita, e as folhas.
struct RootView: View {
    @EnvironmentObject var app: AppModel
    @State private var serversOpen = false
    @State private var createOpen = false
    @State private var sharePicker = false

    var body: some View {
        HStack(spacing: 0) {
            SidebarView(serversOpen: $serversOpen, createOpen: $createOpen).frame(width: 260)
            Divider().overlay(P.border)
            if app.inVoice {
                VoiceRoomView(sharePicker: $sharePicker)
            } else {
                VStack(spacing: 8) {
                    Image(systemName: "speaker.wave.2").font(.system(size: 36)).foregroundColor(P.textFaint)
                    Text("Escolhe uma sala para entrar").foregroundColor(P.textMuted)
                }.frame(maxWidth: .infinity, maxHeight: .infinity).background(P.main)
            }
        }
        .frame(minWidth: 720, minHeight: 480)
        .sheet(isPresented: $serversOpen) { ServersSheet() }
        .sheet(isPresented: $createOpen) { CreateRoomSheet() }
        .sheet(isPresented: $sharePicker) { SharePickerSheet() }
    }
}

import SwiftUI

/// iPhone/iPad: a lista de salas e, por cima, a sala ligada (a que se volta com a seta).
struct RootView: View {
    @EnvironmentObject var app: AppModel
    @State private var serversOpen = false
    @State private var createOpen = false
    @State private var roomShown = false

    var body: some View {
        ZStack {
            SidebarView(serversOpen: $serversOpen, createOpen: $createOpen, onShowRoom: { roomShown = true })
            if roomShown && app.inVoice {
                VoiceRoomView(onBack: { roomShown = false }).transition(.move(edge: .trailing))
            }
        }
        .animation(.easeOut(duration: 0.2), value: roomShown)
        .onChange(of: app.roomId) { id in roomShown = id != nil }
        .sheet(isPresented: $serversOpen) { ServersSheet() }
        .sheet(isPresented: $createOpen) { CreateRoomSheet() }
    }
}

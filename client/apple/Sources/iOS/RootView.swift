import KurzKit
import SwiftUI

/// iPhone/iPad: entrada (login/registo) ou, com a sessão iniciada, a lista de canais numa pilha (o canal de texto empurra-se por cima)
/// e, por cima de tudo, a sala de voz ligada (a que se volta com a seta).
struct RootView: View {
    @EnvironmentObject var app: AppModel

    var body: some View {
        if app.auth.phase == .signedIn, let wm = app.workspace {
            MainView().environmentObject(wm)
        } else {
            AuthView()
        }
    }
}

private struct MainView: View {
    @EnvironmentObject var app: AppModel
    @EnvironmentObject var wm: WorkspaceModel
    @State private var sheet: AppSheet?
    @State private var textOpen = false

    var body: some View {
        ZStack {
            NavigationStack {
                ChannelListView(sheet: $sheet, onOpenText: { textOpen = true }, onShowRoom: { app.showingVoice = true })
                    .toolbar(.hidden, for: .navigationBar)
                    .navigationDestination(isPresented: $textOpen) {
                        if let ch = wm.selectedChannel {
                            ChannelView(channel: ch, onMembers: { sheet = .members }).id(ch.id)
                        } else {
                            Text("Canal indisponível").foregroundColor(P.textMuted)
                        }
                    }
            }
            if app.showingVoice && app.inVoice {
                VoiceRoomView(onBack: { app.showingVoice = false }).transition(.move(edge: .trailing))
            }
        }
        .animation(.easeOut(duration: 0.2), value: app.showingVoice)
        .appSheets($sheet)
        .onAppear { if wm.selectedGuildId == nil, let g = wm.ws.guilds.first { wm.selectGuild(g.id) } }
        .onChange(of: wm.ws.guilds.count) { _ in
            if wm.selectedGuildId == nil, let g = wm.ws.guilds.first { wm.selectGuild(g.id) }
        }
        .onChange(of: wm.selectedChannelId) { id in if id == nil { textOpen = false } }
    }
}

import KurzKit
import SwiftUI

/// A janela: entrada (login/registo) ou, com a sessão iniciada, comunidades | canais | canal de texto ou sala de voz | membros.
struct RootView: View {
    @EnvironmentObject var app: AppModel

    var body: some View {
        Group {
            if app.auth.phase == .signedIn, let wm = app.workspace {
                MainView().environmentObject(wm)
            } else {
                AuthView()
            }
        }
        .frame(minWidth: 720, minHeight: 480)
    }
}

private struct MainView: View {
    @EnvironmentObject var app: AppModel
    @EnvironmentObject var wm: WorkspaceModel
    @State private var sheet: AppSheet?
    @State private var sharePicker = false
    @State private var showMembers = true

    var body: some View {
        HStack(spacing: 0) {
            GuildBar(sheet: $sheet)
            Divider().overlay(P.border)
            ChannelListView(sheet: $sheet).frame(width: 240)
            Divider().overlay(P.border)
            Group {
                if app.showingVoice && app.inVoice {
                    VoiceRoomView(sharePicker: $sharePicker)
                } else if let ch = wm.selectedChannel {
                    HStack(spacing: 0) {
                        ChannelView(channel: ch, membersToggle: $showMembers).id(ch.id)
                        if showMembers {
                            Divider().overlay(P.border)
                            MembersView().frame(width: 220)
                        }
                    }
                } else {
                    VStack(spacing: 8) {
                        Image(systemName: "number").font(.system(size: 36)).foregroundColor(P.textFaint)
                        Text(wm.ws.guilds.isEmpty ? "Cria ou entra numa comunidade" : "Escolhe um canal").foregroundColor(P.textMuted)
                    }.frame(maxWidth: .infinity, maxHeight: .infinity).background(P.main)
                }
            }
        }
        .appSheets($sheet)
        .sheet(isPresented: $sharePicker) { SharePickerSheet() }
        .onAppear { if wm.selectedGuildId == nil, let g = wm.ws.guilds.first { wm.selectGuild(g.id, openFirstText: true) } }
        .onChange(of: wm.ws.guilds.count) { _ in
            // as guilds chegam depois do arranque: abre a primeira com o primeiro canal de texto
            if wm.selectedGuildId == nil, let g = wm.ws.guilds.first { wm.selectGuild(g.id, openFirstText: true) }
        }
        .onChange(of: wm.sections.count) { _ in
            if wm.selectedChannelId == nil, !app.showingVoice, let c = ChannelTree.firstText(wm.ws.channels[wm.selectedGuildId ?? ""] ?? []) { wm.openChannel(c.id) }
        }
    }
}

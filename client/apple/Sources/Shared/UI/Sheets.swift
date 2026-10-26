import SwiftUI

/// Servidores: escolher o ligado, adicionar outro pelo endereço ou tirar um adicionado.
struct ServersSheet: View {
    @EnvironmentObject var app: AppModel
    @Environment(\.dismiss) private var dismiss
    @State private var input = ""
    @State private var invalid = false

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("Servidores").font(.system(size: 16, weight: .semibold)).foregroundColor(P.text)
            VStack(spacing: 2) {
                ForEach(app.servers.servers, id: \.self) { url in
                    let on = url == app.servers.selected
                    HStack(spacing: 8) {
                        Image(systemName: "checkmark").font(.system(size: 12)).foregroundColor(P.accent).opacity(on ? 1 : 0)
                        Text(serverLabel(url)).font(.system(size: 13)).foregroundColor(on ? P.text : P.textMuted).lineLimit(1)
                        Spacer()
                        HealthDot(ok: app.servers.health[url])
                        if url == ServerList.factory {
                            Spacer().frame(width: 20)
                        } else {
                            Button { app.servers.remove(url) } label: { Image(systemName: "xmark").font(.system(size: 11)).foregroundColor(P.textMuted).frame(width: 20, height: 20) }
                                .buttonStyle(.plain).help("Remover")
                        }
                    }
                    .padding(.horizontal, 8).frame(height: 32).hoverRow(selected: on)
                    .onTapGesture { app.servers.select(url); dismiss() }
                }
            }
            Divider().overlay(P.border)
            Text("Adicionar servidor").font(.system(size: 12, weight: .medium)).foregroundColor(P.textMuted)
            TextField("endereço, ex.: cord.kurz.fyi", text: $input).textFieldStyle(.roundedBorder).onSubmit(add)
                .onChange(of: input) { _ in invalid = false }
            if invalid {
                Text("Isto não é um endereço. Usa o nome do servidor, ex.: cord.kurz.fyi ou http://localhost:30080.").font(.system(size: 11.5)).foregroundColor(P.danger)
            }
            HStack {
                Spacer()
                Button("Cancelar") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Adicionar", action: add).keyboardShortcut(.defaultAction).disabled(input.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
        .padding(20).sheetWidth().background(P.main)
        .onAppear { app.servers.setListOpen(true) }
        .onDisappear { app.servers.setListOpen(false) }
    }

    private func add() {
        if app.servers.add(input) { dismiss() } else { invalid = true }
    }
}

/// Criar canal de voz (as salas do manager); entra na sala assim que ela é criada.
struct CreateRoomSheet: View {
    @EnvironmentObject var app: AppModel
    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    @State private var busy = false

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            VStack(alignment: .leading, spacing: 2) {
                Text("Criar canal").font(.system(size: 16, weight: .semibold)).foregroundColor(P.text)
                Text("em Salas").font(.system(size: 12)).foregroundColor(P.textFaint)
            }
            HStack(spacing: 10) {
                Image(systemName: "speaker.wave.2").foregroundColor(P.text)
                VStack(alignment: .leading) {
                    Text("Voz").font(.system(size: 13, weight: .semibold)).foregroundColor(P.text)
                    Text("Voz, vídeo e partilha de ecrã.").font(.system(size: 11.5)).foregroundColor(P.textFaint)
                }
                Spacer()
            }.padding(12).background(P.input, in: RoundedRectangle(cornerRadius: 8))
            Text("Nome do canal").font(.system(size: 12, weight: .medium)).foregroundColor(P.textMuted)
            TextField("novo-canal", text: $name).textFieldStyle(.roundedBorder).onSubmit(create)
            HStack {
                Spacer()
                Button("Cancelar") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Criar canal", action: create).keyboardShortcut(.defaultAction).disabled(name.trimmingCharacters(in: .whitespaces).isEmpty || busy)
            }
        }.padding(20).sheetWidth().background(P.main)
    }

    private func create() {
        busy = true
        Task {
            if await app.createRoom(name) { dismiss() }
            busy = false
        }
    }
}

extension View {
    /// No Mac as folhas têm largura fixa; no iPhone/iPad ocupam o que o sistema der.
    func sheetWidth() -> some View {
#if os(macOS)
        frame(width: 420)
#else
        presentationDetents([.medium, .large])
#endif
    }
}

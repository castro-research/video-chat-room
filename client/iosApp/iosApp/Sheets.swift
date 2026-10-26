import SwiftUI
import KurzShared

/// Servidores: escolher o ligado, adicionar outro pelo endereço ou tirar um adicionado.
struct ServersSheet: View {
    @EnvironmentObject var store: Store
    @Environment(\.dismiss) private var dismiss
    let s: KState
    @State private var input = ""
    @State private var invalid = false

    var body: some View {
        NavigationStack {
            List {
                Section {
                    ForEach(s.servers, id: \.url) { srv in
                        Button { store.kurz.selectServer(url: srv.url); dismiss() } label: {
                            HStack(spacing: 8) {
                                Image(systemName: "checkmark").foregroundColor(P.accent).opacity(srv.url == s.serverUrl ? 1 : 0)
                                Text(srv.label).foregroundColor(srv.url == s.serverUrl ? P.text : P.textMuted).lineLimit(1)
                                Spacer()
                                HealthDot(health: srv.health)
                            }
                        }
                        .swipeActions { if srv.canRemove { Button("Remover", role: .destructive) { store.kurz.removeServer(url: srv.url) } } }
                    }
                }
                Section("Adicionar servidor") {
                    TextField("endereço, ex.: cord.kurz.fyi", text: $input)
                        .textInputAutocapitalization(.never).autocorrectionDisabled().keyboardType(.URL).submitLabel(.done)
                        .onSubmit(add).onChange(of: input) { _ in invalid = false }
                    if invalid {
                        Text("Isto não é um endereço. Usa o nome do servidor, ex.: cord.kurz.fyi ou http://localhost:30080.").font(.system(size: 11.5)).foregroundColor(P.danger)
                    }
                    Button("Adicionar", action: add).disabled(input.trimmingCharacters(in: .whitespaces).isEmpty)
                }
            }
            .navigationTitle("Servidores").navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Fechar") { dismiss() } } }
        }
        .presentationDetents([.medium, .large])
    }

    private func add() {
        if store.kurz.addServer(input: input) { input = ""; dismiss() } else { invalid = true }
    }
}

/// Criar canal de voz (as salas do manager); entra na sala assim que ela é criada.
struct CreateRoomSheet: View {
    @EnvironmentObject var store: Store
    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    @State private var busy = false

    var body: some View {
        NavigationStack {
            Form {
                Section("Tipo de canal") {
                    HStack(spacing: 10) {
                        Image(systemName: "speaker.wave.2")
                        VStack(alignment: .leading) {
                            Text("Voz").font(.system(size: 13, weight: .semibold))
                            Text("Voz, vídeo e partilha de ecrã.").font(.system(size: 11.5)).foregroundColor(P.textFaint)
                        }
                    }
                }
                Section("Nome do canal") {
                    TextField("novo-canal", text: $name).textInputAutocapitalization(.never).autocorrectionDisabled().submitLabel(.done).onSubmit(create)
                }
            }
            .navigationTitle("Criar canal").navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancelar") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) { Button("Criar", action: create).disabled(name.trimmingCharacters(in: .whitespaces).isEmpty || busy) }
            }
        }
        .presentationDetents([.medium])
    }

    private func create() {
        busy = true
        store.kurz.createRoom(name: name) { ok in
            busy = false
            if ok.boolValue { dismiss() }
        }
    }
}

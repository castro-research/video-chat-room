import KurzKit
import SwiftUI

/// Servidores Kurz: escolher o ligado, adicionar outro pelo endereço ou tirar um adicionado.
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
            TextField("endereço, ex.: localhost:8080", text: $input).plainInput().textFieldStyle(.roundedBorder).onSubmit(add)
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

/// As folhas da app, todas pedidas por um só `@State` na raiz.
enum AppSheet: Identifiable {
    case servers, createGuild, joinGuild, profile, members, invite
    case createChannel(parent: Channel?)

    var id: String {
        switch self {
        case .createChannel(let p): return "createChannel:" + (p?.id ?? "")
        case .servers: return "servers"
        case .createGuild: return "createGuild"
        case .joinGuild: return "joinGuild"
        case .profile: return "profile"
        case .members: return "members"
        case .invite: return "invite"
        }
    }
}

extension View {
    /// Apresenta a folha pedida em [sheet]. As do utilizador/dados precisam do WorkspaceModel no ambiente.
    func appSheets(_ sheet: Binding<AppSheet?>) -> some View {
        self.sheet(item: sheet) { s in
            switch s {
            case .servers: ServersSheet()
            case .createGuild: CreateGuildSheet()
            case .joinGuild: JoinGuildSheet()
            case .profile: ProfileSheet()
            case .members: MembersSheet()
            case .invite: InviteSheet()
            case .createChannel(let parent): CreateChannelSheet(parent: parent)
            }
        }
    }
}

private struct SheetFrame<Content: View>: View {
    let title: String
    var subtitle: String?
    @ViewBuilder var content: Content
    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            VStack(alignment: .leading, spacing: 2) {
                Text(title).font(.system(size: 16, weight: .semibold)).foregroundColor(P.text)
                if let subtitle { Text(subtitle).font(.system(size: 12)).foregroundColor(P.textFaint) }
            }
            content
        }.padding(20).sheetWidth().background(P.main)
    }
}

private func fieldLabel(_ t: String) -> some View { Text(t).font(.system(size: 12, weight: .medium)).foregroundColor(P.textMuted) }

private func errorText(_ t: String?) -> some View {
    Group { if let t { Text(t).font(.system(size: 12)).foregroundColor(P.danger) } }
}

/// Criar uma comunidade (guild); o utilizador fica como dono.
struct CreateGuildSheet: View {
    @EnvironmentObject var wm: WorkspaceModel
    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    @State private var busy = false

    var body: some View {
        SheetFrame(title: "Criar comunidade", subtitle: "Começa com um canal #geral.") {
            fieldLabel("Nome")
            TextField("A minha comunidade", text: $name).textFieldStyle(.roundedBorder).onSubmit(create)
            errorText(wm.notice)
            HStack {
                Spacer()
                Button("Cancelar") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Criar", action: create).keyboardShortcut(.defaultAction).disabled(name.trimmingCharacters(in: .whitespaces).isEmpty || busy)
            }
        }.onAppear { wm.notice = nil }
    }

    private func create() {
        busy = true
        Task {
            if await wm.createGuild(name.trimmingCharacters(in: .whitespaces)) { dismiss() }
            busy = false
        }
    }
}

/// Entrar numa comunidade com o código de um convite (aceita também um texto que o termine).
struct JoinGuildSheet: View {
    @EnvironmentObject var wm: WorkspaceModel
    @Environment(\.dismiss) private var dismiss
    @State private var code = ""
    @State private var busy = false

    var body: some View {
        SheetFrame(title: "Entrar com convite", subtitle: "Pede o código a quem gere a comunidade.") {
            fieldLabel("Código do convite")
            TextField("código", text: $code).textFieldStyle(.roundedBorder).plainInput().onSubmit(join)
            errorText(wm.notice)
            HStack {
                Spacer()
                Button("Cancelar") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Entrar", action: join).keyboardShortcut(.defaultAction).disabled(code.trimmingCharacters(in: .whitespaces).isEmpty || busy)
            }
        }.onAppear { wm.notice = nil }
    }

    private func join() {
        busy = true
        Task {
            if await wm.join(code: code) { dismiss() }
            busy = false
        }
    }
}

/// Criar canal (texto, voz ou categoria) na comunidade aberta, opcionalmente dentro de [parent].
struct CreateChannelSheet: View {
    @EnvironmentObject var wm: WorkspaceModel
    @Environment(\.dismiss) private var dismiss
    let parent: Channel?
    @State private var name = ""
    @State private var kind = ChannelKind.text
    @State private var busy = false

    var body: some View {
        SheetFrame(title: "Criar canal", subtitle: parent.map { "em \($0.name)" } ?? wm.selectedGuild?.name) {
            Picker("Tipo", selection: $kind) {
                Text("Texto").tag(ChannelKind.text)
                if wm.supportsVoice { Text("Voz").tag(ChannelKind.voice) }
                if parent == nil { Text("Categoria").tag(ChannelKind.category) }
            }.pickerStyle(.segmented).labelsHidden()
            Text(kind == .text ? "Mensagens, reações e respostas." : kind == .voice ? "Voz, vídeo e partilha de ecrã." : "Agrupa outros canais.")
                .font(.system(size: 11.5)).foregroundColor(P.textFaint)
            fieldLabel("Nome do canal")
            TextField(kind == .category ? "Nova categoria" : "novo-canal", text: $name).textFieldStyle(.roundedBorder).plainInput().onSubmit(create)
            errorText(wm.notice)
            HStack {
                Spacer()
                Button("Cancelar") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Criar canal", action: create).keyboardShortcut(.defaultAction).disabled(name.trimmingCharacters(in: .whitespaces).isEmpty || busy)
            }
        }.onAppear { wm.notice = nil }
    }

    private func create() {
        busy = true
        Task {
            if await wm.createChannel(name: name.trimmingCharacters(in: .whitespaces), kind: kind, parentId: kind == .category ? nil : parent?.id) { dismiss() }
            busy = false
        }
    }
}

/// Criar um convite para a comunidade aberta e copiar o código ou uma mensagem pronta a enviar.
struct InviteSheet: View {
    @EnvironmentObject var wm: WorkspaceModel
    @Environment(\.dismiss) private var dismiss
    @State private var expiry = 86_400
    @State private var invite: Invite?
    @State private var busy = false
    @State private var copied: String?

    var body: some View {
        SheetFrame(title: "Convidar para a comunidade", subtitle: wm.selectedGuild?.name) {
            if let invite {
                fieldLabel("Código")
                Text(invite.code).font(.system(size: 20, weight: .semibold, design: .monospaced)).foregroundColor(P.text).textSelection(.enabled)
                    .padding(12).frame(maxWidth: .infinity, alignment: .leading).background(P.input, in: RoundedRectangle(cornerRadius: 8))
                if let e = invite.expiresAt { Text("Expira a \(e.formatted(date: .abbreviated, time: .shortened))").font(.system(size: 11.5)).foregroundColor(P.textFaint) }
                HStack {
                    Button("Copiar código") { copy(invite.code, "Código copiado.") }
                    Button("Copiar convite") {
                        copy("Entra na minha comunidade Kurz! Servidor: \(serverLabel(wm.api.baseURL)) · código: \(invite.code)", "Convite copiado.")
                    }
                    Spacer()
                    Button("Fechar") { dismiss() }.keyboardShortcut(.defaultAction)
                }
                if let copied { Text(copied).font(.system(size: 12)).foregroundColor(P.online) }
            } else {
                fieldLabel("Expira em")
                Picker("Expira em", selection: $expiry) {
                    Text("1 hora").tag(3_600); Text("1 dia").tag(86_400); Text("7 dias").tag(604_800); Text("Nunca").tag(0)
                }.pickerStyle(.segmented).labelsHidden()
                errorText(wm.notice)
                HStack {
                    Spacer()
                    Button("Cancelar") { dismiss() }.keyboardShortcut(.cancelAction)
                    Button("Criar convite", action: create).keyboardShortcut(.defaultAction).disabled(busy)
                }
            }
        }.onAppear { wm.notice = nil }
    }

    private func copy(_ text: String, _ done: String) { copyToClipboard(text); copied = done }

    private func create() {
        busy = true
        Task {
            invite = await wm.createInvite(maxUses: nil, expiresIn: expiry == 0 ? nil : expiry)
            busy = false
        }
    }
}

/// O perfil do utilizador: nome e descrição (guardados no servidor), servidor ligado e terminar a sessão.
struct ProfileSheet: View {
    @EnvironmentObject var app: AppModel
    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    @State private var subtitle = ""
    @State private var busy = false

    var body: some View {
        SheetFrame(title: "Perfil", subtitle: app.auth.user.map { "@\($0.username) · \(serverLabel(app.auth.baseURL))" }) {
            if let u = app.auth.user {
                HStack { Avatar(name: name.isEmpty ? u.name : name, hue: u.avatarHue, size: 56); Spacer() }
            }
            fieldLabel("Nome")
            TextField("Nome", text: $name).textFieldStyle(.roundedBorder)
            fieldLabel("Descrição")
            TextField("o que estás a fazer", text: $subtitle).textFieldStyle(.roundedBorder)
            errorText(app.auth.error)
            HStack {
                Button("Terminar sessão", role: .destructive) { Task { await app.logout(); dismiss() } }
                Spacer()
                Button("Cancelar") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Guardar", action: save).keyboardShortcut(.defaultAction).disabled(name.trimmingCharacters(in: .whitespaces).isEmpty || busy)
            }
        }.onAppear {
            name = app.auth.user?.name ?? ""; subtitle = app.auth.user?.subtitle ?? ""; app.auth.error = nil
        }
    }

    private func save() {
        busy = true
        Task {
            if await app.auth.updateProfile(name: name, subtitle: subtitle) { dismiss() }
            busy = false
        }
    }
}

/// Os membros da comunidade numa folha (iPhone; no Mac é um painel à direita).
struct MembersSheet: View {
    @Environment(\.dismiss) private var dismiss
    var body: some View {
        VStack(spacing: 0) {
            HStack { Text("Membros").font(.system(size: 16, weight: .semibold)).foregroundColor(P.text); Spacer(); Button("Fechar") { dismiss() } }.padding(16)
            Divider().overlay(P.border)
            MembersView()
        }.background(P.main).sheetWidth()
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

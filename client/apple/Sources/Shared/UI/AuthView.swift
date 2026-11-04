import KurzKit
import SwiftUI

/// Entrar ou criar conta no servidor escolhido (o campo do servidor reaproveita a lista de servidores).
struct AuthView: View {
    @EnvironmentObject var app: AppModel
    @State private var registering = false
    @State private var serversOpen = false
    @State private var username = ""
    @State private var name = ""
    @State private var password = ""
    @State private var inviteCode = ""

    private var info: ServerInfo? { app.auth.info }
    private var registration: ServerInfo.Registration { info?.registration ?? .open }
    private var canSubmit: Bool {
        let u = username.trimmingCharacters(in: .whitespaces)
        if u.isEmpty || password.isEmpty || app.auth.busy || app.auth.incompatible || app.auth.info == nil { return false }
        if registering && registration == .invite && inviteCode.trimmingCharacters(in: .whitespaces).isEmpty { return false }
        return true
    }

    var body: some View {
        ScrollView {
            VStack(spacing: 16) {
                Image(systemName: "bubble.left.and.bubble.right.fill").font(.system(size: 34)).foregroundColor(P.accent).padding(.top, 32)
                Text("Kurz Community").font(.system(size: 22, weight: .semibold)).foregroundColor(P.text)
                serverRow
                if case .unreachable(let why) = app.auth.phase {
                    notice(why)
                    Button("Tentar de novo") { Task { await app.auth.restore() } }
                } else if app.auth.phase == .restoring {
                    ProgressView().controlSize(.small)
                } else {
                    form
                }
            }
            .frame(maxWidth: 360).padding(.horizontal, 16).padding(.bottom, 32).frame(maxWidth: .infinity)
        }
        .background(P.main)
        .sheet(isPresented: $serversOpen) { ServersSheet() }
    }

    private var serverRow: some View {
        Button { serversOpen = true } label: {
            HStack(spacing: 10) {
                Avatar(name: app.servers.label, hue: app.servers.hue, size: 32, rounded: 8)
                VStack(alignment: .leading, spacing: 1) {
                    Text(info?.name ?? app.servers.label).font(.system(size: 14, weight: .semibold)).foregroundColor(P.text).lineLimit(1)
                    Text(app.servers.label).font(.system(size: 11.5)).foregroundColor(P.textFaint).lineLimit(1)
                }
                Spacer()
                HealthDot(ok: app.servers.health[app.servers.selected])
                Image(systemName: "chevron.down").font(.system(size: 11)).foregroundColor(P.textMuted)
            }.padding(10).background(P.input, in: RoundedRectangle(cornerRadius: 10)).contentShape(Rectangle())
        }.buttonStyle(.plain)
    }

    @ViewBuilder private var form: some View {
        Picker("", selection: $registering) {
            Text("Entrar").tag(false)
            Text("Criar conta").tag(true)
        }.pickerStyle(.segmented).labelsHidden().onChange(of: registering) { _ in app.auth.error = nil }
        VStack(spacing: 10) {
            TextField("Nome de utilizador", text: $username).textFieldStyle(.roundedBorder).plainInput()
            if registering { TextField("Nome (opcional)", text: $name).textFieldStyle(.roundedBorder) }
            SecureField("Palavra-passe", text: $password).textFieldStyle(.roundedBorder).onSubmit(submit)
            if registering && registration != .open {
                TextField(registration == .invite ? "Código de convite" : "Código de convite (se tiveres)", text: $inviteCode)
                    .textFieldStyle(.roundedBorder).plainInput()
            }
        }
        if registering && registration == .closed {
            Text("O registo está fechado neste servidor; só com um código de convite.").font(.system(size: 12)).foregroundColor(P.textFaint)
        }
        if let e = app.auth.error { notice(e) }
        Button(action: submit) {
            Group { if app.auth.busy { ProgressView().controlSize(.small) } else { Text(registering ? "Criar conta" : "Entrar") } }
                .frame(maxWidth: .infinity).frame(height: 32)
        }.buttonStyle(.borderedProminent).disabled(!canSubmit)
    }

    private func notice(_ text: String) -> some View {
        HStack(alignment: .top, spacing: 8) {
            Image(systemName: "exclamationmark.circle").foregroundColor(P.danger)
            Text(text).font(.system(size: 12.5)).foregroundColor(P.text).multilineTextAlignment(.leading)
            Spacer(minLength: 0)
        }.padding(10).background(P.danger.opacity(0.12), in: RoundedRectangle(cornerRadius: 8))
    }

    private func submit() {
        guard canSubmit else { return }
        Task {
            if registering { await app.auth.register(username: username, name: name, password: password, inviteCode: inviteCode) }
            else { await app.auth.login(username: username, password: password) }
            if app.auth.phase == .signedIn { password = "" }
        }
    }
}

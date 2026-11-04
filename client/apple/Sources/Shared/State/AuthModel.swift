import Foundation
import KurzKit

/// A sessão no servidor escolhido: restaurar do Keychain, entrar, registar, sair. O utilizador (id e nome) vem do servidor (`me()`).
/// Cada servidor tem os seus tokens (KeychainTokenStore por baseURL); trocar de servidor troca de sessão.
@MainActor
final class AuthModel: ObservableObject {
    enum Phase: Equatable {
        /// A ler o Keychain e a perguntar ao servidor quem somos.
        case restoring
        case signedOut
        /// Há sessão guardada mas o servidor não respondeu: os tokens ficam, dá para tentar de novo.
        case unreachable(String)
        case signedIn
    }

    /// Versão maior do contrato que este cliente fala.
    static let contractMajor = 0

    @Published private(set) var phase = Phase.restoring
    @Published private(set) var user: User?
    @Published private(set) var info: ServerInfo?
    @Published private(set) var busy = false
    /// Erro do último pedido de entrada/registo, ou o motivo de ter voltado ao ecrã de entrada.
    @Published var error: String?

    private(set) var api: KurzAPI
    private var watch: Task<Void, Never>?
    private var generation = 0

    init(baseURL: String) {
        api = KurzAPI(baseURL: baseURL)
    }

    var baseURL: String { api.baseURL }
    var incompatible: Bool { info.map { $0.contractMajor != Self.contractMajor } ?? false }

    /// Passa a usar [baseURL]: outra sessão (outros tokens) e outro `wellKnown`.
    func use(_ baseURL: String) {
        guard KurzAPI(baseURL: baseURL).baseURL != api.baseURL else { return }
        api = KurzAPI(baseURL: baseURL)
        Task { await restore() }
    }

    func restore() async {
        generation += 1
        let gen = generation
        watch?.cancel()
        phase = .restoring; user = nil; info = nil; error = nil
        let api = self.api
        // Reage a "a sessão acabou" (refresh recusado) em qualquer parte da app.
        watch = Task { [weak self] in
            for await s in api.authStates() {
                if case .unauthenticated(let reason) = s { self?.sessionEnded(reason, api: api) }
            }
        }
        do {
            let info = try await api.wellKnown()
            guard gen == generation else { return }
            self.info = info
            if incompatible { phase = .signedOut; error = "Este servidor fala outra versão do contrato (\(info.contract))."; return }
        } catch {
            guard gen == generation else { return }
            let hasTokens = await api.restoreSession()
            phase = hasTokens ? .unreachable(error.localizedDescription) : .signedOut
            self.error = hasTokens ? nil : error.localizedDescription
            return
        }
        guard await api.restoreSession() else { guard gen == generation else { return }; phase = .signedOut; return }
        do {
            let me = try await api.me()
            guard gen == generation else { return }
            user = me; phase = .signedIn
        } catch let e as KurzError where e.isNetwork {
            guard gen == generation else { return }
            phase = .unreachable(e.message)
        } catch let e as KurzError where e.isUnauthenticated {
            guard gen == generation else { return }
            phase = .signedOut
        } catch {
            guard gen == generation else { return }
            phase = .unreachable(error.localizedDescription)
        }
    }

    func login(username: String, password: String) async {
        await run { [api] in try await api.login(username: username.trimmingCharacters(in: .whitespaces), password: password) }
    }

    func register(username: String, name: String, password: String, inviteCode: String) async {
        let n = name.trimmingCharacters(in: .whitespaces), code = inviteCode.trimmingCharacters(in: .whitespaces)
        await run { [api] in
            try await api.register(username: username.trimmingCharacters(in: .whitespaces), password: password,
                                   name: n.isEmpty ? nil : n, inviteCode: code.isEmpty ? nil : code)
        }
    }

    private func run(_ body: () async throws -> Session) async {
        guard !busy else { return }
        busy = true; error = nil
        defer { busy = false }
        do {
            let s = try await body()
            user = s.user; phase = .signedIn
        } catch {
            self.error = error.localizedDescription
        }
    }

    /// Sai da sessão; [voiceChannelId] (se estava numa sala) é deixado no servidor ANTES de os tokens serem apagados.
    func logout(leavingVoice voiceChannelId: String? = nil) async {
        await api.logout(leavingVoice: voiceChannelId)
        user = nil; phase = .signedOut; error = nil
    }

    /// Guarda o perfil no servidor; false (com erro em [error]) se falhou.
    func updateProfile(name: String, subtitle: String) async -> Bool {
        do {
            user = try await api.updateMe(name: name.trimmingCharacters(in: .whitespaces), subtitle: subtitle.trimmingCharacters(in: .whitespaces))
            return true
        } catch {
            self.error = error.localizedDescription
            return false
        }
    }

    /// O refresh foi recusado ou saímos: volta ao ecrã de entrada (ignora sessões de um servidor que já não é o atual).
    private func sessionEnded(_ reason: AuthState.Reason, api ended: KurzAPI) {
        guard ended === api, phase == .signedIn else { return }
        user = nil; phase = .signedOut
        if reason == .sessionExpired { error = "A tua sessão expirou. Entra novamente." }
    }
}

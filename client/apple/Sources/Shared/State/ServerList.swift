import Foundation
import KurzKit

/// Servidores Kurz: o de fábrica primeiro, os adicionados guardados entre arranques, e o ligado. A saúde de cada um vem do
/// `/.well-known/kurz`: o ligado sempre, os outros só enquanto a lista está aberta.
@MainActor
final class ServerList: ObservableObject {
    /// Servidor local de desenvolvimento (`make db-up` + `go run ./cmd/kurz` em server/). O cord.kurz.fyi fala o contrato antigo.
    static let factory = "http://localhost:8080"
    private static let legacy = "https://cord.kurz.fyi"

    @Published private(set) var servers: [String]
    @Published private(set) var selected: String
    /// servidor -> responde ao well-known; sem entrada = ainda por saber
    @Published private(set) var health: [String: Bool] = [:]
    @Published private(set) var info: [String: ServerInfo] = [:]

    private let defaults = UserDefaults.standard
    private var listOpen = false
    private var checks: Task<Void, Never>?

    init() {
        let added = (defaults.stringArray(forKey: "servers") ?? []).filter { $0 != Self.factory && $0 != Self.legacy }
        let all = [Self.factory] + added
        servers = all
        selected = defaults.string(forKey: "server").flatMap { all.contains($0) ? $0 : nil } ?? Self.factory
    }

    var label: String { serverLabel(selected) }
    /// Matiz do avatar do servidor; estável entre arranques (o hashValue do Swift não é).
    var hue: Double { stableHue(selected) }

    func setListOpen(_ open: Bool) {
        listOpen = open
        if open { startChecks() }
    }

    func select(_ url: String) {
        guard url != selected else { return }
        selected = url
        defaults.set(url, forKey: "server")
        startChecks()
    }

    /// Adiciona o servidor escrito e liga-se a ele; false se não for um endereço.
    func add(_ input: String) -> Bool {
        guard let url = normalizeServer(input) else { return false }
        if !servers.contains(url) { servers.append(url); save() }
        select(url)
        return true
    }

    /// Tira um servidor adicionado (o de fábrica fica); se era o ligado, volta ao de fábrica.
    func remove(_ url: String) {
        guard url != Self.factory else { return }
        servers.removeAll { $0 == url }
        save()
        if selected == url { select(Self.factory) }
    }

    /// Sonda o well-known a cada 5 s. Idempotente: reinicia o ciclo (mudou o servidor ou abriu a lista).
    func startChecks() {
        checks?.cancel()
        checks = Task { [weak self] in
            while !Task.isCancelled {
                guard let self else { return }
                let urls = self.listOpen ? self.servers : [self.selected]
                await withTaskGroup(of: (String, ServerInfo?).self) { group in
                    for u in urls {
                        group.addTask { (u, try? await KurzAPI(baseURL: u, store: InMemoryTokenStore()).wellKnown()) }
                    }
                    for await (u, i) in group { self.health[u] = i != nil; self.info[u] = i }
                }
                try? await Task.sleep(nanoseconds: 5_000_000_000)
            }
        }
    }

    private func save() { defaults.set(servers.filter { $0 != Self.factory }, forKey: "servers") }
}

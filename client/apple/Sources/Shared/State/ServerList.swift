import Foundation

/// Servidores (managers): o de fábrica primeiro, os adicionados guardados entre arranques, e o ligado. A saúde de cada um
/// vem do /healthz: o ligado sempre, os outros só enquanto a lista está aberta.
@MainActor
final class ServerList: ObservableObject {
    #if DEBUG
    static let factory = "http://localhost:30080"
    #else
    static let factory = "https://cord.kurz.fyi"
    #endif

    @Published private(set) var servers: [String]
    @Published private(set) var selected: String
    /// servidor -> responde ao /healthz; sem entrada = ainda por saber
    @Published private(set) var health: [String: Bool] = [:]

    private let defaults = UserDefaults.standard
    private var listOpen = false
    private var checks: Task<Void, Never>?

    init() {
        let added = (defaults.stringArray(forKey: "servers") ?? []).filter { $0 != Self.factory }
        let all = [Self.factory] + added
        servers = all
        selected = defaults.string(forKey: "server").flatMap { all.contains($0) ? $0 : nil } ?? Self.factory
    }

    var label: String { serverLabel(selected) }
    /// Matiz do avatar do servidor; estável entre arranques (o hashValue do Swift não é).
    var hue: Double { Double(selected.unicodeScalars.reduce(0) { ($0 &* 31 &+ Int($1.value)) & 0x7fffffff } % 360) }

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

    /// Sonda o /healthz a cada 5 s. Idempotente: reinicia o ciclo (mudou o servidor ou abriu a lista).
    func startChecks() {
        checks?.cancel()
        checks = Task { [weak self] in
            while !Task.isCancelled {
                guard let self else { return }
                let urls = self.listOpen ? self.servers : [self.selected]
                await withTaskGroup(of: (String, Bool).self) { group in
                    for u in urls { group.addTask { (u, await Manager(u).healthy()) } }
                    for await (u, ok) in group { self.health[u] = ok }
                }
                try? await Task.sleep(nanoseconds: 5_000_000_000)
            }
        }
    }

    private func save() { defaults.set(servers.filter { $0 != Self.factory }, forKey: "servers") }
}

import Foundation

/// As salas do servidor ligado, actualizadas a cada 3 s.
@MainActor
final class RoomList: ObservableObject {
    @Published private(set) var rooms: [Room] = []
    /// Erro da última listagem (manager fora, URL errada); nil se correu bem.
    @Published private(set) var error: String?

    private var base = ""
    private var loop: Task<Void, Never>?

    /// Passa a listar as salas de [base] (outro servidor: nada das salas do anterior).
    func follow(_ base: String) {
        if base != self.base { self.base = base; rooms = []; error = nil }
        loop?.cancel()
        loop = Task { [weak self] in
            try? await Task.sleep(nanoseconds: 300_000_000)  // espera que se acabe de escrever a URL
            while !Task.isCancelled {
                await self?.refresh(base)
                try? await Task.sleep(nanoseconds: 3_000_000_000)
            }
        }
    }

    private func refresh(_ base: String) async {
        do {
            let list = try await Manager(base).rooms()
            guard base == self.base else { return }
            rooms = list; error = nil
        } catch {
            guard base == self.base else { return }
            self.error = error.localizedDescription
        }
    }

    /// Cria a sala; já entra na lista (a UI mostra a sala que se vai abrir sem esperar pela listagem seguinte).
    func create(_ name: String) async throws -> Room {
        let room = try await Manager(base).create(name)
        rooms = rooms.filter { $0.id != room.id } + [room]
        return room
    }
}

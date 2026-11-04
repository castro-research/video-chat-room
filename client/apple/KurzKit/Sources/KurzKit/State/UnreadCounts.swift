import Foundation

/// Não-lidos por canal (mensagens de outros autores depois da última lida; o servidor limita a 1000).
public struct UnreadCounts: Equatable, Sendable {
    public private(set) var counts: [String: Int] = [:]
    public init() {}

    public func count(_ channelId: String) -> Int { counts[channelId] ?? 0 }
    public func total(in channels: [Channel]) -> Int { channels.reduce(0) { $0 + count($1.id) } }

    /// Estado vindo do REST para estes canais: o que não vem é 0.
    public mutating func set(_ states: [ReadState], for channels: [Channel]) {
        for c in channels { counts[c.id] = nil }
        for s in states where s.unread > 0 { counts[s.channelId] = min(s.unread, 1000) }
    }
    public mutating func increment(_ channelId: String) { counts[channelId] = min(count(channelId) + 1, 1000) }
    public mutating func clear(_ channelId: String) { counts[channelId] = nil }
}

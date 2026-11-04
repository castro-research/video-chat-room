import Foundation

/// Uma categoria (ou, para os canais soltos, `nil`) com os seus canais já ordenados.
public struct ChannelSection: Hashable, Sendable, Identifiable {
    public var category: Channel?
    public var channels: [Channel]
    public var id: String { category?.id ?? "loose" }
}

public enum ChannelTree {
    private static func order(_ a: Channel, _ b: Channel) -> Bool { a.position != b.position ? a.position < b.position : idLess(a.id, b.id) }

    /// Canais soltos primeiro, depois cada categoria (por `position`) com os filhos (por `position`). Canais de tipo desconhecido saem;
    /// um filho cuja categoria não existe (ou que o utilizador não vê) vai para os soltos. Categorias vazias ficam (dá para criar canais nelas).
    public static func sections(_ channels: [Channel]) -> [ChannelSection] {
        let usable = channels.filter { $0.kind != .unknown }
        let categories = usable.filter { $0.kind == .category }.sorted(by: order)
        let ids = Set(categories.map(\.id))
        let loose = usable.filter { $0.kind != .category && ($0.parentId.map { !ids.contains($0) } ?? true) }.sorted(by: order)
        var out: [ChannelSection] = []
        if !loose.isEmpty { out.append(ChannelSection(category: nil, channels: loose)) }
        for c in categories {
            out.append(ChannelSection(category: c, channels: usable.filter { $0.kind != .category && $0.parentId == c.id }.sorted(by: order)))
        }
        return out
    }

    /// O primeiro canal de texto na ordem da lista (o que abrir ao entrar numa guild).
    public static func firstText(_ channels: [Channel]) -> Channel? {
        sections(channels).lazy.flatMap(\.channels).first { $0.kind == .text }
    }

    public static func upsert(_ c: Channel, in list: inout [Channel]) {
        if let i = list.firstIndex(where: { $0.id == c.id }) { list[i] = c } else { list.append(c) }
    }
}

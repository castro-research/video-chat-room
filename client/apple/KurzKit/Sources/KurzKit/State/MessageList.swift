import Foundation

/// Ids são snowflakes decimais como texto: crescem com o tempo; comparar por tamanho e depois lexicograficamente ordena como número.
public func idLess(_ a: String, _ b: String) -> Bool { a.count != b.count ? a.count < b.count : a < b }

/// Mensagem enviada pelo utilizador que o servidor ainda não confirmou (envio otimista). Casa com a real pelo `nonce`.
public struct PendingMessage: Hashable, Sendable, Identifiable {
    public var nonce: String
    public var text: String
    public var replyToId: String?
    public var createdAt: Date
    /// O envio falhou: a UI oferece tentar de novo ou descartar.
    public var failed = false
    public var id: String { nonce }

    public init(nonce: String, text: String, replyToId: String? = nil, createdAt: Date = Date()) {
        self.nonce = nonce; self.text = text; self.replyToId = replyToId; self.createdAt = createdAt
    }
}

public enum MessageRow: Hashable, Sendable, Identifiable {
    case sent(Message)
    case sending(PendingMessage)
    public var id: String {
        switch self {
        case .sent(let m): return m.id
        case .sending(let p): return "pending:" + p.nonce
        }
    }
}

/// As mensagens carregadas de um canal, da mais antiga para a mais nova, mais as otimistas. Tudo idempotente (upsert/remove por id):
/// um evento do gateway pode refletir algo que o REST já devolveu.
public struct MessageList: Equatable, Sendable {
    public var messages: [Message] = []
    public var pending: [PendingMessage] = []
    /// Há mensagens mais antigas no servidor que a primeira carregada.
    public var hasMore = false
    /// Já veio a primeira página.
    public var loaded = false

    public init() {}

    public var firstId: String? { messages.first?.id }
    public var lastId: String? { messages.last?.id }
    public func message(_ id: String) -> Message? { messages.first { $0.id == id } }
    public func contains(_ id: String) -> Bool { messages.contains { $0.id == id } }

    public var rows: [MessageRow] { messages.map(MessageRow.sent) + pending.map(MessageRow.sending) }

    /// A página mais nova (primeira abertura ou refetch depois de `ready`): substitui o que havia. As otimistas ficam.
    public mutating func replace(with page: MessagePage) {
        messages = page.items.sorted { idLess($0.id, $1.id) }
        hasMore = page.hasMore
        loaded = true
        let nonces = Set(messages.compactMap(\.nonce))
        pending.removeAll { nonces.contains($0.nonce) }
    }

    /// Página mais antiga (rolar para cima).
    public mutating func prepend(_ page: MessagePage) {
        for m in page.items { upsert(m) }
        hasMore = page.hasMore
    }

    /// Mensagem nova ou atualizada. Se traz o `nonce` de uma otimista, esta sai.
    public mutating func upsert(_ m: Message) {
        if let n = m.nonce { pending.removeAll { $0.nonce == n } }
        if let i = messages.firstIndex(where: { $0.id == m.id }) { messages[i] = m; return }
        let at = messages.firstIndex { idLess(m.id, $0.id) } ?? messages.endIndex
        messages.insert(m, at: at)
    }

    public mutating func apply(_ u: MessageUpdate) {
        guard let i = messages.firstIndex(where: { $0.id == u.id }) else { return }
        messages[i] = messages[i].applying(u)
    }

    public mutating func remove(id: String) { messages.removeAll { $0.id == id } }

    /// `message.create` e REST podem dar a mesma reação duas vezes; a do próprio não conta de novo.
    public mutating func addReaction(_ e: ReactionEvent, me: String) {
        guard let i = messages.firstIndex(where: { $0.id == e.messageId }) else { return }
        let mine = e.userId == me
        if let r = messages[i].reactions.firstIndex(where: { $0.emoji == e.emoji }) {
            if mine && messages[i].reactions[r].me { return }
            messages[i].reactions[r].count += 1
            if mine { messages[i].reactions[r].me = true }
        } else {
            messages[i].reactions.append(Reaction(emoji: e.emoji, count: 1, me: mine))
        }
    }

    public mutating func removeReaction(_ e: ReactionEvent, me: String) {
        guard let i = messages.firstIndex(where: { $0.id == e.messageId }),
              let r = messages[i].reactions.firstIndex(where: { $0.emoji == e.emoji }) else { return }
        let mine = e.userId == me
        if mine && !messages[i].reactions[r].me { return }
        messages[i].reactions[r].count -= 1
        if mine { messages[i].reactions[r].me = false }
        if messages[i].reactions[r].count <= 0 { messages[i].reactions.remove(at: r) }
    }

    // MARK: Envio otimista

    public mutating func addPending(_ p: PendingMessage) { pending.removeAll { $0.nonce == p.nonce }; pending.append(p) }
    public mutating func failPending(nonce: String) { if let i = pending.firstIndex(where: { $0.nonce == nonce }) { pending[i].failed = true } }
    public mutating func retryPending(nonce: String) { if let i = pending.firstIndex(where: { $0.nonce == nonce }) { pending[i].failed = false } }
    public mutating func dropPending(nonce: String) { pending.removeAll { $0.nonce == nonce } }
}

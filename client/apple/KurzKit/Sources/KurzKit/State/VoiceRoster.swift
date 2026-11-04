import Foundation

/// Quem está em cada canal de voz: vem do `ready.voice`, de `GET .../voice` e dos eventos `voice.state` (sem poll).
public struct VoiceRoster: Equatable, Sendable {
    public private(set) var byChannel: [String: [VoiceParticipant]] = [:]
    public init() {}

    public func participants(in channelId: String) -> [VoiceParticipant] { byChannel[channelId] ?? [] }
    public func channel(of userId: String) -> String? { byChannel.first { $0.value.contains { $0.id == userId } }?.key }

    /// Estado completo (o `ready`): substitui tudo.
    public mutating func reset(_ states: [VoiceState]) {
        byChannel = [:]
        for s in states { apply(s) }
    }

    /// Lista completa de um canal (REST).
    public mutating func set(_ participants: [VoiceParticipant], in channelId: String) {
        byChannel[channelId] = participants.isEmpty ? nil : participants
    }

    /// Entrou/mudou (upsert por usuário) ou saiu. Entrar num canal tira o usuário dos outros (mudar de canal é sair e entrar).
    public mutating func apply(_ s: VoiceState) {
        let uid = s.participant.id
        if s.connected {
            for (cid, list) in byChannel where cid != s.channelId && list.contains(where: { $0.id == uid }) { remove(uid, from: cid) }
            var list = byChannel[s.channelId] ?? []
            if let i = list.firstIndex(where: { $0.id == uid }) { list[i] = s.participant } else { list.append(s.participant) }
            byChannel[s.channelId] = list
        } else {
            remove(uid, from: s.channelId)
        }
    }

    public mutating func removeChannel(_ channelId: String) { byChannel[channelId] = nil }

    /// O usuário saiu da guild: sai de qualquer canal de voz.
    public mutating func removeUser(_ userId: String, in channelIds: Set<String>) {
        for cid in channelIds { remove(userId, from: cid) }
    }

    private mutating func remove(_ uid: String, from channelId: String) {
        guard var list = byChannel[channelId] else { return }
        list.removeAll { $0.id == uid }
        byChannel[channelId] = list.isEmpty ? nil : list
    }
}

import Foundation

public struct GuildSummary: Codable, Hashable, Sendable, Identifiable {
    public var id: String
    public var name: String
    public var ownerId: String
}

/// `ready`: sessão aberta. Depois dele, releia o estado por REST (canais, membros, mensagens).
public struct GatewayReady: Hashable, Sendable {
    public var sessionId: String
    public var userId: String
    public var guilds: [GuildSummary]
    public var heartbeatInterval: TimeInterval
    /// Estado de voz atual (só em servidores com `voice`).
    public var voice: [VoiceState]
}

extension GatewayReady: Decodable {
    private enum CodingKeys: String, CodingKey { case sessionId, user, guilds, heartbeatInterval, voice }
    private struct U: Decodable { var id: String }
    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        sessionId = try c.decode(String.self, forKey: .sessionId)
        userId = try c.decode(U.self, forKey: .user).id
        guilds = try c.decodeIfPresent([GuildSummary].self, forKey: .guilds) ?? []
        heartbeatInterval = (try c.decodeIfPresent(Double.self, forKey: .heartbeatInterval) ?? 30000) / 1000
        voice = try c.decodeIfPresent([VoiceState].self, forKey: .voice) ?? []
    }
}

public struct MessageUpdate: Codable, Hashable, Sendable {
    public var id: String
    public var channelId: String
    public var text: String
    public var editedAt: Date
}

public struct MessageDelete: Codable, Hashable, Sendable { public var id: String; public var channelId: String }

public struct ReactionEvent: Codable, Hashable, Sendable {
    public var channelId: String
    public var messageId: String
    public var userId: String
    public var emoji: String
}

public struct GuildCreateEvent: Codable, Hashable, Sendable { public var userId: String; public var guild: Guild }
public struct GuildDeleteEvent: Codable, Hashable, Sendable { public var id: String }

public struct MemberEvent: Codable, Hashable, Sendable {
    public var guildId: String
    public var userId: String
    public var member: Member
}

public struct MemberLeaveEvent: Codable, Hashable, Sendable {
    public var guildId: String
    public var userId: String
    /// `leave`, `kick` ou `ban`.
    public var reason: String
}

public struct RoleEvent: Codable, Hashable, Sendable { public var guildId: String; public var role: Role }
public struct RoleDeleteEvent: Codable, Hashable, Sendable { public var guildId: String; public var roleId: String }
public struct ChannelDeleteEvent: Codable, Hashable, Sendable { public var id: String; public var guildId: String }

public struct ChannelPermissionsEvent: Codable, Hashable, Sendable {
    public var channelId: String
    public var guildId: String
    public var override: PermissionOverride
}

/// Evento do gateway já tipado. Aplique de forma idempotente (upsert/remove por id): um evento pode refletir algo que o REST já devolveu.
public enum GatewayEvent: Sendable, Equatable {
    // Controle
    /// Sessão nova (primeira ou depois de `invalidSession`): refaça o fetch por REST.
    case ready(GatewayReady)
    /// Sessão retomada; os eventos perdidos chegam em seguida. Nada a refazer.
    case resumed
    /// O servidor não conseguiu retomar (sessão expirada ou buffer estourado). O cliente já fez `identify`; um `ready` vem a seguir.
    case invalidSession

    case messageCreate(Message)
    case messageUpdate(MessageUpdate)
    case messageDelete(MessageDelete)
    case reactionAdd(ReactionEvent)
    case reactionRemove(ReactionEvent)
    case guildCreate(GuildCreateEvent)
    case guildUpdate(Guild)
    case guildDelete(GuildDeleteEvent)
    case memberJoin(MemberEvent)
    case memberLeave(MemberLeaveEvent)
    case memberUpdate(MemberEvent)
    case roleCreate(RoleEvent)
    case roleUpdate(RoleEvent)
    case roleDelete(RoleDeleteEvent)
    case channelCreate(Channel)
    case channelUpdate(Channel)
    case channelDelete(ChannelDeleteEvent)
    case channelPermissions(ChannelPermissionsEvent)
    case voiceState(VoiceState)
    /// Evento que este cliente não conhece, ou cujo formato mudou: `name` é o `t` do servidor.
    case unknown(name: String, data: JSONValue)

    static func decode(name: String, data: JSONValue) -> GatewayEvent {
        let dec = KurzJSON.decoder()
        guard let raw = try? KurzJSON.encoder().encode(data) else { return .unknown(name: name, data: data) }
        func d<T: Decodable>(_ f: (T) -> GatewayEvent) -> GatewayEvent {
            (try? dec.decode(T.self, from: raw)).map(f) ?? .unknown(name: name, data: data)
        }
        switch name {
        case "message.create": return d { .messageCreate($0) }
        case "message.update": return d { .messageUpdate($0) }
        case "message.delete": return d { .messageDelete($0) }
        case "reaction.add": return d { .reactionAdd($0) }
        case "reaction.remove": return d { .reactionRemove($0) }
        case "guild.create": return d { .guildCreate($0) }
        case "guild.update": return d { .guildUpdate($0) }
        case "guild.delete": return d { .guildDelete($0) }
        case "member.join": return d { .memberJoin($0) }
        case "member.leave": return d { .memberLeave($0) }
        case "member.update": return d { .memberUpdate($0) }
        case "role.create": return d { .roleCreate($0) }
        case "role.update": return d { .roleUpdate($0) }
        case "role.delete": return d { .roleDelete($0) }
        case "channel.create": return d { .channelCreate($0) }
        case "channel.update": return d { .channelUpdate($0) }
        case "channel.delete": return d { .channelDelete($0) }
        case "channel.permissions": return d { .channelPermissions($0) }
        case "voice.state": return d { .voiceState($0) }
        default: return .unknown(name: name, data: data)
        }
    }
}

public extension Message {
    /// Aplica um `message.update` a uma cópia local.
    func applying(_ u: MessageUpdate) -> Message {
        var m = self
        m.text = u.text; m.editedAt = u.editedAt
        return m
    }
}

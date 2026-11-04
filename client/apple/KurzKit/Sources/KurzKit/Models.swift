import Foundation

// Modelos do contrato 0.6 (contract/openapi.yaml). Ids são strings opacas (snowflake). Campos opcionais do contrato são opcionais aqui;
// listas que o contrato exige mas que um servidor mais antigo pode omitir caem para [] em vez de falhar o decode.

// MARK: - Discovery

public struct ServerInfo: Codable, Hashable, Sendable {
    public enum Registration: String, Codable, Sendable { case open, invite, closed }
    public var contract: String
    public var name: String
    public var hue: Double?
    public var iconUrl: String?
    public var registration: Registration
    /// Strings abertas (`text`, `realtime`, `search`, `voice`, `video`, `screenshare`, `attachments`).
    public var capabilities: [String]

    public var contractMajor: Int? { Int(contract.split(separator: ".").first ?? "") }
    public func supports(_ capability: String) -> Bool { capabilities.contains(capability) }
    public var supportsVoice: Bool { supports("voice") }
    public var supportsRealtime: Bool { supports("realtime") }
}

// MARK: - Users

public enum UserStatus: String, Codable, Hashable, Sendable {
    case online, idle, offline
    public init(from decoder: Decoder) throws {
        let s = try decoder.singleValueContainer().decode(String.self)
        self = UserStatus(rawValue: s) ?? .offline
    }
}

public struct User: Codable, Hashable, Sendable, Identifiable {
    public var id: String
    public var username: String
    public var name: String
    public var status: UserStatus
    public var subtitle: String?
    public var hue: Double?
    public var avatarUrl: String?

    public init(id: String, username: String, name: String, status: UserStatus = .offline, subtitle: String? = nil, hue: Double? = nil, avatarUrl: String? = nil) {
        self.id = id; self.username = username; self.name = name; self.status = status
        self.subtitle = subtitle; self.hue = hue; self.avatarUrl = avatarUrl
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        username = try c.decode(String.self, forKey: .username)
        name = try c.decode(String.self, forKey: .name)
        status = try c.decodeIfPresent(UserStatus.self, forKey: .status) ?? .offline
        subtitle = try c.decodeIfPresent(String.self, forKey: .subtitle)
        hue = try c.decodeIfPresent(Double.self, forKey: .hue)
        avatarUrl = try c.decodeIfPresent(String.self, forKey: .avatarUrl)
    }
}

/// Resposta de register/login/refresh.
public struct Session: Codable, Hashable, Sendable {
    public var accessToken: String
    public var refreshToken: String
    /// Segundos até o `accessToken` expirar.
    public var expiresIn: Int
    public var user: User
}

// MARK: - Guilds

public struct Permissions: OptionSet, Codable, Hashable, Sendable {
    public let rawValue: Int64
    public init(rawValue: Int64) { self.rawValue = rawValue }
    public init(from decoder: Decoder) throws { rawValue = try decoder.singleValueContainer().decode(Int64.self) }
    public func encode(to encoder: Encoder) throws { var c = encoder.singleValueContainer(); try c.encode(rawValue) }

    public static let viewChannel = Permissions(rawValue: 1)
    public static let sendMessages = Permissions(rawValue: 2)
    public static let manageMessages = Permissions(rawValue: 4)
    public static let manageChannels = Permissions(rawValue: 8)
    public static let createInvite = Permissions(rawValue: 16)
    public static let kickMembers = Permissions(rawValue: 32)
    public static let banMembers = Permissions(rawValue: 64)
    public static let connect = Permissions(rawValue: 128)
    public static let speak = Permissions(rawValue: 256)
    public static let manageRoles = Permissions(rawValue: 512)
    public static let administrator = Permissions(rawValue: 1024)

    /// `administrator` vale por todas as outras.
    public func allows(_ p: Permissions) -> Bool { contains(.administrator) || contains(p) }
}

public struct Guild: Codable, Hashable, Sendable, Identifiable {
    public var id: String
    public var name: String
    public var ownerId: String
    public var createdAt: Date
}

public struct Role: Codable, Hashable, Sendable, Identifiable {
    public var id: String
    public var name: String
    public var permissions: Permissions
    /// Maior = mais poder. @everyone é 0.
    public var position: Int
    public var everyone: Bool
}

public struct Member: Codable, Hashable, Sendable, Identifiable {
    public var user: User
    public var joinedAt: Date
    /// Cargos do membro, sem o @everyone implícito.
    public var roleIds: [String]
    public var id: String { user.id }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        user = try c.decode(User.self, forKey: .user)
        joinedAt = try c.decode(Date.self, forKey: .joinedAt)
        roleIds = try c.decodeIfPresent([String].self, forKey: .roleIds) ?? []
    }
}

public struct MemberPage: Codable, Hashable, Sendable {
    public var items: [Member]
    public var total: Int
    /// Ausente na última página.
    public var next: String?
}

public struct Invite: Codable, Hashable, Sendable {
    public var code: String
    public var guildId: String
    public var creatorId: String
    public var maxUses: Int?
    public var uses: Int
    public var expiresAt: Date?
    public var createdAt: Date
}

public struct InvitePreview: Codable, Hashable, Sendable {
    public var code: String
    public var expiresAt: Date?
    public var guild: Guild
}

// MARK: - Channels

public enum ChannelKind: String, Codable, Hashable, Sendable {
    case text, voice, category
    /// Tipo que este cliente não conhece (servidor mais novo): a UI deve ignorar o canal.
    case unknown

    public init(from decoder: Decoder) throws {
        let s = try decoder.singleValueContainer().decode(String.self)
        self = ChannelKind(rawValue: s) ?? .unknown
    }
}

public struct Channel: Codable, Hashable, Sendable, Identifiable {
    public var id: String
    public var guildId: String
    public var name: String
    public var kind: ChannelKind
    /// A categoria onde está. Ausente em categorias e canais soltos.
    public var parentId: String?
    /// Ordem entre canais com o mesmo `parentId`.
    public var position: Int
}

public struct PermissionOverride: Codable, Hashable, Sendable {
    public var kind: String
    public var targetId: String
    public var allow: Permissions
    public var deny: Permissions
}

// MARK: - Messages

public struct Reaction: Codable, Hashable, Sendable {
    public var emoji: String
    public var count: Int
    /// O usuário autenticado reagiu com este emoji.
    public var me: Bool
}

public struct Attachment: Codable, Hashable, Sendable, Identifiable {
    public var id: String
    /// GET pré-assinado, expira em 1 h: busque a mensagem de novo para renovar.
    public var url: String
    public var contentType: String
    public var name: String?
    public var size: Int?
    public var width: Int?
    public var height: Int?
}

public struct Message: Codable, Hashable, Sendable, Identifiable {
    public var id: String
    public var channelId: String
    public var author: User
    public var createdAt: Date
    public var editedAt: Date?
    public var text: String
    public var replyToId: String?
    public var reactions: [Reaction]
    public var attachments: [Attachment]
    /// Eco do nonce enviado; só vem na mensagem do próprio remetente.
    public var nonce: String?

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        channelId = try c.decode(String.self, forKey: .channelId)
        author = try c.decode(User.self, forKey: .author)
        createdAt = try c.decode(Date.self, forKey: .createdAt)
        editedAt = try c.decodeIfPresent(Date.self, forKey: .editedAt)
        text = try c.decodeIfPresent(String.self, forKey: .text) ?? ""
        replyToId = try c.decodeIfPresent(String.self, forKey: .replyToId)
        reactions = try c.decodeIfPresent([Reaction].self, forKey: .reactions) ?? []
        attachments = try c.decodeIfPresent([Attachment].self, forKey: .attachments) ?? []
        nonce = try c.decodeIfPresent(String.self, forKey: .nonce)
    }
}

public struct MessagePage: Codable, Hashable, Sendable {
    /// Sempre do mais antigo para o mais novo.
    public var items: [Message]
    /// Há mensagens mais antigas que o primeiro item.
    public var hasMore: Bool
    /// Há mensagens mais novas que o último item.
    public var hasNewer: Bool
}

public struct ReadState: Codable, Hashable, Sendable {
    public var channelId: String
    public var lastReadMessageId: String?
    /// Mensagens de outros autores mais novas que `lastReadMessageId`, no máximo 1000.
    public var unread: Int
}

// MARK: - Voice

public struct IceServer: Codable, Hashable, Sendable {
    public var urls: [String]
    public var username: String?
    public var credential: String?
}

public struct VoiceParticipant: Codable, Hashable, Sendable, Identifiable {
    public var user: User
    public var muted: Bool
    public var camOn: Bool
    /// Compartilhando a tela.
    public var sharing: Bool
    public var id: String { user.id }

    public init(user: User, muted: Bool = false, camOn: Bool = false, sharing: Bool = false) {
        self.user = user; self.muted = muted; self.camOn = camOn; self.sharing = sharing
    }
}

/// Resposta de `POST /v1/channels/{id}/voice/join`. Mesmos campos que o `VoiceSession` do app (media.url/token, iceServers),
/// mais `participants`.
public struct VoiceSession: Codable, Sendable {
    public struct Media: Codable, Hashable, Sendable {
        public var url: String
        public var token: String
        public init(url: String, token: String) { self.url = url; self.token = token }
    }
    public var media: Media
    public var iceServers: [IceServer]
    public var participants: [VoiceParticipant]

    public init(media: Media, iceServers: [IceServer] = [], participants: [VoiceParticipant] = []) {
        self.media = media; self.iceServers = iceServers; self.participants = participants
    }

    public init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        media = try c.decode(Media.self, forKey: .media)
        iceServers = try c.decodeIfPresent([IceServer].self, forKey: .iceServers) ?? []
        participants = try c.decodeIfPresent([VoiceParticipant].self, forKey: .participants) ?? []
    }
}

/// Payload do evento `voice.state` (e de `ready.voice`): alguém entrou, saiu ou mudou mute/câmera/tela.
public struct VoiceState: Codable, Hashable, Sendable {
    public var channelId: String
    public var connected: Bool
    public var participant: VoiceParticipant
}

// MARK: - Errors on the wire

public struct ApiError: Codable, Hashable, Sendable {
    public var code: String
    public var message: String
}

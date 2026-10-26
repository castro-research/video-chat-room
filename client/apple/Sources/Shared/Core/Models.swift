import Foundation

// Contrato do manager e do SFU (contract/openapi.yaml, experimentos/README.md). Os campos em falta têm valor por omissão.

struct Participant: Codable, Hashable {
    let userId: String
    let name: String
    var mic = false
    var camera = false
    var screen = false

    init(userId: String, name: String, mic: Bool = false, camera: Bool = false, screen: Bool = false) {
        self.userId = userId; self.name = name; self.mic = mic; self.camera = camera; self.screen = screen
    }

    init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        userId = try c.decode(String.self, forKey: .userId)
        name = try c.decode(String.self, forKey: .name)
        mic = try c.decodeIfPresent(Bool.self, forKey: .mic) ?? false
        camera = try c.decodeIfPresent(Bool.self, forKey: .camera) ?? false
        screen = try c.decodeIfPresent(Bool.self, forKey: .screen) ?? false
    }
}

struct Room: Codable, Hashable, Identifiable {
    let id: String
    let name: String
    var participants: [Participant] = []

    init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = try c.decode(String.self, forKey: .name)
        participants = try c.decodeIfPresent([Participant].self, forKey: .participants) ?? []
    }
}

struct IceServer: Codable, Hashable {
    let urls: [String]
    var username: String?
    var credential: String?
}

/// Resposta de `POST /v1/rooms/{id}/join`.
struct VoiceSession: Codable {
    struct Media: Codable { let url: String; let token: String }
    let media: Media
    var iceServers: [IceServer] = []

    init(from d: Decoder) throws {
        let c = try d.container(keyedBy: CodingKeys.self)
        media = try c.decode(Media.self, forKey: .media)
        iceServers = try c.decodeIfPresent([IceServer].self, forKey: .iceServers) ?? []
    }
}

struct RoomListResponse: Codable { let rooms: [Room] }
struct ApiError: Codable { let code: String; let message: String }

/// Erro com a mensagem em português que a UI mostra.
struct KurzError: LocalizedError {
    let message: String
    init(_ message: String) { self.message = message }
    var errorDescription: String? { message }
}

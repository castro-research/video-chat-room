import Foundation
import Security

/// Tokens de uma sessão. `expiresAt` é calculado no cliente a partir de `expiresIn`.
public struct StoredTokens: Codable, Hashable, Sendable {
    public var accessToken: String
    public var refreshToken: String
    public var expiresAt: Date
    public init(accessToken: String, refreshToken: String, expiresAt: Date) {
        self.accessToken = accessToken; self.refreshToken = refreshToken; self.expiresAt = expiresAt
    }
}

/// Guarda access+refresh por servidor (chave = base URL). Chamadas síncronas e baratas; implementações são thread-safe.
public protocol TokenStore: Sendable {
    func load(for baseURL: String) throws -> StoredTokens?
    func save(_ tokens: StoredTokens, for baseURL: String) throws
    func clear(for baseURL: String) throws
}

/// Para testes e para quem não quer persistir.
public final class InMemoryTokenStore: TokenStore, @unchecked Sendable {
    private let lock = NSLock()
    private var items: [String: StoredTokens] = [:]
    public init() {}
    public func load(for baseURL: String) throws -> StoredTokens? { lock.lock(); defer { lock.unlock() }; return items[baseURL] }
    public func save(_ tokens: StoredTokens, for baseURL: String) throws { lock.lock(); items[baseURL] = tokens; lock.unlock() }
    public func clear(for baseURL: String) throws { lock.lock(); items[baseURL] = nil; lock.unlock() }
}

/// Keychain (macOS e iOS): um item genérico por servidor, com o JSON dos tokens.
public final class KeychainTokenStore: TokenStore {
    public let service: String
    public init(service: String = "fyi.kurz.tokens") { self.service = service }

    private func query(_ baseURL: String) -> [String: Any] {
        [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service, kSecAttrAccount as String: baseURL]
    }

    private func fail(_ status: OSStatus) -> KurzError {
        KurzError("Não foi possível aceder ao Keychain (\(status)).", code: "keychain")
    }

    public func load(for baseURL: String) throws -> StoredTokens? {
        var q = query(baseURL)
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var out: CFTypeRef?
        let status = SecItemCopyMatching(q as CFDictionary, &out)
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess, let data = out as? Data else { throw fail(status) }
        return try? KurzJSON.decoder().decode(StoredTokens.self, from: data)
    }

    public func save(_ tokens: StoredTokens, for baseURL: String) throws {
        let data = try KurzJSON.encoder().encode(tokens)
        let update = SecItemUpdate(query(baseURL) as CFDictionary, [kSecValueData as String: data] as CFDictionary)
        if update == errSecSuccess { return }
        guard update == errSecItemNotFound else { throw fail(update) }
        var add = query(baseURL)
        add[kSecValueData as String] = data
        add[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlock
        let status = SecItemAdd(add as CFDictionary, nil)
        guard status == errSecSuccess else { throw fail(status) }
    }

    public func clear(for baseURL: String) throws {
        let status = SecItemDelete(query(baseURL) as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else { throw fail(status) }
    }
}

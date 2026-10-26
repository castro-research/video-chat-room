import Foundation

/// REST do manager (experimentos/README.md). Erros viram KurzError com mensagem em português.
struct Manager {
    let base: String

    init(_ base: String) { self.base = base.trimmingCharacters(in: .whitespaces).trimmingSuffix("/") }

    func healthy() async -> Bool { (try? await call("GET", "/healthz", timeout: 3)) != nil }
    func rooms() async throws -> [Room] { try decode(RoomListResponse.self, await call("GET", "/v1/rooms")).rooms }
    func room(_ id: String) async throws -> Room { try decode(Room.self, await call("GET", "/v1/rooms/\(enc(id))")) }
    func create(_ name: String) async throws -> Room { try decode(Room.self, await call("POST", "/v1/rooms", ["name": name])) }
    func join(_ id: String, userId: String, name: String) async throws -> VoiceSession {
        try decode(VoiceSession.self, await call("POST", "/v1/rooms/\(enc(id))/join", ["userId": userId, "name": name]))
    }

    private func enc(_ s: String) -> String {
        s.trimmingCharacters(in: .whitespaces).addingPercentEncoding(withAllowedCharacters: .urlPathAllowed.subtracting(CharacterSet(charactersIn: "/"))) ?? s
    }

    private func decode<T: Decodable>(_ type: T.Type, _ data: Data) throws -> T {
        do { return try JSONDecoder().decode(type, from: data) } catch { throw KurzError("Resposta inesperada do servidor.") }
    }

    private func call(_ method: String, _ path: String, _ body: [String: String]? = nil, timeout: TimeInterval = 5) async throws -> Data {
        guard let url = URL(string: base + path), url.scheme != nil else { throw KurzError("URL do manager inválida: \(base)") }
        var req = URLRequest(url: url, timeoutInterval: timeout)
        req.httpMethod = method
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        if let body { req.httpBody = try JSONSerialization.data(withJSONObject: body) }
        let data: Data, res: URLResponse
        do { (data, res) = try await URLSession.shared.data(for: req) } catch { throw KurzError("Manager indisponível em \(base).") }
        let status = (res as? HTTPURLResponse)?.statusCode ?? 0
        if (200...299).contains(status) { return data }
        let err = try? JSONDecoder().decode(ApiError.self, from: data)
        if err?.code == "not_found" { throw KurzError("Sala não encontrada.") }
        let text = err?.message ?? String(decoding: data.prefix(120), as: UTF8.self)
        throw KurzError("O manager recusou o pedido (\(status)): \(text)")
    }
}

extension String {
    func trimmingSuffix(_ s: String) -> String {
        var r = self
        while r.hasSuffix(s) { r.removeLast(s.count) }
        return r
    }
}

/// O que o utilizador escreveu → URL base do servidor, ou nil se não for um endereço (ver core/Servers.kt).
/// Sem esquema assume https, menos para localhost e IPs.
func normalizeServer(_ input: String) -> String? {
    var s = input.trimmingCharacters(in: .whitespacesAndNewlines).trimmingSuffix("/")
    if s.isEmpty || s.contains(where: { $0.isWhitespace }) { return nil }
    if !s.contains("://") {
        let host = String(s.split(separator: "/", maxSplits: 1, omittingEmptySubsequences: false)[0].split(separator: ":", maxSplits: 1, omittingEmptySubsequences: false)[0])
        let local = host == "localhost" || (!host.isEmpty && host.allSatisfy { $0.isNumber || $0 == "." })
        s = (local ? "http://" : "https://") + s
    }
    guard let r = s.range(of: "://") else { return nil }
    let scheme = s[..<r.lowerBound].lowercased()
    let rest = String(s[r.upperBound...])
    let authority = String(rest.split(separator: "/", maxSplits: 1, omittingEmptySubsequences: false)[0])
    let parts = authority.split(separator: ":", maxSplits: 1, omittingEmptySubsequences: false)
    let host = String(parts[0])
    let port = parts.count > 1 ? String(parts[1]) : nil
    if scheme != "http" && scheme != "https" { return nil }
    if host.isEmpty || authority.contains("@") || authority.contains("[") { return nil }
    if let port, port.isEmpty || !port.allSatisfy({ $0.isNumber }) { return nil }
    return "\(scheme)://\(rest)"
}

func serverLabel(_ url: String) -> String { url.range(of: "://").map { String(url[$0.upperBound...]) } ?? url }

import Foundation

/// Camada fina sobre URLSession: monta a URL, envia, mapeia erros. Não conhece autenticação.
struct HTTPClient: Sendable {
    let baseURL: String
    let session: URLSession
    var timeout: TimeInterval = 20

    init(baseURL: String, session: URLSession) {
        self.baseURL = baseURL.trimmingCharacters(in: .whitespacesAndNewlines).trimmingSuffix("/")
        self.session = session
    }

    func url(_ path: String, query: [URLQueryItem] = []) throws -> URL {
        guard var c = URLComponents(string: baseURL + path), c.scheme != nil else { throw KurzError("URL do servidor inválida: \(baseURL)", code: "invalid_url") }
        if !query.isEmpty { c.queryItems = query }
        guard let u = c.url else { throw KurzError("URL do servidor inválida: \(baseURL)", code: "invalid_url") }
        return u
    }

    @discardableResult
    func send(_ method: String, _ path: String, query: [URLQueryItem] = [], body: Data? = nil, bearer: String? = nil) async throws -> Data {
        var req = URLRequest(url: try url(path, query: query), timeoutInterval: timeout)
        req.httpMethod = method
        req.setValue("application/json", forHTTPHeaderField: "Accept")
        if let body { req.httpBody = body; req.setValue("application/json", forHTTPHeaderField: "Content-Type") }
        if let bearer { req.setValue("Bearer \(bearer)", forHTTPHeaderField: "Authorization") }
        let data: Data, res: URLResponse
        do { (data, res) = try await session.data(for: req) }
        catch is CancellationError { throw CancellationError() }
        catch let e as URLError where e.code == .cancelled { throw CancellationError() }
        catch { throw KurzError.network(baseURL) }
        guard let http = res as? HTTPURLResponse else { throw KurzError.network(baseURL) }
        if (200...299).contains(http.statusCode) { return data }
        let retry = http.value(forHTTPHeaderField: "Retry-After").flatMap { TimeInterval($0.trimmingCharacters(in: .whitespaces)) }
        throw KurzError.api(status: http.statusCode, error: try? JSONDecoder().decode(ApiError.self, from: data), retryAfter: retry)
    }

    /// Segmento de caminho: escapa tudo, inclusive `/`, `@` fica (o contrato usa `@me`).
    static func seg(_ s: String) -> String {
        var allowed = CharacterSet.urlPathAllowed
        allowed.remove(charactersIn: "/?#")
        return s.addingPercentEncoding(withAllowedCharacters: allowed) ?? s
    }
}

extension String {
    func trimmingSuffix(_ s: String) -> String {
        var r = self
        while r.hasSuffix(s) { r.removeLast(s.count) }
        return r
    }
}

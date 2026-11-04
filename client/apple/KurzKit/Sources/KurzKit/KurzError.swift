import Foundation

/// Erro do cliente ou do servidor, com a mensagem em português que a UI mostra.
/// `code` é o `ApiError.code` do servidor (conjunto aberto) ou um código local: `network`, `decoding`, `invalid_url`, `unauthenticated`.
public struct KurzError: LocalizedError, Equatable, Sendable {
    public let code: String
    /// Status HTTP; 0 quando o erro não veio de uma resposta.
    public let status: Int
    public let message: String
    /// Mensagem original do servidor (não é para comparar nem para a UI).
    public let serverMessage: String?
    /// `Retry-After` do 429, em segundos.
    public let retryAfter: TimeInterval?

    public init(_ message: String, code: String = "client", status: Int = 0, serverMessage: String? = nil, retryAfter: TimeInterval? = nil) {
        self.message = message; self.code = code; self.status = status
        self.serverMessage = serverMessage; self.retryAfter = retryAfter
    }

    public var errorDescription: String? { message }

    public var isRateLimited: Bool { code == "rate_limited" }
    public var isUnauthenticated: Bool { code == "unauthenticated" || code == "token_expired" || (status == 401) }
    public var isNetwork: Bool { code == "network" }

    static func unauthenticated() -> KurzError {
        KurzError("Sessão encerrada. Entra novamente.", code: "unauthenticated", status: 401)
    }
    static func network(_ base: String) -> KurzError {
        let host = URL(string: base)?.host.map { h in URL(string: base)?.port.map { "\(h):\($0)" } ?? h } ?? base
        return KurzError("Servidor indisponível em \(host).", code: "network")
    }
    static let decoding = KurzError("Resposta inesperada do servidor.", code: "decoding")

    /// Traduz a resposta de erro do servidor.
    static func api(status: Int, error: ApiError?, retryAfter: TimeInterval?) -> KurzError {
        let code = error?.code ?? "http_\(status)"
        let text: String
        switch code {
        case "unauthorized": text = "Sessão inválida. Entra novamente."
        case "token_expired": text = "Sessão expirada. Entra novamente."
        case "forbidden": text = "Não tens permissão para fazer isto."
        case "missing_permission": text = "Falta-te permissão para esta ação."
        case "hierarchy": text = "O teu cargo não é superior ao do alvo."
        case "not_found": text = "Não encontrado."
        case "validation": text = error.map { "Dados inválidos: \($0.message)" } ?? "Dados inválidos."
        case "username_taken": text = "Esse nome de utilizador já está em uso."
        case "registration_closed": text = "O registo está fechado neste servidor."
        case "invalid_invite": text = "Convite inválido."
        case "invite_expired": text = "Este convite expirou."
        case "banned": text = "Foste banido desta comunidade."
        case "owner_cannot_leave": text = "O dono não pode sair da comunidade. Transfere a propriedade primeiro."
        case "not_supported": text = "Este servidor não suporta esta funcionalidade."
        case "rate_limited":
            if let r = retryAfter { text = "Demasiados pedidos. Tenta novamente em \(Int(r.rounded(.up))) s." }
            else { text = "Demasiados pedidos. Tenta novamente daqui a pouco." }
        default:
            text = status >= 500 ? "Erro no servidor (\(status))." : "O servidor recusou o pedido (\(status))."
        }
        return KurzError(text, code: code, status: status, serverMessage: error?.message, retryAfter: retryAfter)
    }
}

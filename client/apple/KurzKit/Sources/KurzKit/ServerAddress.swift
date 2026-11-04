import Foundation

/// O que o utilizador escreveu → URL base do servidor, ou nil se não for um endereço (ver core/Servers.kt).
/// Sem esquema assume https, menos para localhost e IPs.
public func normalizeServer(_ input: String) -> String? {
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

/// O endereço sem o esquema, para mostrar.
public func serverLabel(_ url: String) -> String { url.range(of: "://").map { String(url[$0.upperBound...]) } ?? url }

/// Matiz (0..<360) estável a partir de um texto (o `hashValue` do Swift muda entre arranques). Para avatares sem `hue` do servidor.
public func stableHue(_ s: String) -> Double {
    Double(s.unicodeScalars.reduce(0) { ($0 &* 31 &+ Int($1.value)) & 0x7fffffff } % 360)
}

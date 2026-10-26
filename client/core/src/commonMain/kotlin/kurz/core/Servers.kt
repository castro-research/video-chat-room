package kurz.core

/**
 * O que o utilizador escreveu → URL base do servidor, ou null se não for um endereço.
 * Sem esquema assume https, menos para localhost e IPs (http: quase nunca têm certificado).
 * ponytail: sem IPv6 literal nem utilizador@ no endereço.
 */
fun normalizeServer(input: String): String? {
    var s = input.trim().trimEnd('/')
    if (s.isEmpty() || s.any { it.isWhitespace() }) return null
    if ("://" !in s) {
        val host = s.substringBefore('/').substringBefore(':')
        val local = host == "localhost" || (host.isNotEmpty() && host.all { it.isDigit() || it == '.' })
        s = (if (local) "http://" else "https://") + s
    }
    val scheme = s.substringBefore("://").lowercase()
    val rest = s.substringAfter("://")
    val authority = rest.substringBefore('/')
    val host = authority.substringBefore(':')
    val port = authority.substringAfter(':', "")
    if (scheme != "http" && scheme != "https") return null
    if (host.isEmpty() || '@' in authority || '[' in authority) return null
    if (':' in authority && (port.isEmpty() || !port.all { it.isDigit() })) return null
    return "$scheme://$rest"
}

/** Como o servidor se mostra: o endereço sem o esquema. */
fun serverLabel(url: String) = url.substringAfter("://")

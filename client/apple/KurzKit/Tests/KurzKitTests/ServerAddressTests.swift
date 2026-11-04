import Testing
@testable import KurzKit

@Suite("Endereço do servidor")
struct ServerAddressTests {
    @Test func normaliza() {
        #expect(normalizeServer("localhost:8080") == "http://localhost:8080")
        #expect(normalizeServer("192.168.1.5:8080/") == "http://192.168.1.5:8080")
        #expect(normalizeServer("kurz.exemplo.pt") == "https://kurz.exemplo.pt")
        #expect(normalizeServer("  http://x.pt/// ") == "http://x.pt")
        #expect(normalizeServer("http://localhost:8080") == "http://localhost:8080")
    }

    @Test func recusa() {
        for bad in ["", "   ", "a b", "ftp://x.pt", "https://", "http://u@x.pt", "x.pt:abc", "http://[::1]:80"] {
            #expect(normalizeServer(bad) == nil, "\(bad)")
        }
    }

    @Test func etiquetaEMatiz() {
        #expect(serverLabel("http://localhost:8080") == "localhost:8080")
        #expect(serverLabel("sem-esquema") == "sem-esquema")
        #expect(stableHue("abc") == stableHue("abc") && (0..<360).contains(stableHue("http://localhost:8080")))
    }
}

import Testing
@testable import KurzKit

/// Espera uma condição (polling curto) ou falha o teste.
func eventually(timeout: TimeInterval = 5, _ message: Comment = "condição", _ cond: @Sendable () async -> Bool, sourceLocation: SourceLocation = #_sourceLocation) async {
    let end = Date().addingTimeInterval(timeout)
    while Date() < end { if await cond() { return }; try? await Task.sleep(nanoseconds: 10_000_000) }
    Issue.record("Tempo esgotado esperando: \(message)", sourceLocation: sourceLocation)
}


import Foundation

/// Backoff exponencial com jitter: `min(cap, base * 2^attempt)` escalado por `[0.5, 1]`, para não reconectar tudo ao mesmo tempo
/// nem em 0 s.
public struct Backoff: Sendable, Equatable {
    public var base: TimeInterval
    public var cap: TimeInterval
    public init(base: TimeInterval = 1, cap: TimeInterval = 30) { self.base = base; self.cap = cap }

    /// `jitter` em [0, 1]; por omissão sorteado.
    public func delay(attempt: Int, jitter: Double = Double.random(in: 0...1)) -> TimeInterval {
        let exp = min(cap, base * pow(2, Double(max(0, min(attempt, 30)))))
        return exp * (0.5 + 0.5 * min(1, max(0, jitter)))
    }
}

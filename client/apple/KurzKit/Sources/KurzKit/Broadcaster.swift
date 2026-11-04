import Foundation

/// Distribui valores a vários `AsyncStream`s. Quem assina depois não recebe o passado, exceto o último valor se pedir (`replayLatest`).
final class Broadcaster<Element: Sendable>: @unchecked Sendable {
    private let lock = NSLock()
    private var continuations: [UUID: AsyncStream<Element>.Continuation] = [:]
    private var latest: Element?
    private var finished = false

    init(initial: Element? = nil) { latest = initial }

    var current: Element? { lock.lock(); defer { lock.unlock() }; return latest }

    func stream(replayLatest: Bool = false) -> AsyncStream<Element> {
        AsyncStream(bufferingPolicy: .unbounded) { cont in
            let id = UUID()
            lock.lock()
            if finished { lock.unlock(); cont.finish(); return }
            continuations[id] = cont
            let replay = replayLatest ? latest : nil
            lock.unlock()
            if let replay { cont.yield(replay) }
            cont.onTermination = { [weak self] _ in
                guard let self else { return }
                self.lock.lock(); self.continuations[id] = nil; self.lock.unlock()
            }
        }
    }

    func yield(_ value: Element) {
        lock.lock()
        latest = value
        let all = Array(continuations.values)
        lock.unlock()
        for c in all { c.yield(value) }
    }

    func finish() {
        lock.lock()
        finished = true
        let all = Array(continuations.values)
        continuations.removeAll()
        lock.unlock()
        for c in all { c.finish() }
    }
}

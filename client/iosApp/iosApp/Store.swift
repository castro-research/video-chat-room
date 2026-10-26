import Combine
import KurzShared

/// Ponte entre o KurzController (Kotlin) e o SwiftUI: [s] é o último instantâneo do estado.
final class Store: ObservableObject {
    let kurz = KurzController()
    @Published var s: KState?
    private var token: StateSubscription?

    init() {
        token = kurz.observe { [weak self] state in self?.s = state }
    }

    deinit { token?.cancel(); kurz.release() }
}

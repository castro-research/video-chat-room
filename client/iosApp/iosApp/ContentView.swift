import SwiftUI

struct ContentView: View {
    @StateObject private var store = Store()

    var body: some View {
        Group {
            if let s = store.s {
                ZStack {
                    RoomsView(s: s)
                    if s.roomVisible {
                        VoiceRoomView(s: s).transition(.move(edge: .trailing))
                    }
                }
                .animation(.easeOut(duration: 0.2), value: s.roomVisible)
            } else {
                P.sidebar.ignoresSafeArea()
            }
        }
        .environmentObject(store)
    }
}

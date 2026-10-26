import SwiftUI

@main
struct KurzApp: App {
    @StateObject private var app = AppModel()

    var body: some Scene {
        WindowGroup {
            RootView().environmentObject(app).frame(idealWidth: 1100, idealHeight: 720)
        }
    }
}

// swift-tools-version:5.9
import PackageDescription

let package = Package(
    name: "KurzKit",
    platforms: [.macOS(.v13), .iOS(.v16)],
    products: [.library(name: "KurzKit", targets: ["KurzKit"])],
    targets: [
        .target(name: "KurzKit"),
        .testTarget(name: "KurzKitTests", dependencies: ["KurzKit"]),
    ],
    swiftLanguageVersions: [.v5]
)

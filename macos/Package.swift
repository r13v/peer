// swift-tools-version: 6.2
import PackageDescription

let package = Package(
    name: "Peer",
    platforms: [.macOS("26.0")],
    dependencies: [
        .package(url: "https://github.com/gonzalezreal/textual", exact: "0.5.0")
    ],
    targets: [
        .executableTarget(name: "Peer", dependencies: [.product(name: "Textual", package: "textual")], path: "Sources/Peer"),
        .testTarget(name: "PeerTests", dependencies: ["Peer"], path: "Tests/PeerTests")
    ]
)

// swift-tools-version: 5.10
import PackageDescription

let package = Package(
    name: "AgentRunnerKit",
    platforms: [
        .macOS(.v14),
    ],
    products: [
        .library(name: "AgentRunnerKit", targets: ["AgentRunnerKit"]),
    ],
    targets: [
        .target(
            name: "AgentRunnerKit",
            dependencies: []
        ),
    ]
)


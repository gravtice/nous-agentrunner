// swift-tools-version: 5.10
import PackageDescription

let package = Package(
    name: "NousAgentRunnerKit",
    platforms: [
        .macOS(.v14),
    ],
    products: [
        .library(name: "NousAgentRunnerKit", targets: ["NousAgentRunnerKit"]),
    ],
    targets: [
        .target(
            name: "NousAgentRunnerKit",
            dependencies: []
        ),
    ]
)


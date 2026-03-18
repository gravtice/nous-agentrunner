// swift-tools-version: 5.10
import PackageDescription

let package = Package(
    name: "AgentRunnerDemo",
    platforms: [
        .macOS(.v14),
    ],
    dependencies: [
        .package(path: "../../../sdk/swift/AgentRunnerKit"),
    ],
    targets: [
        .executableTarget(
            name: "AgentRunnerDemo",
            dependencies: [
                .product(name: "AgentRunnerKit", package: "AgentRunnerKit"),
            ]
        ),
    ]
)

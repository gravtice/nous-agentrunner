// swift-tools-version: 5.10
import PackageDescription

let package = Package(
    name: "NousAgentRunnerDemo",
    platforms: [
        .macOS(.v14),
    ],
    dependencies: [
        .package(path: "../../../sdk/swift/NousAgentRunnerKit"),
    ],
    targets: [
        .executableTarget(
            name: "NousAgentRunnerDemo",
            dependencies: [
                .product(name: "NousAgentRunnerKit", package: "NousAgentRunnerKit"),
            ]
        ),
    ]
)

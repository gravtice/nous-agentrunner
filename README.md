# Nous Agent Runner

Nous Agent Runner 是一个面向 macOS（Apple Silicon）的 **Agent 运行与隔离**运行时：在 Host(macOS) 上通过 Apple Virtualization Framework（AVF）启动 Linux VM，在 VM 内用容器运行不同的 Agent Service，并对本机 AI App 暴露统一的控制面/数据面接口。

- 数据面协议命名：ASP（Agent Service Protocol）
- 设计与实现计划：`docs/v0.1.0/IMPLEMENTATION_PLAN.md`
- 构建与打包：`docs/v0.1.0/BUILDING.md`

代码结构（v1）：

- `cmd/nous-agent-runnerd`：Host daemon（Control Plane + ASP 网关；子进程集成 Lima）
- `cmd/nous-guest-runnerd`：Guest daemon（容器/镜像管理 + 内部 WS 代理）
- `services/claude-agent-service`：首发 Agent Service（Python Claude Agent SDK）
- `sdk/swift/NousAgentRunnerKit`：Swift SDK（发现 runtime、封装 HTTP/WS）
- `demo/macos/NousAgentRunnerDemo`：最小 SwiftUI Demo（验证集成）

# 构建与打包（v0.1.0 / MVP）

目标：在 macOS 14+（Apple Silicon）上构建并打包 Nous Agent Runner（Host+Guest）以及首发 `claude-agent-service`。

## 1. 先决条件

- macOS 14+ / Apple Silicon
- Xcode（用于 Demo UI App）
- Go（用于 `nous-agent-runnerd` / `nous-guest-runnerd` / `limactl`）
- Docker（用于构建 `claude-agent-service` 镜像；或在 VM 内用 `nerdctl` 构建）
- 本仓库包含 submodules：首次 clone 后执行 `git submodule update --init --recursive`

## 2. 构建二进制

执行：`scripts/macos/build_binaries.sh`

产物输出到 `dist/`：

- `dist/nous-agent-runnerd`（darwin/arm64）
- `dist/nous-guest-runnerd`（linux/arm64，供 VM 内 systemd 启动）
- `dist/limactl`（darwin/arm64，作为内部 VM 后端）
- `dist/lima-guestagent.Linux-aarch64`（linux/arm64，供 Lima hostagent 使用）
- `dist/lima-templates/`（Lima templates，供 `template:*` 解析）

## 3. 构建 claude-agent-service 镜像

在仓库根目录执行：

`docker build -f services/claude-agent-service/Dockerfile -t local/claude-agent-service:0.1.0 .`

说明：

- 该镜像依赖 `references/claude-agent-sdk-python`（已 vendored）。
- 运行时仍需要容器内具备 Claude Code CLI 及其凭据/环境配置（由集成方处理）。

## 4. Demo App（UI）

当前 Demo 以 SwiftPM + SwiftUI 形式提供：`demo/macos/NousAgentRunnerDemo/`。

建议用 Xcode 打开并运行；打包成 `.app` 后再生成 DMG。

## 5. DMG 打包

推荐使用一键脚本（会自动构建 runtime 二进制、把它们注入到 App 的 `Contents/Resources/`，再输出 DMG）：

`scripts/macos/package_dmg.sh <app_path>`

其中 `<app_path>` 支持：

- 已构建的 `.app` bundle 路径
- 包含且仅包含一个 `.app` 的目录
- SwiftPM package 目录（包含 `Package.swift`；脚本会生成最小 `.app` wrapper）

产物输出到：`dist/<AppName>.dmg`

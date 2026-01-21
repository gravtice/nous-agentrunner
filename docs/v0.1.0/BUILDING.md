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

`scripts/macos/make_dmg.sh` 需要一个已构建好的 `.app`（例如 `dist/NousAgentRunnerDemo.app`）。

将以下文件放入 App bundle 的 `Contents/Resources/`（建议）：

- `nous-agent-runnerd`
- `limactl`
- `nous-guest-runnerd`
- `NousAgentRunnerConfig.json`（包含 `instance_id`，App 与 runnerd 需一致）

然后由 App 启动时以子进程方式启动 `nous-agent-runnerd`。

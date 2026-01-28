# 构建与打包（v0.1.0 / MVP）

目标：在 macOS 14+（Apple Silicon）上构建并打包 Nous Agent Runner（Host+Guest）以及首发 `claude-agent-service`。

## 1. 先决条件

- macOS 14+ / Apple Silicon
- Xcode（用于 Demo UI App）
- Go（用于 `nous-agent-runnerd` / `nous-guest-runnerd` / `limactl`）
- Docker（用于构建 `claude-agent-service` 镜像；或在 VM 内用 `nerdctl` 构建）
- 本仓库包含 submodules：首次 clone 后执行 `git submodule update --init --recursive`

版本：

- `VERSION` 是打包/离线资产的单一事实来源（`NOUS_VERSION` / `NOUS_VM_VERSION`）。
- Lima 通过 submodule `references/lima` 固定到 tag `v2.0.3`。

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

`docker build -f services/claude-agent-service/Dockerfile -t docker.io/gravtice/nous-claude-agent-service:$(awk -F= '$1=="NOUS_VERSION"{print $2; exit}' VERSION) .`

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

## 6. （可选）离线资产包：避免首次启动下载

默认策略是 **首次启动按 Lima 模板在线下载 VM 镜像与 containerd/nerdctl 依赖**（体积更小、也更容易跟上上游安全更新）。

如果你需要在弱网/企业网络环境中更稳定（或用于离线演示），可以在打包前预下载并随 DMG 一起分发（可选）：

1) 下载离线资产到 `dist/offline-assets/`：

`scripts/macos/fetch_offline_assets.sh`

2) 打包 DMG（`package_dmg.sh` 会自动探测并注入离线资产）：

`scripts/macos/package_dmg.sh <app_path>`

Runner 行为：

- 若 App bundle 内存在 `nous-offline-assets/manifest.json`，Runner 会把资源复制到
  `~/Library/Caches/NousAgentRunner/<instance_id>/SharedTmp/OfflineAssets/`，并在生成 Lima YAML 时优先使用本地路径，从而避免首次启动下载。
- 若 manifest 内包含 `images[]`，Runner 会在 `Create Service` 时优先把对应 tar 包导入到 VM 的 containerd（避免从 registry 拉取镜像）。

注意：

- 这会显著增大 DMG 体积。
- 一旦把 OS 镜像/依赖随产品再分发，可能触发上游开源许可证的再分发义务（Third‑Party Notices / Source offer 等）。具体合规策略请走你们的法务/合规流程确认。

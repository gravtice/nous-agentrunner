# NousAgentRunnerDemo

最小 SwiftUI Demo（用于验证 Nous Agent Runner 的集成方式）。

在 macOS 14+ 上：

1. 在 Demo App 的 bundle 中放置：
   - （可选）`NousAgentRunnerConfig.json`（覆盖 `instance_id`；缺省会从 App 的 Bundle ID 自动派生）
   - `nous-agent-runnerd` / `limactl` / `nous-guest-runnerd`（按你的打包方式放到 Resources）
2. 用 Xcode 打开并运行（需要 `.app` bundle，`instance_id` 会从 Bundle ID 自动派生）。

说明：本 Demo 只覆盖最小闭环（发现 runtime、创建 service、WS 对话），UI/错误处理保持极简。

提示：如果你用 `./scripts/macos/package_dmg.sh` 打包 Demo App，脚本默认 ad-hoc codesign，
macOS 的“文件与文件夹”权限可能会在每次替换安装后重新弹窗。要让授权在更新后继续生效，
请用同一个 bundle id + 稳定签名证书（设置 `NOUS_CODESIGN_IDENTITY`）。

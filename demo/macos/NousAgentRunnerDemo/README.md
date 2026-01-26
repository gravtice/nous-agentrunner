# NousAgentRunnerDemo

最小 SwiftUI Demo（用于验证 Nous Agent Runner 的集成方式）。

在 macOS 14+ 上：

1. 在 Demo App 的 bundle 中放置：
   - （可选）`NousAgentRunnerConfig.json`（覆盖 `instance_id`；缺省会从 App 的 Bundle ID 自动派生）
   - `nous-agent-runnerd` / `limactl` / `nous-guest-runnerd`（按你的打包方式放到 Resources）
2. 用 Xcode 打开并运行（需要 `.app` bundle，`instance_id` 会从 Bundle ID 自动派生）。

说明：本 Demo 只覆盖最小闭环（发现 runtime、创建 service、WS 对话），UI/错误处理保持极简。

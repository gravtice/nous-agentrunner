# AgentRunnerDemo

最小 SwiftUI Demo（用于验证 Agent Runner 的集成方式）。

在 macOS 14+ 上：

1. 在 Demo App 的 bundle 中放置：
   - （可选）`AgentRunnerConfig.json`（覆盖 `instance_id`；缺省会从 App 的 Bundle ID 自动派生）
   - `agent-runnerd` / `limactl` / `guest-runnerd`（按你的打包方式放到 Resources）
2. 用 Xcode 打开并运行（需要 `.app` bundle，`instance_id` 会从 Bundle ID 自动派生）。

说明：本 Demo 只覆盖最小闭环（发现 Runner Context、创建 service、WS 对话），UI/错误处理保持极简。

提示：如果你用 `./scripts/macos/package_dmg.sh` 打包 Demo App，脚本默认 ad-hoc codesign，
macOS 的“文件与文件夹”权限可能会在每次替换安装后重新弹窗。要让授权在更新后继续生效，
请用同一个 bundle id + 稳定签名证书（设置 `AGENT_RUNNER_CODESIGN_IDENTITY`）。

## E2E UI 自动化测试（真实大模型）

推荐方案：`XCTest/XCUITest`（macOS UI Testing）。

```bash
./scripts/macos/demo_xcuitest.sh
```

说明：

- UI 测试会启动 Demo App，自动点击 UI：创建 service → 发送一条消息 → 断言出现 `usage:`（表示真实模型调用已发生）。
- 会调用真实模型，可能产生费用；首次启动 VM / 拉镜像可能需要几分钟。
- 脚本会在运行测试前尽量把输入法切到 `ABC`（ASCII），避免中文输入法影响 `typeText` 行为。
- 首次跑 UI 测试如果卡在 `Timed out while enabling automation mode`，需要到 `系统设置 → 隐私与安全性 → 辅助功能(Accessibility)` 里允许 Xcode/测试 Runner 的自动化控制权限，然后重试。
- 依赖（模型账号/Key 等）需要你已在 Demo 的 Settings / Claude settings 中配置好（脚本不会注入密钥）。

# Agent Runner TypeScript SDK 设计与实现计划（v0.2.4）

## 0. 文档目的

在现有 Swift SDK（`sdk/swift/AgentRunnerKit`）基础上，制定一个 **KISS、可维护、与 ASMP/ASP 协议对齐** 的 TypeScript SDK 开发方案，用于 Node/Electron 集成 Agent Runner。

本计划关注：

- SDK 的对外 API 形态与目录结构
- Runner Context 发现（零参数风格）与鉴权接入
- ASMP（HTTP）与 ASP（WebSocket）最小可用封装
- 测试与交付节奏（阶段化）

## 1. 目标与非目标

### 1.1 目标（必须实现）

1. **与 Swift SDK 对齐的能力覆盖**：至少覆盖 Swift SDK 已实现的接口集合（System/Shares/Images/Services/Tunnels/Skills + Chat WS URL/连接）。
2. **零参数风格 Runner Context 发现**：默认不依赖 CLI 参数；从 `runtime.json`/`.env.*`/`token` 文件发现端口与鉴权信息，优先级与现有约定一致：`.env.local > .env.production > .env.development > .env.test`。
3. **Node 18+ 可用**：使用 Node 内置 `fetch`（或可注入 fetch），不引入臃肿依赖。
4. **错误可诊断**：HTTP 非 200 返回应包含 status 与响应体文本（便于定位）。
5. **安全默认保守**：对 `instance_id`、`skill_name` 等路径相关输入做字符集校验，避免 path traversal。

### 1.2 非目标（明确不做 / 延后）

- 不做浏览器 SDK（浏览器无法设置 `Authorization` header，且无法读取本机 token 文件）。
- 不做协议/类型自动生成（OpenAPI / codegen）。先保证正确性与可维护性。
- 不做“自动下载/安装 runner”能力（SDK 只负责连接与调用）。
- 不做过度抽象（Transport 插件体系、Rx/Observable、复杂中间件等）。

## 2. 范围与版本对齐

- 产品版本：`AGENT_RUNNER_VERSION=0.2.4`（该文档对应版本）。
- 协议版本：以 Runner `GET /v1/system/status` 返回为准（ASMP/ASP v0.2.0 + ASMP v0.3.0 skills 能力等）。
- 兼容性策略：
  - SDK 默认 **忽略响应中的未知字段**（前向兼容）。
  - 对能力差异以 `system.status.capabilities.*` 做特性探测（例如 `capabilities.skills_install`）。

## 3. SDK 形态（对外 API）

### 3.1 包与目录结构（建议）

新增目录：

```
sdk/typescript/agent-runner-sdk/
├── package.json
├── tsconfig.json
├── src/
│   ├── context.ts          # runner context discovery
│   ├── client.ts           # ASMP client
│   ├── daemon.ts           # (可选) runnerd 管理
│   ├── ws.ts               # ASP websocket helper
│   ├── errors.ts
│   └── index.ts
└── test/                   # node:test / 最小 mock server
```

发布包名：`agent-runner-sdk`。

### 3.2 核心类型与类（建议与 Swift 命名对齐）

**AgentRunnerContext**

- 字段：`baseURL: URL`, `token: string`, `instanceId: string`
- 静态方法：
  - `discover(): Promise<AgentRunnerContext>`（默认按 App Bundle ID 派生 instance id；与 Swift/runnerd 一致）
  - `discover({ instanceId }: { instanceId: string }): Promise<...>`（可选逃生口；一般不建议上层乱用）
  - `deriveInstanceIdFromBundleId(bundleId: string): string`（与 Swift 一致：sha256 + 前 12 位 hex）

**AgentRunnerClient**

- `constructor(runnerContext: AgentRunnerContext, opts?: { fetch?: typeof fetch })`
- 统一内部：`requestJSON(method, path, body, timeoutMs)`
- 方法集合（按 Swift SDK 覆盖）：
  - System：
    - `getSystemStatus()`
    - `diagnoseGuestToHostTunnel()`
    - `getSystemPaths()`
    - `restartVM()`
  - Shares：
    - `listShares()`
    - `addShare(hostPath: string)`
  - Skills（ASMP v0.3.0）：
    - `listSkills()`
    - `discoverSkills(source: string, opts?: { ref?: string; subpath?: string })`
    - `installSkills(source: string, opts?: { ref?: string; subpath?: string; skills?: string[]; replace?: boolean })`
    - `deleteSkill(name: string)`
  - Images：
    - `pullImage(ref: string)`
  - Services：
    - `createService(req: CreateServiceRequest)`（通用）
    - `createClaudeService(...)`（可选便捷封装：默认 resources 与 Swift 保持一致）
    - `listServices()`
    - `getService(serviceId: string)`
    - `getBuiltinTools(serviceType: string)`
    - `deleteService(serviceId: string)`
    - `stopService(serviceId: string)`
    - `startService(serviceId: string)`
    - `resumeService(serviceId: string)`
  - Tunnels：
    - `createTunnel(hostPort: number, guestPort?: number)`
    - `listTunnels()`
    - `getTunnelByHostPort(hostPort: number)`
    - `deleteTunnel(tunnelId: string)`
    - `deleteTunnelByHostPort(hostPort: number)`
  - ASP（WebSocket）：
    - `openChatWebSocket(serviceId: string)`（返回 ws 连接；见 5.2）

**AgentRunnerDaemon（可选，后置阶段）**

- 目标：在 Electron/Node 应用中按需拉起 `agent-runnerd`，并等待其就绪。
- `ensureRunning()` 逻辑与 Swift 一致：先 discover + status 探测，失败后 spawn runnerd 并轮询。

### 3.3 类型策略（KISS）

- 第一阶段：响应体类型统一为 `Record<string, unknown>`（或 `unknown`），避免“类型看起来很美但不准”的维护成本。
- 第二阶段（可选）：为常用对象补充最小接口（例如 `Service`/`Tunnel`/`SystemStatus`），并允许额外字段：`type X = { ... } & Record<string, unknown>`。
- 不引入 `zod/io-ts` 之类的运行时校验库。

## 4. Runner Context 发现（Runtime/Context Discovery）

### 4.0 `instance_id` 发现（与 Swift/runnerd 对齐）

零参数默认值必须稳定且与 runnerd 一致，避免同机多 App 冲突。规则（按优先级）：

1. 若能在可执行文件附近找到 `AgentRunnerConfig.json` 且包含合法 `instance_id`：使用该值。
2. 否则（macOS）：尝试从 App 的 `Info.plist` 读取 `CFBundleIdentifier`，并按 `sha256(bundleId)` 取前 12 位 hex 派生 `instance_id`。
3. 兜底：`"default"`（例如非 `.app` 运行环境或无法读取 bundle id）。

### 4.1 Config 路径

与现有实现一致：

- `~/.agentrunner/<instance_id>/`
  - `runtime.json`：优先读取 `listen_addr/listen_port`
  - `token`：Bearer token（0600）
  - `.env.*`：作为端口兜底（按优先级）

### 4.2 端口发现优先级（必须一致）

1. `runtime.json.listen_port`（若存在且合法）
2. 依次读取 `.env.local/.env.production/.env.development/.env.test` 的 `AGENT_RUNNER_PORT`

### 4.3 输入校验

- `instance_id`：仅允许 `[A-Za-z0-9._-]`，且不能为空。
- `skill_name`：同上（与 Swift SDK `isSafeSkillDirName` 规则一致）。

## 5. 传输层（HTTP / WebSocket）

### 5.1 HTTP（ASMP）

- 默认基于 `fetch`，必须允许注入（便于测试/特殊环境）。
- 超时用 `AbortController`（或 `AbortSignal.timeout`），并将超时映射为 `timeout` 类型错误。
- 非 200：抛出 `http` 错误，包含 `status` + 响应体文本（不要吞）。

### 5.2 WebSocket（ASP）

- Node 环境建议使用 `ws`（支持在握手阶段设置 `Authorization` header）。
- 连接 URL：`ws://<listen_addr>:<listen_port>/v1/services/{service_id}/chat`
- 鉴权：`Authorization: Bearer <token>`
- 第一阶段 SDK 只负责“建立连接 + 发送/接收 JSON 文本帧”；不强行封装成复杂的事件流框架。

补充说明：这里的“直接依赖 `ws`”指 SDK 在 `dependencies` 里声明并内部 `import ws` 来创建连接；使用方无需额外注入 WebSocket 实现。对比方案是把 WebSocket 构造器/工厂函数作为参数注入（减少依赖，但集成更麻烦）。

## 6. 测试策略（必须可重复）

### 6.1 单元测试

- `parseEnv()`：引号/空行/# 注释处理与 Swift 行为一致。
- runner context discovery：
  - runtime.json 优先级
  - `.env.*` 优先级
  - token 缺失报错
- safe name 校验：覆盖边界字符与空字符串。
- URL 生成：HTTP baseURL → WS URL 的转换正确。

### 6.2 轻量集成测试（推荐）

用 Node `http`/`ws` 起一个最小 mock server：

- 验证 `Authorization` header 注入
- 验证非 200 错误体透传
- 验证 `POST` JSON body 编码

不做真实 VM/容器依赖的端到端测试（那是 Runner 本体的职责）。

## 7. 交付节奏（阶段化）

> 原则：每个阶段都可独立验收；每个阶段都能产出“可用的增量”，不要搞大爆炸。

### Stage 1：基础 SDK（ASMP + runner context discovery）

- 交付：
  - `AgentRunnerContext.discover()`
  - `AgentRunnerClient` 覆盖除 WS 之外的 Swift SDK 方法
  - 最小单元测试与 mock HTTP 测试
- 验收：
  - 能在本机连上 `agent-runnerd` 并成功 `GET /v1/system/status`
  - 端口发现优先级正确
  - 错误信息可读（status + body）

### Stage 2：ASP WebSocket（最小封装）

- 交付：
  - `openChatWebSocket(serviceId)`
  - 最小 mock WS 测试（握手 header + 收发 JSON）
- 验收：
  - 能与真实 service 完成一次 `input` → `response.delta` → `done` 的闭环（手工验证即可）

### Stage 3：Daemon 管理（可选）

- 交付：
  - `AgentRunnerDaemon.ensureRunning()`：支持传入 runnerd 可执行文件路径
  - 等待就绪超时与错误可诊断
- 验收：
  - runner 未启动时 SDK 可拉起并连接成功
  - 不产生 stdout/stderr pipe 堵塞（默认 `stdio: ignore`，日志看 runner 自己的 `runnerd.log`）

### Stage 4：文档与发布

- 交付：
  - README 增加 Node/Electron 使用示例
  - npm 发布流程（版本对齐 `AGENT_RUNNER_VERSION`）
- 验收：
  - `npm pack` 产物可用，`dist/` 内容正确

## 8. 未决问题（需要确认）

已确认决策：

- npm 包名：`agent-runner-sdk`
- WebSocket：SDK **直接依赖** `ws`
- `instance_id`：默认按 `CFBundleIdentifier` 派生（与 Swift/runnerd 一致）

仍需确认：

1. npm 版本策略：
   - 是否与 Runner 版本严格同步（0.2.4 → 0.2.4），还是允许 SDK 独立语义版本？

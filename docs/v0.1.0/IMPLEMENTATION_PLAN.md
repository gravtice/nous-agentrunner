# Nous Agent Runner v1（MVP）设计与实现计划

## 0. 文档目的

本文档定义 Nous Agent Runner v1（MVP）的目标、边界、架构、接口协议与实现分解，作为后续实现与验收的唯一参考。

## 1. 背景与目标

### 1.1 背景

目标是在 macOS 端提供一个可被 AI App 集成的 Agent Runner：能够**隔离运行 Agent**（不是 LLM），对外提供统一接口，便于不同 Agent 框架（先 Claude Agent SDK，后续 OpenAI Agent SDK 等）以容器形式部署运行。

### 1.2 v1（MVP）目标

- 平台：**macOS 14+、Apple Silicon**。
- 隔离：**共享 Linux VM**（AVF），VM 内用 Container 跑 Agent Service。
- 接口：
  - **控制面（ASMP）**：管理 VM/镜像/Service（HTTP，localhost）。
  - **数据面（ASP）**：与 Agent Service 进行**流式对话**（WebSocket，localhost），支持**输入多模态**（v1 输出先文本，协议留扩展位）。
- 目录共享：
  - VM 启动时一次性共享**白名单目录**到 Guest。
  - 白名单目录对所有 Service **默认只读可见**（无需显式声明）。
  - Service 创建时可声明特定子路径为 **可写（rw）**，其余保持只读（ro）。
  - 路径在容器内必须与 App 路径一致（例如 `/Users/...` 在容器内同路径可读写）。
- 镜像：
  - 支持从**单一固定官方 registry** 拉取镜像。
  - 支持导入/使用本地自定义镜像（基于官方镜像的约束 v1 先靠文档约定，后续再做强校验）。
  - 支持将运行中的 Service 容器**固化为本地镜像**（filesystem commit，不包含挂载目录数据）。
- 分发：
  - Agent Runner 作为可嵌入 runtime，被第三方 AI App 一起打包成 DMG。
  - 本仓库提供一个带简单 UI 的 Demo AI App（用于演示与集成验证）。

### 1.3 v1 非目标（明确不做）

- 不支持 Intel。
- 不支持远程访问（仅 `127.0.0.1`）。
- 不做 per-service 的“只读可见范围收敛”（白名单目录全量可见）。
- 不对外暴露 tool call/中间推理事件（Agent Service 内部完成对 LLM/工具对接）。
- 不支持把非白名单路径“搬运”进共享目录（path 不在白名单直接报错）。
- 不支持多 registry（商业版能力预留但 v1 不做）。
- 不做 output 多模态（协议保留字段，v1 只输出 text）。

## 2. 术语

- Host：macOS 侧。
- Guest：Linux VM 内。
- Agent Service：完整 Agent 运行服务（容器化），对外提供对话接口；内部自行对接 LLM Service、工具、检索等。
- Data Plane：对话/任务数据流接口（流式）。
- ASMP（Agent Service Management Protocol）：对外控制面协议（v1：HTTP+JSON），用于管理 VM/容器/镜像/Service。
- ASP（Agent Service Protocol）：对外数据面协议（v1 使用 WebSocket），用于 App 与某个 Agent Service 的会话式流式交互。
- Share（白名单目录）：Host 目录共享到 Guest/Container，用于 Agent 读取参考文件或输出产物（仅在声明 rw 时可写）。

## 3. 设计原则（v1 强约束）

1. **本机优先**：只监听 `127.0.0.1`，不对外网开放端口。
2. **路径一致**：App 传入的 `path` 在容器内必须可直接使用（同路径）。
3. **默认只读**：白名单目录对容器默认 ro；写权限必须显式声明且可审计。
4. **最小协议**：对外只暴露“Agent 视角”的对话接口；不暴露 tool call 等内部事件。
5. **KISS**：v1 先做共享 VM、固定官方 registry、最小可用的镜像/Service 管理能力。
6. **零参数风格（集成友好）**：不依赖 CLI 参数覆盖配置；配置来自文件（含 `.env.*` 优先级）。

## 4. 总体架构

```
┌──────────────────────────── Host (macOS) ─────────────────────────────┐
│  Demo AI App / Third-party AI App                                     │
│     └── NousAgentRunnerKit (Swift) / TS SDK                            │
│             │  (localhost + token)                                     │
│             ▼                                                          │
│       nous-agent-runnerd                                               │
│       - ASMP HTTP (127.0.0.1)                                          │
│       - ASP WS (127.0.0.1)                                             │
│       - Shared VM Manager (Lima + AVF)                                 │
│       - Share whitelist manager                                        │
│       - Image/service state & auth (files; Keychain 预留)              │
│             │  (SSH port-forward)                                      │
│             ▼                                                          │
│  ┌──────────────────────── Linux VM (Guest) ───────────────────────┐  │
│  │  nous-guest-runnerd                                              │  │
│  │  - containerd/nerdctl (or ctr)                                   │  │
│  │  - pull/import/commit images                                     │  │
│  │  - start/stop containers, mounts, cgroups                        │  │
│  │  - logs/health/status                                            │  │
│  │        │                                                         │  │
│  │        ▼                                                         │  │
│  │   Agent Service Containers                                       │  │
│  │   - claude-agent-service                                         │  │
│  │   - openai-agent-service (future)                                │  │
│  │   - custom-agent-service (future)                                │  │
│  └──────────────────────────────────────────────────────────────────┘  │
└────────────────────────────────────────────────────────────────────────┘
```

关键点：

- v1 采用 **Lima 子进程 + SSH port-forward** 连接 Guest 内 `nous-guest-runnerd`（控制与数据转发都复用该通道）；vsock 可作为后续优化方向。
- Guest 出网通过 VM NAT 网络；容器出网由 Guest 网络栈提供。
- 目录共享使用 AVF VirtioFS，在 VM 启动时一次性配置所有 Share。

## 5. 目录共享与权限模型

### 5.1 Share 白名单

Share 由两部分组成：

- `host_path`：Host 绝对路径（必须是目录）。
- `share_id`：稳定 ID（用于配置与审计；可由 canonical path hash 生成）。

约束：

- v1 仅支持 VM 启动时一次性配置 Share；变更 Share 列表需要重启 VM。
- Share 白名单对所有 Service **默认 ro 可见**（通过容器层 ro bind mount 实现）。
- VM/Guest 层对 Share 建议以 **rw** 方式挂载，以支持 `rw_mounts` 按需放开写权限；默认只读由容器层强制。

### 5.2 路径一致性（同路径挂载）

为满足“容器内可直接使用 App 路径”，v1 采取 **同路径挂载**策略：

- Guest 内 mountpoint = `host_path`（同绝对路径）。
- Container 内 bind mount destination = `host_path`（同绝对路径）。

示例：

- Host：`/Users/alice/Work`（Share）
- Guest：mount 到 `/Users/alice/Work`
- Container：bind mount 到 `/Users/alice/Work`（默认 ro）

### 5.3 写权限（rw 覆盖挂载）

Service 创建时可声明 `rw_mounts[]`（目录列表）：

- 每一项必须是某个 Share 的子路径（canonical 后做前缀匹配）。
- 运行时对该子路径增加更具体的 bind mount，覆盖 ro 为 rw。

其余所有 Share 路径保持 ro。

### 5.4 默认临时目录（必须落盘路径输入）

由于 v1 要求大文件必须通过 `path` 输入，且 `path` 必须在白名单目录内，Runner 需要提供一个默认临时目录（加入 Share 白名单），供 SDK/应用落盘；如需让某个 service 写入，该目录（或其子路径）应加入该 service 的 `rw_mounts`：

- 默认临时目录（默认加入 Share 白名单）：
  - `~/Library/Caches/NousAgentRunner/<instance_id>/SharedTmp`

SDK 提供获取该路径的 API（或 ASMP 提供 `GET /v1/system/paths`）。

### 5.5 Path 校验（防止 symlink 逃逸）

所有通过 `path` 或 `rw_mounts` 进入系统的路径必须：

1. 计算 canonical path（解析 `..`、解析 symlink）。
2. canonical path 必须严格落在某个 Share 的 canonical `host_path` 前缀之下。
3. 若不满足，直接拒绝请求并返回 `PATH_NOT_ALLOWED`。

## 6. 网络与访问边界

- 对外监听：仅 `127.0.0.1`。
- Guest/容器可出网（用于访问 LLM API、外部工具服务等）。
- Agent **不需要也不允许**在宿主机执行命令；只允许在容器内执行命令或通过网络访问外部工具。

## 7. 进程与组件职责

### 7.1 `nous-agent-runnerd`（Host daemon）

职责：

- 管理共享 VM 生命周期（启动/停止/健康检查）。
  - v1 通过 `limactl` 子进程实现（Lima 作为内部 VM 后端）。
- 管理 Share 白名单与默认临时目录创建。
- 管理镜像与 Service 的 ASMP API（转发到 Guest）。
- 作为 ASP（数据面）网关：
  - 对外提供 ASP 接口（v1 暂定 WebSocket）。
  - 对内通过 SSH port-forward 与 Guest 交互，并把流式响应转发给客户端。
- 鉴权：
  - 本机 token（Bearer），v1 实现落盘到 `~/Library/Application Support/NousAgentRunner/<instance_id>/token`（Keychain 作为后续增强）。
  - 仅本机调用，不做复杂身份绑定。
- 状态持久化（不依赖命令行参数）：
  - `~/Library/Application Support/NousAgentRunner/<instance_id>/...`

### 7.2 `nous-guest-runnerd`（Guest daemon）

职责：

- 管理容器生命周期：create/start/stop/delete。
- 管理镜像：pull/import/list/commit（snapshot）。
- 管理资源限制：cgroups（cpu/mem/pids）。
- 管理服务健康、日志抓取。

实现建议（v1）：

- 采用 `containerd` + `nerdctl`（避免引入 Docker 全家桶）。

### 7.3 Agent Service 容器（运行规范）

每个 Agent Service 容器必须实现一个最小服务接口，供 runner 代理：

- `GET /health`：返回 200 表示就绪。
- 对话接口（ASP，v1 内部/外部保持一致便于代理）：`WS /v1/chat`

容器需要能处理两类输入来源：

- `bytes`（base64，<= 8MB）
- `path`（容器内同路径，例如 `/Users/...`）

## 8. 配置与状态文件

### 8.1 Instance 隔离（避免不同 App 互相踩）

为了让第三方 AI App 可将 Runner 打包到 DMG 并与其它 App 共存，Runner 使用 `instance_id` 区分不同集成方实例：

- `instance_id` 来源优先级（零参数）：
  1) 若存在：集成方在 app bundle 资源中提供 `NousAgentRunnerConfig.json`，Runner 启动时读取。
  2) 若缺失且 Runner 位于 macOS `.app` bundle 内：从 `CFBundleIdentifier` 派生稳定 `instance_id`：`sha256(lowercase(bundle_id))` 的前 12 位 hex。
  3) 仍缺省：默认 `instance_id = "default"`（仅用于开发；不建议生产使用）。

### 8.2 Runner Home 目录

- Application Support（状态/配置）：
  - `~/Library/Application Support/NousAgentRunner/<instance_id>/`
- Caches（临时文件/落盘大文件）：
  - `~/Library/Caches/NousAgentRunner/<instance_id>/`

### 8.3 配置文件优先级（零参数）

Runner 读取配置时遵循优先级：

`.env.local > .env.production > .env.development > .env.test`

文件位于：

`~/Library/Application Support/NousAgentRunner/<instance_id>/.env.*`

v1 关键配置项（示例）：

- `NOUS_AGENT_RUNNER_PORT`：对外监听端口（未配置则首次运行自动分配并写入 `.env.local`）。
- `NOUS_AGENT_RUNNER_REGISTRY_BASE`：官方 registry base（固定编译默认值，可通过文件覆盖）。
- `NOUS_AGENT_RUNNER_VM_MEMORY_MB`：VM 内存（默认例如 4096）。
- `NOUS_AGENT_RUNNER_VM_CPU_CORES`：VM CPU（默认例如 4）。

### 8.4 状态文件（建议）

- `shares.json`：Share 白名单。
- `runtime.json`：当前运行时信息（端口、pid、vm 状态摘要）。
- `services.json`：已创建 service 的元数据（用于崩溃恢复与清理）。
- `logs/`：daemon 日志与每 service 的聚合日志索引。

## 9. 对外接口（ASMP）

约定：

- 协议：ASMP（v1：HTTP+JSON）
- Base URL：`http://127.0.0.1:<port>`
- Header：`Authorization: Bearer <token>`
- 所有响应 `Content-Type: application/json`

错误返回统一格式：

```json
{
  "error": {
    "code": "PATH_NOT_ALLOWED",
    "message": "path is not under any shared directory",
    "details": {}
  }
}
```

### 9.1 系统状态

`GET /v1/system/status`

返回示例：

```json
{
  "version": "0.1.0",
  "vm": {"state": "running"},
  "services_running": 2
}
```

`GET /v1/system/paths`

返回示例：

```json
{
  "default_temp_dir": "/Users/alice/Library/Caches/NousAgentRunner/demo/SharedTmp"
}
```

### 9.2 Shares（白名单目录）

`GET /v1/shares`

`POST /v1/shares`

请求：

```json
{"host_path":"/Users/alice/Work"}
```

`DELETE /v1/shares/{share_id}`

说明：

- Share 变更后，`vm_restart_required=true`（需要重启 VM 才生效）。
- Share 仅定义“可见目录集合”；具体哪些子路径可写由创建 service 时的 `rw_mounts` 决定。

### 9.3 Images（镜像管理）

`POST /v1/images/pull`

请求：

```json
{"ref":"docker.io/gravtice/nous-claude-agent-service:0.1.0"}
```

约束：

- `ref` 会先做 canonicalize（例如 `gravtice/...` 视为 `docker.io/gravtice/...`），再校验其必须以 `NOUS_AGENT_RUNNER_REGISTRY_BASE` 为前缀，否则拒绝（`REGISTRY_NOT_ALLOWED`）。

`POST /v1/images/import`

- 用于导入本地 OCI image（tar/目录），返回本地 tag。

`GET /v1/images`

### 9.4 Services（创建/销毁/查询）

`POST /v1/services`

请求示例：

```json
{
  "type": "claude",
  "image_ref": "docker.io/gravtice/nous-claude-agent-service:0.1.0",
  "resources": {"cpu_cores": 2, "memory_mb": 1024, "pids": 256},
  "rw_mounts": ["/Users/alice/Work/project/output"],
  "service_config": {
    "system_prompt": "You are a helpful agent.",
    "mcp_servers": "/Users/alice/Work/mcp-config.json"
  }
}
```

返回示例：

```json
{
  "service_id": "svc_01H...",
  "state": "running",
  "asp_url": "ws://127.0.0.1:8347/v1/services/svc_01H.../chat"
}
```

`GET /v1/services`

`GET /v1/services/{service_id}`

`DELETE /v1/services/{service_id}`

### 9.5 Service 固化（snapshot）

`POST /v1/services/{service_id}/snapshot`

请求：

```json
{"new_tag":"local/claude-agent-service:dev-20260121"}
```

语义：

- 等价于对容器 rootfs 做 commit/snapshot，生成一个**本地镜像**。
- **不包含任何挂载目录的数据**（挂载目录是 Host 数据，不应被打包进镜像）。

## 10. 对外接口（ASP：v1 使用 WebSocket）

路径：

`WS /v1/services/{service_id}/chat`

### 10.0 传输选型（WebSocket vs Streamable HTTP）

v1 选择 WebSocket 的原因：

- **会话语义简单**：一条连接 = 一个 session（直接对齐 Claude Agent SDK 的 `session_id` 复用）。
- **双向控制直观**：`cancel` 是同一连接内的消息，不需要额外 HTTP endpoint。
- **实现更省事**：Host 侧做 WS 代理，Guest/容器侧也实现同一套 WS 接口即可。

Streamable HTTP（SSE/NDJSON/chunked response）也可行，但会引入“跨请求 session 保存/取消接口/长连接订阅”等额外机制；v1 先不引入。

约定：

- 一条 WS 连接 = 一个会话（不复用 conversation）。
- v1 输出只包含文本流，但消息结构统一为 `contents[]`，为未来输出多模态预留。
- 连接内支持多轮：客户端可在同一连接内多次发送 `input`；服务端按顺序逐次响应（v1 不支持同一连接内并发多个 in-flight 请求）。
- 会话建立：WS 连接建立后，服务端立即发送 `session.started`（用于日志/排障；同时用于对接 Claude Agent SDK 的 `session_id`）。

服务端在连接建立后发送：

```json
{"type":"session.started","session_id":"sess_01H...","service_id":"svc_01H..."}
```

### 10.1 输入消息（Client → Runner）

```json
{
  "type": "input",
  "contents": [
    {"kind":"text","text":"请阅读这个 PDF 并总结要点"},
    {"kind":"file","source":{"type":"path","path":"/Users/alice/Work/spec.pdf","mime":"application/pdf"}}
  ]
}
```

`bytes` 输入（<= 8MB，base64）：

```json
{
  "type": "input",
  "contents": [
    {"kind":"image","source":{"type":"bytes","encoding":"base64","mime":"image/png","data":"..."}}
  ]
}
```

约束：

- `source.type="path"` 时必须在 Share 白名单内，否则返回 `PATH_NOT_ALLOWED` 并关闭会话。
- `source.type="bytes"` 时解码后大小必须 <= `MAX_INLINE_BYTES=8MB`，否则返回 `INLINE_BYTES_TOO_LARGE`。

取消：

```json
{"type":"cancel"}
```

### 10.2 输出消息（Runner → Client）

增量：

```json
{"type":"response.delta","text":"第一点：..."}
```

结束：

```json
{
  "type":"response.final",
  "contents":[{"kind":"text","text":"总结如下：..."}]
}
```

错误：

```json
{"type":"error","code":"SERVICE_CRASHED","message":"agent service exited unexpectedly"}
```

完成：

```json
{"type":"done"}
```

## 10.3 Claude Agent SDK（Python）适配说明（v1 首发实现）

v1 首个 Agent Service 以 `references/claude-agent-sdk-python` 为基础实现（容器内运行 Python + `claude-agent-sdk`，底层驱动 Claude Code CLI）。

### 10.3.1 为什么 ASP 仍然合理

Claude Agent SDK（Python）的交互模型本质是：

- 输入：`ClaudeSDKClient.query(prompt: str)`（以及少量 streaming-mode 控制）。
- 输出：按消息流返回 `AssistantMessage / ResultMessage`，可选 `StreamEvent`（需开启 `include_partial_messages`）。

因此 v1 的外部 ASP 仍可保持“Agent 视角”的统一接口：

- 通过 `input.contents[]` 表达用户输入（多模态由文件路径或 inline bytes 表达）。
- 在 Claude 容器内部把 `contents[]` 规整成一个 prompt 字符串（并在 prompt 中显式列出附件路径/元信息）。
- 输出仅对外发送文本增量与最终文本，不暴露 tool call / thinking 等内部事件。

### 10.3.2 会话（session）如何对接 Claude Agent SDK

Claude Agent SDK（Python）在 streaming mode 下会把每次 `query()` 发送为带 `session_id` 的消息（其 `ClaudeSDKClient.query(..., session_id="default")` 默认值为 `"default"`）。

为保证多轮对话的上下文连续性，Claude Agent Service v1 采取最简单且可靠的对接方式：

- **每条外部 WS 连接创建并持有一个 `ClaudeSDKClient` 实例**（整个连接生命周期内保持连接）。
- 服务端为该 WS 连接生成 `session_id` 并发送 `session.started`。
- 连接内每次收到 `input`，调用 `ClaudeSDKClient.query(prompt, session_id=<同一个 session_id>)`，从而把多轮输入绑定到同一个 Claude session。
- 连接关闭时 `disconnect()` 并清理资源。

这既满足“WS 连接=会话”的外部语义，也与 Claude SDK 的 session 模型直接对齐。

### 10.3.3 流式输出来源（必须开启 partial messages）

Claude Agent SDK 只有在 `ClaudeAgentOptions.include_partial_messages=true` 时才会产出 `StreamEvent`（Anthropic API 原始流事件），否则不会有 `StreamEvent`（见其 e2e 测试 `e2e-tests/test_include_partial_messages.py`）。

为满足“流式对话”的产品要求，Claude Agent Service v1 应默认开启：

- `include_partial_messages=true`

并将 `StreamEvent` 中的文本增量映射为对外：

- `response.delta`（仅转发 `content_block_delta` 中 `delta.type == "text_delta"` 的文本；忽略 `thinking_delta` 等）。

最终结果以 `response.final` 给出（可直接取最后的 `AssistantMessage` 文本块拼接或内部累计）。

### 10.3.4 多模态输入的落地方式（Claude Code 视角）

ClaudeSDKClient 的 `query()` 接收的仍是字符串 prompt。v1 为保持统一协议且不引入额外工具链，Claude Agent Service 采取“文件引用优先”的策略：

- `source.type="path"`：直接在 prompt 中列出该路径（路径在容器内同路径可用），并由 Agent 自行读取/处理。
- `source.type="bytes"`（base64，<= 8MB）：在容器内落盘到 session 临时目录（容器内即可，不要求与 Host 路径一致），再把该临时文件路径写入 prompt 作为附件引用。

说明：

- “路径一致”只对 `path` 输入强约束；`bytes` 输入会在容器侧落盘生成一个容器内路径，不增加 App 侧复杂度。
- 对于音频/视频/图片等是否能被 Agent 正确理解，属于 Agent Service 自身能力（可通过容器内工具链或 Agent 工具完成），Runner 不在 v1 承诺“原生理解”。

### 10.3.5 cancel 的映射

外部 `cancel` 映射到 Claude Agent SDK 的 `ClaudeSDKClient.interrupt()`。

约束：

- interrupt 要求服务端持续消费 message stream（SDK 文档已提示）。Agent Service 的 WS 实现必须在会话生命周期内持续读取 SDK 输出，才能保证 cancel 生效。

## 10.4 Claude Agent Service 的配置字段（service_config 建议）

Claude Agent Service 容器内部建议将 `service_config` 直接视为 Claude Agent SDK 的 `ClaudeAgentOptions`（同名字段透传），核心字段：

- `system_prompt`：system prompt
- `allowed_tools` / `disallowed_tools`：工具白/黑名单
- `permission_mode`：必须选择非交互模式（否则会卡死等待 TTY），建议默认 `bypassPermissions` 并配合 `allowed_tools` 收敛
- `cwd`：工作目录（建议默认指向默认临时目录或用户指定目录）
- `add_dirs`：额外允许访问的目录（建议自动包含所有 Share 白名单目录）
- `mcp_servers`：MCP Server 配置（用于在容器内提供工具/检索等能力）；建议支持两种形态  
  - inline map：`{"name": {"type":"stdio","command":"...","args":[...]}}`  
  - path：指向一个 JSON 配置文件路径（必须在 Share 白名单内且容器内同路径可读）
- `env`：注入给 Claude Code CLI 的环境变量（例如 API key；v1 先不定义 Runner 的密钥管理策略，由集成方提供）
- `include_partial_messages`：建议默认 true

Runner 对 `service_config` 不做强 schema 校验（v1），只做 JSON 透传；校验与默认值由具体 Agent Service 决定。

## 11. Host ↔ Guest 通信（v1 实现方式）

由于 v1 采用 **Lima 子进程方式**，Host 侧直接使用 vsock 的成本较高；因此 v1 实现选择：

- Host 通过 Lima 生成的 `ssh.config` 建立 **SSH port-forward** 到 Guest `127.0.0.1:<NOUS_GUEST_RUNNERD_PORT>`。
- Host ↔ Guest 之间以 **HTTP(JSON) + WebSocket** 通信（Guest `nous-guest-runnerd` 提供 `/internal/*` 接口）。

ASP 转发复用同一 SSH 通道：

- Host 负责对外 ASP（v1：WS），并将 WS 事件转发到 Guest。
- Guest 负责与容器 WS 通信并回传增量事件给 Host（Host 侧只做校验与转发）。

（内部协议属于实现细节，可在实现阶段落成单独文档/代码注释；对外接口稳定即可。）

## 12. 安全模型（v1）

边界：

- Host 进程（nous-agent-runnerd）可信，负责鉴权与路径校验。
- Guest VM 提供一层隔离（与 Host 内核隔离）。
- 容器再提供一层隔离（同一 VM 内的多服务隔离）。

控制措施：

 - 本机 token 鉴权（v1 实现落盘；Keychain 预留）。
- 仅监听 localhost。
- 严格 path 白名单与 symlink 逃逸防护。
- 默认 ro，共享目录写权限显式声明。
- 禁止在 Host 执行命令；Agent 只能在容器内执行。

## 13. 分发与集成（DMG）

### 13.1 运行时打包

第三方 AI App 需要随 DMG 一起打包：

- `nous-agent-runnerd`（Host daemon）
- VM 资源（kernel/initrd/disk 模板/guest runner 等）
-（可选）`NousAgentRunnerKit`（Swift SDK）

App 启动后由 SDK/集成代码负责：

1. 启动 `nous-agent-runnerd`（子进程或 launchd，v1 建议子进程方式）。
2. 读取 `runtime.json` 获取端口，或通过 SDK 自动发现。
3. 使用 token 调用 ASMP 创建 service，再用 ASP 走数据面（v1 暂定 WebSocket）。

### 13.2 代码签名与 Entitlements（macOS 14+）

使用 AVF 需要至少包含：

- `com.apple.security.virtualization`
- `com.apple.security.network.client`
- `com.apple.security.network.server`

（是否沙盒化取决于集成方产品策略；v1 不强制沙盒。）

## 14. 实现分解（建议里程碑）

### M0：工程骨架与可运行闭环

- 定义目录结构、构建系统、基础日志。
- `nous-agent-runnerd` 能启动并提供 `GET /v1/system/status`。

### M1：共享 VM（子进程集成 Lima）

- 在 `nous-agent-runnerd` 内以子进程方式调用 `limactl` 管理 VM 生命周期（start/stop/status）。
- 生成并落盘 lima YAML（`vmType: vz`、`mountType: virtiofs`、`mounts` 由 Share 白名单生成且同路径挂载）。
- VM 健康检查（确保 `nous-guest-runnerd` 已就绪）。

### M2：Guest 容器管理与最小 Service

- 通过 Lima `provision` 在 Guest 内安装/启动 `nous-guest-runnerd`（systemd service）。
- `nous-guest-runnerd` 集成 `containerd`，可启动一个最小“echo service”（用于端到端冒烟测试）。
- ASMP `POST /v1/services` / `DELETE /v1/services` 可用。

### M3：claude-agent-service（Python Claude Agent SDK）

- 实现 `claude-agent-service` 容器：对内提供与 ASP 兼容的流式对话接口，内部调用 `ClaudeSDKClient`。
- 对接 session（同一会话复用 `session_id`）、partial streaming（`include_partial_messages=true`）、`interrupt()` cancel。
- 支持 `service_config`（含 `mcp_servers`）透传并做最小必要的默认值处理。

### M4：ASP（数据面）网关

- Host 侧提供 ASP 入口（v1 暂定 WebSocket），转发到 Guest/容器并流式回传。
- 完整处理 `bytes`（base64 <= 8MB）与 `path`（白名单校验），以及 `cancel`。

### M5：镜像管理与 snapshot

- pull（官方 registry 前缀校验）
- import（本地 OCI）
- snapshot（commit 到本地 tag）

### M6：Demo AI App + SDK

- `NousAgentRunnerKit`：启动/发现 runner、封装 ASMP/ASP。
- Demo App：白名单配置、创建 service、聊天 UI、多模态输入落盘到默认临时目录。
- DMG 打包示例与集成文档。

## 15. 待定事项（后续再开 v2/v3）

- 输出多模态（audio/image/video/file）。
- 多 registry / 镜像签名与供应链校验。
- per-service 可见范围收敛（而不是全量白名单可见）。
- 多 VM（每 service 一 VM）高隔离模式。

## 16. v1 VM 后端：子进程集成 Lima

本仓库包含 `references/lima`。v1 **确定**以子进程方式集成 Lima，把它作为“共享 VM 启动 + Guest 引导 + VirtioFS 挂载”的内部实现细节。

### 16.1 值得直接借鉴的点（与 v1 需求强相关）

- AVF（`vmType: vz`）+ VirtioFS：Lima 的 VZ driver 展示了完整的 AVF 设备配置与 VirtioFS 分享目录的落地方式  
  - 参考：`references/lima/pkg/driver/vz/vm_darwin.go`（`attachFolderMounts` / `attachOtherDevices`）
- vsock：Lima 在 VZ driver 中默认挂载了 virtio socket，并提供 host 侧直接 `Connect(vsockPort)` 的实现方式  
  - 参考：`references/lima/pkg/driver/vz/vz_driver_darwin.go`（`GuestAgentConn`）
- 同路径挂载（满足我们的“App path = 容器 path”）：Lima 的 cloud-init 模板支持把每个 share 挂载到任意绝对路径（例如 `/Users/...`）  
  - 参考：`references/lima/pkg/cidata/cidata.TEMPLATE.d/user-data`（`mounts:`）
- 容器运行时（containerd/nerdctl）：Lima 的 cidata boot scripts 能在 Guest 内安装并启用 containerd/nerdctl  
  - 参考：`references/lima/pkg/cidata/cidata.TEMPLATE.d/boot/40-install-containerd.sh`
- Entitlements：AVF 所需的 entitlements 可参考 Lima 的配置  
  - 参考：`references/lima/vz.entitlements`

### 16.2 v1 子进程集成方案（确定）

原则：**不把 Lima 的 CLI/配置暴露给集成方**；集成方只面向 `nous-agent-runnerd` 的 ASMP / ASP。

落地方式：

1. `nous-agent-runnerd` 打包 `limactl`（放在 App bundle 内），运行时设置私有 `LIMA_HOME`：  
   `~/Library/Caches/NousAgentRunner/lima/`（避免 UNIX socket 路径过长；不同实例由 `nous-<instance_id>` 子目录隔离）。
2. Runner 生成并管理 lima instance（建议固定 name：`nous-<instance_id>`），自动生成 lima YAML：  
   - `vmType: vz`（Apple Virtualization）  
   - `mountType: virtiofs`  
   - `mounts`：由 Share 白名单生成，`location == mountPoint == host_path`（满足“同路径挂载”）  
   - `writable: true`（Guest 层允许写；默认只读由容器 bind mount 强制）
3. 在 Guest 内安装/启动 `nous-guest-runnerd`（systemd），并作为 Host ↔ Guest 的稳定控制入口（通过 SSH port-forward 访问）。
4. 对外 ASMP / ASP 始终由 `nous-agent-runnerd` 提供；避免把运行时行为绑死在 `limactl shell` / `lima nerdctl` 这类“命令封装”上（调试可用，但不作为产品依赖）。

主要取舍：

- ✅ 优点：VM 创建、disk/cidata、VirtioFS、Guest 引导、containerd 安装这些“最费工的脏活”几乎现成；Lima 已被大量桌面容器产品验证。
- ⚠️ 代价：Lima 特性面较大（包含我们不需要的网络/端口转发能力）；若用子进程方式，需要严格隔离 `LIMA_HOME`，并把错误处理与升级策略想清楚。

## 17. 语言与技术栈选择（v1 建议）

### 17.1 Lima 使用的语言

Lima 的主体实现是 **Go**（`limactl`/drivers/cidata 等均为 Go）。

### 17.2 Nous Agent Runner（Host/Guest daemon）

v1 建议：

- `nous-agent-runnerd`：**Go**
  - 理由：与 Lima 同语言，复用成本最低；Go 对 daemon（HTTP/WS、并发、日志）非常合适；二进制分发与 DMG 签名也简单。
- `nous-guest-runnerd`：**Go**
  - 理由：Linux 下可静态编译，部署简单；适合做容器生命周期与资源限制的控制平面。

（如果未来决定完全不依赖 Lima，并且希望直接深度使用 AVF/XPC，也可以考虑 Host 侧改为 Swift；但 v1 没必要引入多语言构建复杂度。）

### 17.3 Claude Agent Service（首发实现）

v1 首发的 Claude Agent Service 建议使用 **Python 版 Claude Agent SDK**（`references/claude-agent-sdk-python`）：

- 理由：大量 Agent 的“工具代码”天然就是 Python（依赖生态也在 Python）；用 Python SDK 可直接复用依赖与开发方式。
- 若使用 TS 版 SDK：仍很可能需要额外引入 Python 运行时/依赖来跑工具，反而增加镜像复杂度与维护成本。

## 18. 参考 Goose 的复用点（可选）

本仓库不再 vendoring Goose（Block 开源的本地 Agent，Rust 实现；参考：https://github.com/block/goose ）。对 Nous Agent Runner 的直接复用价值有限（Goose 是“Agent 产品”，而我们是“Agent 运行时/隔离层”），但有两块非常值得参考：

### 18.1 ACP（Agent Client Protocol）的会话与流式模型

Goose 实现了 ACP agent server（JSON-RPC over stdio，基于 `sacp` crate），核心交互为：

- `initialize`：返回能力（是否支持 `loadSession`、输入模态等）
- `session/new`：创建 session（带 `cwd`、`mcpServers`）
- `session/load`：加载并回放历史（通过 notification 推送 session history）
- `session/prompt`：发送 prompt（prompt 是 `ContentBlock[]`，并通过 notification 流式推送增量）
- `cancel`：取消正在运行的 prompt

参考实现：

- 协议服务端：`https://github.com/block/goose/blob/51cc5db3879a816e3c3f09bfb5ee9b3f16194fbd/crates/goose-acp/src/server.rs`
- 简易客户端（很适合拿来理解协议行为）：`https://github.com/block/goose/blob/51cc5db3879a816e3c3f09bfb5ee9b3f16194fbd/test_acp_client.py`

与 Nous Agent Runner 的关系：

- 我们的 ASP（Data Plane：WS `input`/`response.delta`/`cancel`/`session.started`）在语义上可以视为“ACP 的一个子集/变体”。
- ACP 的 `session/load` + history replay 对我们后续做“断线重连/会话恢复（v2）”非常有参考价值。

注意：

- Goose ACP 在能力协商里明确 `audio=false`（且实现中忽略 audio block），因此 ACP 在 v1 形态下不能直接满足我们“输入音频/视频/文件”的目标；但其“能力协商 + session + streaming notifications”模式仍值得借鉴。

### 18.2 SSE 流式接口与 OpenAPI 工程化

Goose 的 `goose-server` 使用 axum 提供 HTTP API，并用 SSE（`text/event-stream`）推送流式事件，同时通过 `utoipa` 生成 OpenAPI schema 供 UI/SDK 使用。

参考：

- SSE 流式（reply）：`https://github.com/block/goose/blob/51cc5db3879a816e3c3f09bfb5ee9b3f16194fbd/crates/goose-server/src/routes/reply.rs`
- OpenAPI 生成入口：`https://github.com/block/goose/blob/51cc5db3879a816e3c3f09bfb5ee9b3f16194fbd/crates/goose-server/src/openapi.rs`、`https://github.com/block/goose/blob/51cc5db3879a816e3c3f09bfb5ee9b3f16194fbd/crates/goose-server/src/bin/generate_schema.rs`

与 Nous Agent Runner 的关系：

- 我们 v1 选择 WebSocket；但 Goose 的 SSE 事件建模（增量/结束/错误）与 OpenAPI 生成流程可作为工程实践参考（尤其是第三方集成/文档/SDK 生成）。

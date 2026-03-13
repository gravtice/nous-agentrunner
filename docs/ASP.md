# ASP（Agent Service Protocol）

ASP 是 Nous Agent Runner 的**数据面**协议：用于与某个 Agent Service 进行**流式会话**交互。

对外 WebSocket 入口同样由 `nous-agent-runnerd` 暴露。它是 Runner 的 localhost gateway/server 组件，不是独立产品名。

本文档以当前代码实现为准（`nous-agent-runnerd` 作为 WebSocket 网关，代理到 Guest/容器内的 `/v1/chat`）。

---

## 1. 基本约定

- URL：`ws://127.0.0.1:<port>/v1/services/{service_id}/chat`
- 鉴权：`Authorization: Bearer <token>`
- 编码：WebSocket 文本帧，内容为 JSON object
- 会话对象：
  - `service_id`：通过 ASMP 创建的 service（资源生命周期由 ASMP 管理，支持 stop/resume）
  - WS 连接：ASP 的传输通道；**同一 `service_id` 同时只允许一条 WS 连接**
  - `session_id`：对话会话（Agent session）标识；**每个 `service_id` 固定一个 `session_id`**，用于断线续聊与 resume
- 连接语义：
  - `session_id` 表示一个 Agent session（用于“继续对话”）
  - 当前实现：**一个 `service_id` 绑定一个 `session_id`**，WS 断线后重新连接会复用同一个 `session_id`
  - 当前实现：同一 `service_id` 同时只允许一条 WS 连接；并发连接会被拒绝（HTTP 409 `SERVICE_BUSY`）
  - 可在同一连接内多轮 `input`，但不支持并发多请求

连接建立后，Runner 会先发送 `session.started`：

```json
{"type":"session.started","session_id":"550e8400-e29b-41d4-a716-446655440000","service_id":"svc_xxxxxxxxxxxx"}
```

`session.started` 增量字段（v0.2.0+，可选）：

- `asp_version`：ASP 协议版本（例如 `"0.2.0"`）
- `capabilities`：网关语义能力（与 `GET /v1/system/status` 一致）
- `limits`：关键限制（例如 inline bytes 与 WS message 上限）

示例：

```json
{
  "type":"session.started",
  "service_id":"svc_...",
  "session_id":"550e8400-e29b-41d4-a716-446655440000",
  "asp_version":"0.2.0",
  "capabilities":{
    "single_ws_per_service":true,
    "error_fatal_field":true,
    "invalid_input_returns_done":true,
    "service_idle_timeout":true
  },
  "limits":{
    "max_inline_bytes":8388608,
    "max_ws_message_bytes":12000000
  }
}
```

---

## 2. Client → Runner 消息

### 2.1 `input`

结构：

```json
{
  "type": "input",
  "contents": [
    {"kind":"text","text":"你好"},
    {"kind":"file","source":{"type":"path","path":"/Users/alice/Work/spec.pdf","mime":"application/pdf"}}
  ]
}
```

`contents[].kind`（当前实现允许）：

- `"text"`：使用 `text`
- `"image" | "audio" | "video" | "file"`：必须提供 `source`

`source`：

- `{"type":"path","path":"/absolute/path","mime":"..."}`
  - `path` 必须是**绝对路径**、**真实存在**，且必须落在某个 Share 白名单目录内（含 canonicalize + 防 symlink 逃逸）
- `{"type":"bytes","encoding":"base64","data":"...","mime":"..."}`
  - `encoding` 必须为 `"base64"`（大小写不敏感）
  - base64 解码后字节数必须 `<= NOUS_AGENT_RUNNER_MAX_INLINE_BYTES`（默认 8MB）

注意：

- 使用 `source.type="path"` 时，路径在容器内也必须同路径可用（靠 Share 的“路径一致”挂载实现）。
- 若校验失败，Runner 会发送 `{"type":"error",...}`；对 `input` 还会补发 `{"type":"done"}`，连接保持可用（除非 `fatal=true`）。

### 2.2 `cancel`

取消当前请求：

```json
{"type":"cancel"}
```

### 2.3 `ask.answer`

回复 `agent.ask`（AskUserQuestion）：

```json
{"type":"ask.answer","ask_id":"ask_xxx","answers":{"<question>":"<answer>"}}
```

### 2.4 `permission_mode.set`

切换权限模式（例如切到 plan）：

```json
{"type":"permission_mode.set","mode":"plan"}
```

`mode`（当前实现允许）：

- `default`
- `acceptEdits`
- `plan`
- `bypassPermissions`

---

## 3. Runner → Client 消息

Runner 会原样转发底层 Agent Service 的事件（除去重复的 `session.started`）。

当前首发 `claude` service 会产生以下事件类型：

说明：

- 这些事件由 `claude-agent-service` 产出并透传；如果你使用的是较老的 service 镜像，可能只会看到 `response.delta` / `response.final` / `done`，不会有 `response.thinking.delta` / `tool.*` / `response.usage`。

### 3.1 `response.delta`

增量文本：

```json
{"type":"response.delta","text":"..."}
```

### 3.2 `response.thinking.delta`

增量思考文本（可选，取决于 service 配置/模型能力）：

```json
{"type":"response.thinking.delta","text":"..."}
```

开启方式（claude service）：

- 推荐：在 `service_config` 中设置 `max_thinking_tokens`（对应 Claude Agent SDK 的 `ClaudeAgentOptions.max_thinking_tokens`，最终会转成 `claude --max-thinking-tokens`）。

可选字段：

- `reset=true`：表示思考内容发生“重置/替换”，客户端应清空已累计的 thinking 再追加 `text`。

### 3.3 `tool.use`

工具调用（可选）：

```json
{"type":"tool.use","id":"toolu_xxx","name":"Bash","input":{"command":"ls -la"}}
```

可选字段：

- `input_json`：当 `input` 无法解析为 JSON object 时，service 可能以原始 JSON 字符串形式返回。

### 3.4 `tool.result`

工具执行结果（可选）：

```json
{"type":"tool.result","tool_use_id":"toolu_xxx","content":"...","is_error":false}
```

### 3.5 `agent.ask`

Agent 需要用户补充信息（AskUserQuestion）时推送：

```json
{"type":"agent.ask","ask_id":"ask_xxx","input":{"questions":[{"header":"...","question":"...","options":[{"label":"...","description":"..."}]}]}}
```

客户端收到后应展示问题并回发 `ask.answer`，随后 Agent 会继续执行并产出后续事件。

### 3.6 `response.final`

最终输出：

```json
{"type":"response.final","contents":[{"kind":"text","text":"..."}]}
```

### 3.7 `response.usage`

本轮 usage/cost（通常在 `response.final` 之后、`done` 之前到达）：

```json
{"type":"response.usage","usage":{"input_tokens":123,"output_tokens":456},"total_cost_usd":0.0012}
```

可选字段：`duration_ms`、`duration_api_ms`。

### 3.8 `error`

错误：

```json
{"type":"error","code":"BAD_REQUEST","message":"..."}
```

可选字段：

- `fatal=true`：表示 session 级错误；服务端会关闭 WS 连接（客户端无需等待 `done`）。

来源：

- Runner 侧输入校验可能返回：`PATH_NOT_ALLOWED`、`INLINE_BYTES_TOO_LARGE`、`BAD_REQUEST`
- Service 侧可能返回其它 `code`（例如 `BUSY/CLI_NOT_FOUND/SERVICE_ERROR/SERVICE_UNAVAILABLE` 等）

### 3.9 `done`

表示本次请求完成（无论成功或失败）：

```json
{"type":"done"}
```

### 3.10 `permission_mode.updated`

切换权限模式成功回执：

```json
{"type":"permission_mode.updated","mode":"plan"}
```

---

## 4. 推荐客户端状态机（避免踩坑）

- 连接建立后等待 `session.started`
- 每次发送 `input` 后：
  - 持续消费 `response.delta`（可边到边显示）
  - 可选：消费 `response.thinking.delta` / `tool.use` / `tool.result` / `response.usage`（用于 Debug/展示/统计）
  - 若收到 `agent.ask`：展示问题并发送 `ask.answer`，然后继续消费后续事件
  - 需要切换权限模式（例如 plan）时发送 `permission_mode.set`（建议在上一轮 `done` 之后）
  - 收到 `response.final` 后继续等待 `done`
  - 收到 `error`：
    - 若 `fatal=true`：连接将被关闭；可重连后继续（同一 `service_id` 会复用 `session_id`）
    - 否则：若该错误来自本轮 `input`，通常随后会有 `done`；若没有 `done`，表示该错误不属于一轮 `input`（例如非法消息/不合法 ask.answer 等）
- 同一连接内不要并发发送多条 `input`；若上一轮未结束再次发送，`claude` service 可能返回 `{"type":"error","code":"BUSY",...}`

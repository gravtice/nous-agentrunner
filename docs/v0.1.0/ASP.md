# ASP（Agent Service Protocol）v1

ASP 是 Nous Agent Runner 的**数据面**协议：用于与某个 Agent Service 进行**流式会话**交互。

本文档以当前代码实现为准（`nous-agent-runnerd` 作为 WebSocket 网关，代理到 Guest/容器内的 `/v1/chat`）。

---

## 1. 基本约定

- URL：`ws://127.0.0.1:<port>/v1/services/{service_id}/chat`
- 鉴权：`Authorization: Bearer <token>`
- 编码：WebSocket 文本帧，内容为 JSON object
- 连接语义：**一条 WS 连接 = 一个 session**（可在同一连接内多轮 `input`，但不支持并发多请求）

连接建立后，Runner 会先发送：

```json
{"type":"session.started","session_id":"sess_xxxxxxxxxxxx","service_id":"svc_xxxxxxxxxxxx"}
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
- 若校验失败，Runner 会发送 `{"type":"error",...}` 并关闭会话。

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

---

## 3. Runner → Client 消息

Runner 会原样转发底层 Agent Service 的事件（除去重复的 `session.started`）。

当前首发 `claude` service 会产生以下事件类型：

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

来源：

- Runner 侧输入校验可能返回：`PATH_NOT_ALLOWED`、`INLINE_BYTES_TOO_LARGE`、`BAD_REQUEST`
- Service 侧可能返回其它 `code`（例如 `BUSY/CLI_NOT_FOUND/SERVICE_ERROR/SERVICE_UNAVAILABLE` 等）

### 3.9 `done`

表示本次请求完成（无论成功或失败）：

```json
{"type":"done"}
```

---

## 4. 推荐客户端状态机（避免踩坑）

- 连接建立后等待 `session.started`
- 每次发送 `input` 后：
  - 持续消费 `response.delta`（可边到边显示）
  - 可选：消费 `response.thinking.delta` / `tool.use` / `tool.result` / `response.usage`（用于 Debug/展示/统计）
  - 若收到 `agent.ask`：展示问题并发送 `ask.answer`，然后继续消费后续事件
  - 收到 `response.final` 后继续等待 `done`
  - 收到 `error` 后也继续等待 `done`（`claude` service 会在错误后补发 `done`）
- 同一连接内不要并发发送多条 `input`；若上一轮未结束再次发送，`claude` service 可能返回 `{"type":"error","code":"BUSY",...}`

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

---

## 3. Runner → Client 消息

Runner 会原样转发底层 Agent Service 的事件（除去重复的 `session.started`）。

当前首发 `claude` service 会产生以下事件类型：

### 3.1 `response.delta`

增量文本：

```json
{"type":"response.delta","text":"..."}
```

### 3.2 `response.final`

最终输出：

```json
{"type":"response.final","contents":[{"kind":"text","text":"..."}]}
```

### 3.3 `error`

错误：

```json
{"type":"error","code":"BAD_REQUEST","message":"..."}
```

来源：

- Runner 侧输入校验可能返回：`PATH_NOT_ALLOWED`、`INLINE_BYTES_TOO_LARGE`、`BAD_REQUEST`
- Service 侧可能返回其它 `code`（例如 `BUSY/CLI_NOT_FOUND/SERVICE_ERROR/SERVICE_UNAVAILABLE` 等）

### 3.4 `done`

表示本次请求完成（无论成功或失败）：

```json
{"type":"done"}
```

---

## 4. 推荐客户端状态机（避免踩坑）

- 连接建立后等待 `session.started`
- 每次发送 `input` 后：
  - 持续消费 `response.delta`（可边到边显示）
  - 收到 `response.final` 后继续等待 `done`
  - 收到 `error` 后也继续等待 `done`（`claude` service 会在错误后补发 `done`）
- 同一连接内不要并发发送多条 `input`；若上一轮未结束再次发送，`claude` service 可能返回 `{"type":"error","code":"BUSY",...}`


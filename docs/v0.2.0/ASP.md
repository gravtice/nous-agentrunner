# ASP（Agent Service Protocol）v0.2.0

ASP 是 Nous Agent Runner 的**数据面**协议：用于与某个 Agent Service 进行**流式会话**交互。

本版本文档是 **v0.1.0 的增量说明**；完整消息结构与事件集合见 `docs/v0.1.0/ASP.md`。

---

## 1. 兼容性约定

- v0.2.0 不改变传输（仍为 WS 文本帧 + JSON object）。
- v0.2.0 只新增可选字段；老客户端忽略未知字段即可兼容。

---

## 2. `session.started` 增强：版本与能力协商

连接建立后，Runner 发送的 `session.started` 在 v0.2.0 增加：

- `asp_version`：ASP 协议版本
- `capabilities`：网关语义能力（与 `GET /v1/system/status` 一致）
- `limits`：关键限制（如 inline bytes 与 WS message 上限）

示例：

```json
{
  "type":"session.started",
  "service_id":"svc_...",
  "session_id":"sess_...",
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

## 3. `error.fatal`（可选字段）

v0.2.0 约定 `error` 事件可包含：

- `fatal=true`：表示 session 级错误；服务端将关闭 WS 连接（客户端无需等待 `done`）。

（错误码与 `done` 语义不变，详见 `docs/v0.1.0/ASP.md`。）


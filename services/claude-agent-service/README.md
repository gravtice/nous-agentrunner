# claude-agent-service

`claude-agent-service` 是 Nous Agent Runner v1 的首发 Agent Service：在容器内使用 **Python 版 Claude Agent SDK** 驱动 Claude Code，并对外暴露 **ASP(v1: WebSocket)** 接口。

接口：

- `GET /health`
- `WS /v1/chat`（连接建立后发送 `session.started`；支持 `?session_id=...` 续聊）
  - 事件：`response.delta` / `response.final` / `response.thinking.delta` / `tool.use` / `tool.result` / `agent.ask` / `response.usage` / `permission_mode.updated` / `error` / `done`

运行时约定（由 `nous-guest-runnerd` 注入）：

- `NOUS_RUNNER_SERVICE_PORT`：监听端口
- `NOUS_RUNNER_SERVICE_CONFIG_B64`：base64(JSON) 的 ClaudeAgentOptions
- `NOUS_RUNNER_SHARE_DIRS_B64`：base64(JSON array) 的共享白名单目录列表（用于默认填充 `add_dirs`）
- `NOUS_RUNNER_MAX_INLINE_BYTES`：inline bytes 上限（默认 8MB）
- `NOUS_FIRST_EVENT_TIMEOUT_SECONDS`：每次 `input` 后等待首个响应事件（`stream_event` / `assistant` / `result`）的超时（默认 20 秒），用于避免 CLI 卡死导致无输出/无错误

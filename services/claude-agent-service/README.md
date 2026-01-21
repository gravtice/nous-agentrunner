# claude-agent-service

`claude-agent-service` 是 Nous Agent Runner v1 的首发 Agent Service：在容器内使用 **Python 版 Claude Agent SDK** 驱动 Claude Code，并对外暴露 **ASP(v1: WebSocket)** 接口。

接口：

- `GET /health`
- `WS /v1/chat`（一条连接=一个 session）

运行时约定（由 `nous-guest-runnerd` 注入）：

- `NOUS_SERVICE_PORT`：监听端口
- `NOUS_SERVICE_CONFIG_B64`：base64(JSON) 的 ClaudeAgentOptions
- `NOUS_SHARE_DIRS_B64`：base64(JSON array) 的共享白名单目录列表（用于默认填充 `add_dirs`）
- `NOUS_MAX_INLINE_BYTES`：inline bytes 上限（默认 8MB）


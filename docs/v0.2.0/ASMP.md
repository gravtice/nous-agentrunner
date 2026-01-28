# ASMP（Agent Service Management Protocol）v0.2.0

ASMP 是 Nous Agent Runner 的**控制面**协议：用于管理 VM、共享目录（Share）、镜像（Image）、以及 Agent Service 生命周期。

本版本文档是 **v0.1.0 的增量说明**；完整 API 见 `docs/v0.1.0/ASMP.md`。

---

## 1. 兼容性约定

- v0.2.0 **不改 URL/鉴权/错误结构**（仍为 `/v1` + `Authorization: Bearer <token>`）。
- v0.2.0 只新增可选字段：
  - 老客户端应忽略未知字段；不需要升级即可继续工作。
  - 新客户端在缺少字段时按 v0.1.0 语义处理。

---

## 2. System：协议版本与能力发现

`GET /v1/system/status` 在 v0.2.0 增加：

- `protocols`：当前 Runner 对外提供的 ASMP/ASP 协议版本
- `capabilities`：网关语义与限制（便于上游避免“猜版本”）

示例（仅展示新增字段）：

```json
{
  "protocols": {"asmp":"0.2.0","asp":"0.2.0"},
  "capabilities": {
    "single_ws_per_service": true,
    "error_fatal_field": true,
    "invalid_input_returns_done": true,
    "service_idle_timeout": true,
    "max_inline_bytes": 8388608,
    "max_ws_message_bytes": 12000000
  }
}
```

---

## 3. Services：Idle Auto-Stop（可选）

### 3.1 创建 service 时设置 idle 超时

`POST /v1/services` 请求体新增可选字段：

- `idle_timeout_seconds`：`int`，默认 `0`（禁用）。

示例：

```json
{
  "type": "claude",
  "image_ref": "docker.io/gravtice/nous-claude-agent-service:0.2.0",
  "resources": {"cpu_cores": 2, "memory_mb": 1024, "pids": 256},
  "rw_mounts": ["/Users/alice/Work/project"],
  "env": {"ANTHROPIC_AUTH_TOKEN": "..."},
  "service_config": {"cwd": "/Users/alice/Work/project"},
  "idle_timeout_seconds": 600
}
```

### 3.2 Service 对象新增字段

`GET /v1/services` / `GET /v1/services/{service_id}` 返回的 `Service` 新增（可选）：

- `idle_timeout_seconds`
- `last_activity_at`：RFC3339 时间字符串
- `stop_reason`：`manual | idle_timeout | unknown`（仅在 `state=stopped` 时有意义）

示例：

```json
{
  "service_id":"svc_...",
  "session_id":"sess_...",
  "type":"claude",
  "image_ref":"...",
  "state":"stopped",
  "created_at":"2026-01-26T00:00:00Z",
  "idle_timeout_seconds":600,
  "last_activity_at":"2026-01-26T00:10:00Z",
  "stop_reason":"idle_timeout"
}
```

### 3.3 Idle 语义（KISS 版）

- 当某个 service 存在**活跃 ASP WS 连接**时，Runner 不会对其执行 idle stop。
- 断开 WS 连接后，Runner 更新 `last_activity_at`。
- 当满足：
  - service `state=running`
  - `idle_timeout_seconds>0`
  - 无活跃 WS 连接
  - `now - last_activity_at >= idle_timeout_seconds`
  时，Runner 会对该 service 执行 stop，并写入 `stop_reason="idle_timeout"`。

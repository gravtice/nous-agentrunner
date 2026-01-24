# ASMP（Agent Service Management Protocol）v1

ASMP 是 Nous Agent Runner 的**控制面**协议：用于管理 VM、共享目录（Share）、镜像（Image）、以及 Agent Service 生命周期。

本文档以当前代码实现为准（`nous-agent-runnerd`，API 前缀 `"/v1"`）。

---

## 1. 基本约定

- Base URL：`http://127.0.0.1:<port>`
- 鉴权：`Authorization: Bearer <token>`
- 编码：请求/响应均为 `application/json`
- 成功：HTTP 200
- 失败：HTTP 4xx/5xx，统一错误结构：

```json
{
  "error": {
    "code": "BAD_REQUEST",
    "message": "invalid json",
    "details": null
  }
}
```

---

## 2. 运行时发现（上游集成必读）

Runner 对外只监听本机环回地址，且不提供“命令行参数覆盖配置”。上游 App 需要通过**文件**发现端口与 token。

### 2.1 InstanceID（实例隔离）

Runner 支持 `instance_id` 隔离不同集成方的运行时数据目录。

- Runner 会尝试从可执行文件同目录或 `../Resources/` 读取 `NousAgentRunnerConfig.json`：

```json
{"instance_id":"default"}
```

- 缺省：`instance_id = "default"`。

### 2.2 端口与 token

以 macOS 为例（当前产品目标平台）：

- AppSupport：`~/Library/Application Support/NousAgentRunner/<instance_id>/`
- 端口：
  - 优先读取：`runtime.json` 的 `listen_port`
  - 其次按优先级读取：`.env.local > .env.production > .env.development > .env.test` 中的 `NOUS_AGENT_RUNNER_PORT`
- token：读取 `token` 文件内容（纯文本）

相关文件：

- `runtime.json`：包含 `listen_addr/listen_port/pid/version/started_at`
- `token`：Bearer token（Runner 首次运行会生成并落盘，权限 `0600`）

Swift 集成可直接参考：`sdk/swift/NousAgentRunnerKit/Sources/NousAgentRunnerKit/NousAgentRunnerKit.swift`

---

## 3. 概念：Share / Image / Service

- **Share**：Host 目录白名单。仅白名单内路径才能用于：
  - `POST /v1/images/import` 的 `path`
  - ASP `source.type="path"` 输入
  - `POST /v1/services` 的 `rw_mounts`
- **默认 Share**：
  - 首次运行若无 Share，会自动加入（macOS）：`/Users`、`/Volumes`
  - 永远包含默认临时目录（可由 `GET /v1/system/paths` 取得）
- **默认只读**：所有 Share 会对 Service 容器以 `ro` 挂载；仅 `rw_mounts` 指定的子路径以 `rw` 挂载。
- **路径一致**：Share 在容器内的挂载目标路径与 Host 路径一致（例如 `/Users/alice/...` 在容器内同路径可用）。

---

## 4. API（v1）

### 4.1 System

#### `GET /v1/system/status`

返回：

- `version`：Runner 版本（当前为 `"0.1.0"`）
- `vm.state`：`running/stopped/not_created/unknown`（或其它小写状态；来自 Lima `status`）
- `vm.restart_required`：Share 变更后会置 `true`
- `services_running`：当前 Runner 记录的 `running` Service 数

示例：

```json
{
  "version": "0.1.0",
  "vm": {
    "state": "running",
    "restart_required": false,
    "backend": "lima",
    "guest_runnerd_port": 17777,
    "lima_instance_name": "nous-default",
    "lima_home_directory": "/Users/alice/Library/Caches/NousAgentRunner/default/lima"
  },
  "services_running": 1
}
```

#### `GET /v1/system/paths`

示例字段：

- `default_temp_dir`：默认临时目录（已加入 Share 白名单）
- `runnerd_log`：Runner 日志路径
- `lima_home_dir` / `lima_instance_dir`

#### `POST /v1/system/vm/restart`

语义：

- 重启 VM；若 `vm.restart_required=true`，会触发“重建实例”以应用新的 Share 挂载配置。

返回：

```json
{"ok": true}
```

---

### 4.2 Shares

#### `GET /v1/shares`

返回：

```json
{
  "shares": [
    {"share_id":"shr_...","host_path":"/Users"},
    {"share_id":"shr_...","host_path":"/Users/alice/Library/Caches/NousAgentRunner/default/SharedTmp"}
  ]
}
```

#### `POST /v1/shares`

请求：

```json
{"host_path":"/Users/alice/Work"}
```

约束：

- `host_path` 必须是**绝对路径**且为**可访问目录**
- 内部会做 canonicalize（解析 symlink），并用 canonical path 生成 `share_id`

返回：

- 若已存在：`vm_restart_required=false`
- 若新增成功：`vm_restart_required=true`（必须重启 VM 才会在 Guest/容器内生效）

```json
{"share":{"share_id":"shr_...","host_path":"/Users/alice/Work"},"vm_restart_required":true}
```

#### `DELETE /v1/shares/{share_id}`

返回：

```json
{"deleted":true,"vm_restart_required":true}
```

限制：

- 默认临时目录对应的 Share 不允许删除（会返回 `BAD_REQUEST`）

---

### 4.3 Images

#### `POST /v1/images/pull`

请求：

```json
{"ref":"docker.io/gravtice/nous-claude-agent-service:0.1.0"}
```

约束：

- `ref` 会做规范化：`gravtice/...` 会被视为 `docker.io/gravtice/...`
- 必须以 `NOUS_AGENT_RUNNER_REGISTRY_BASE` 为前缀（默认 `docker.io/gravtice/`），否则返回 `REGISTRY_NOT_ALLOWED`

返回（成功）：

```json
{"ok": true}
```

#### `POST /v1/images/import`

请求：

```json
{"path":"/Users/alice/Library/Caches/NousAgentRunner/default/SharedTmp/image.tar"}
```

约束：

- `path` 必须位于某个 Share 白名单目录下（否则 `PATH_NOT_ALLOWED`）

返回（成功）：

```json
{"ok": true, "output": "Loaded image: local/xxx:tag\n"}
```

#### `GET /v1/images`

返回：

```json
{"images":["docker.io/gravtice/nous-claude-agent-service:0.1.0","local/xxx:tag"]}
```

---

### 4.4 Services

#### `GET /v1/services/types/{service_type}/builtin_tools`

返回某个 service type 所支持的内置工具列表（用于 UI 做工具白名单配置）：

```json
{"type":"claude","builtin_tools":["Read","Write","Bash","AskUserQuestion"]}
```

#### `POST /v1/services`

语义：

- 创建一个 Agent Service（当前仅支持 `type="claude"`）
- 会在需要时自动启动/初始化 VM 与 Guest daemon

请求结构：

```json
{
  "type": "claude",
  "image_ref": "docker.io/gravtice/nous-claude-agent-service:0.1.0",
  "resources": {"cpu_cores": 2, "memory_mb": 1024, "pids": 256},
  "rw_mounts": ["/Users/alice/Work/project/output"],
	  "env": {"ANTHROPIC_API_KEY": "..."},
	  "service_config": {
	    "cwd": "/Users/alice/Work/project",
	    "permission_mode": "plan",
	    "mcp_servers": "/Users/alice/Work/mcp-servers.json",
	    "allowed_tools": ["Read","Glob","Grep","AskUserQuestion"],
	    "setting_sources": ["project"],
	    "agents": {
      "reviewer": {"description":"Code reviewer","prompt":"Review the diff.","tools":["Read","Grep"],"model":"sonnet"}
    }
  }
}
```

约束（当前实现）：

- `type`：必须为 `"claude"`
- `image_ref`：
  - 支持官方 registry（前缀 `NOUS_AGENT_RUNNER_REGISTRY_BASE`），或
  - 本地镜像命名空间：`local/*`
- `rw_mounts`：
  - 必须为绝对路径
  - 必须位于某个 Share 白名单目录下
  - Runner 会在 Host 上创建该目录（`mkdir -p`）并再次做 canonical 校验
- `env`：
  - key 不允许以 `NOUS_` 开头（保留给 Runner/Service 注入）
  - key 仅允许字母/数字/下划线（且不允许数字开头），数量与 value 大小有上限
- `service_config`：
  - 透传给容器内服务；对 `type="claude"`，会被解释为 Python Claude Agent SDK 的 `ClaudeAgentOptions`
  - 若 `mcp_servers` 是字符串路径，则必须位于 Share 白名单目录下（否则 `PATH_NOT_ALLOWED`）
  - `permission_mode` 可在会话中通过 ASP `permission_mode.set` 动态切换

返回：

```json
{
  "service_id": "svc_xxxxxxxxxxxx",
  "state": "running",
  "asp_url": "ws://127.0.0.1:<port>/v1/services/svc_xxxxxxxxxxxx/chat"
}
```

#### `GET /v1/services`

返回：

```json
{
  "services": [
    {"service_id":"svc_...","type":"claude","image_ref":"...","state":"running","created_at":"2026-01-23T00:00:00Z"}
  ]
}
```

#### `GET /v1/services/{service_id}`

返回：单个 `Service` 对象（同上）。

#### `DELETE /v1/services/{service_id}`

返回：

```json
{"deleted": true}
```

#### `POST /v1/services/{service_id}/snapshot`

请求：

```json
{"new_tag":"local/claude-agent-service:dev-20260123"}
```

约束：

- `new_tag` 必须以 `local/` 开头

返回：

```json
{"ok": true}
```

---

## 5. 推荐集成流程（最小闭环）

1. 发现 `<port>` 与 `<token>`（见第 2 节），调用 `GET /v1/system/status` 确认 Runner 可用
2. `POST /v1/shares` 加入你的工程目录；如返回 `vm_restart_required=true` 或 `system.status.vm.restart_required=true`，调用 `POST /v1/system/vm/restart`
3. `POST /v1/images/pull` 拉取官方镜像（或用 `images/import` 导入 `local/*`）
4. `POST /v1/services` 创建 service，拿到 `service_id/asp_url`
5. 用 ASP 打开 `asp_url` WebSocket 对话（见 `docs/v0.1.0/ASP.md`）
6. 用完后 `DELETE /v1/services/{service_id}`

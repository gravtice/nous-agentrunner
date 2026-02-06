# ASMP（Agent Service Management Protocol）

ASMP 是 Nous Agent Runner 的**控制面**协议：用于管理 VM、共享目录（Share）、镜像（Image）、以及 Agent Service 生命周期。

本文档以当前代码实现为准（`nous-agent-runnerd`，API 前缀固定为 `/v1`）。

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

兼容性约定：

- 老客户端应忽略未知字段（前向兼容）。
- 新能力优先通过 `GET /v1/system/status` 的 `capabilities` 探测（避免“猜版本”）。

---

## 2. 运行时发现（上游集成必读）

Runner 对外只监听本机环回地址，且不提供“命令行参数覆盖配置”。上游 App 需要通过**文件**发现端口与 token。

### 2.1 InstanceID（实例隔离）

Runner 支持 `instance_id` 隔离不同集成方的运行时数据目录。

- `instance_id` 来源优先级（零参数）：
  1) 若存在：从可执行文件同目录或 `../Resources/` 读取 `NousAgentRunnerConfig.json`：

```json
{"instance_id":"default"}
```

  2) 若缺失且 Runner 位于 macOS `.app` bundle 内：从 `Info.plist` 的 `CFBundleIdentifier` 派生稳定 `instance_id`：  
     `sha256(lowercase(bundle_id))` 的前 12 位 hex（用于避免目录/VM socket 路径过长）。
  3) 仍缺省：`instance_id = "default"`（仅用于开发；不建议生产使用）。

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

Swift 集成可参考：`sdk/swift/NousAgentRunnerKit/Sources/NousAgentRunnerKit/NousAgentRunnerKit.swift`  
TypeScript 集成可参考：`sdk/typescript/nous-agent-runner-sdk/src/runtime.ts`

---

## 3. 概念：Share / Image / Service

- **Share**：Host 目录白名单。仅白名单内路径才能用于：
  - `POST /v1/images/import` 的 `path`
  - ASP `source.type="path"` 输入
  - `POST /v1/services` 的 `rw_mounts`
  - Skills 安装（本地路径 source 必须位于 Share 白名单目录下）
- **默认 Share**：
  - 首次运行若无 Share，会自动加入（macOS）：`/Users`、`/Volumes`
  - 永远包含默认临时目录（可由 `GET /v1/system/paths` 取得）
- **默认只读**：所有 Share 会对 Service 容器以 `ro` 挂载；仅 `rw_mounts` 指定的子路径以 `rw` 挂载。
- **路径一致**：Share 在容器内的挂载目标路径与 Host 路径一致（例如 `/Users/alice/...` 在容器内同路径可用）。

---

## 4. API

### 4.1 System

#### `GET /v1/system/status`

返回（基线字段）：

- `version`：Runner 版本（例如 `"0.2.8"`）
- `vm.state`：`running/stopped/not_created/unknown`（或其它小写状态；来自 Lima `status`）
- `vm.restart_required`：Share 变更后会置 `true`
- `services_running`：当前 Runner 记录的 `running` Service 数

示例：

```json
{
  "version": "0.2.8",
  "vm": {
    "state": "running",
    "restart_required": false,
    "backend": "lima",
    "guest_runnerd_port": 17777,
    "lima_instance_name": "nous-default",
    "lima_home_directory": "/Users/alice/Library/Caches/NousAgentRunner/lima"
  },
  "services_running": 1
}
```

新增字段（v0.2.0+，可选）：

- `protocols`：Runner 对外提供的协议版本，例如 `{"asmp":"0.3.0","asp":"0.2.0"}`
- `capabilities`：语义能力探测（客户端应优先用它做分支，而不是猜版本），可能包含：
  - `single_ws_per_service`
  - `error_fatal_field`
  - `invalid_input_returns_done`
  - `service_idle_timeout`
  - `max_inline_bytes`
  - `max_ws_message_bytes`
  - `skills_install`
  - `tunnels_list`
  - `tunnels_by_host_port`

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
  ],
  "excludes": ["/Users/alice/.claude","/Users/alice/.codex"]
}
```

备注：

- `excludes` 为 **effective excludes**（用户配置 + 强制内置）。
- 强制内置 excludes（存在时）：
  - `<HOME>/.claude`
  - `<HOME>/.codex`
  - 不允许用户删除。

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

#### `PUT /v1/shares/excludes`

语义：

- 设置 Share excludes（目录黑名单）。命中 excludes 的目录及其子路径将对 VM/容器不可访问（权限拒绝 EACCES）。
- excludes 变更需要重启 VM 才能对所有 Service 完整生效（会返回 `vm_restart_required=true`）。
- 无论请求内容如何，effective excludes 永远包含强制内置 `<HOME>/.claude`、`<HOME>/.codex`（目录存在时）。

请求：

```json
{"excludes":["/Users/alice/.claude"]}
```

约束：

- 只支持目录（必须存在且可访问）
- 必须位于某个 Share 之下，且不能等于 Share root
- 不能与默认共享临时目录（`default_temp_dir`）重叠
- 请求 `excludes` 仅表示用户自定义 excludes；强制内置 excludes 会自动合并进 effective excludes

返回：

```json
{"excludes":["/Users/alice/.claude"],"vm_restart_required":true}
```

---

### 4.3 Images

#### `POST /v1/images/pull`

请求：

```json
{"ref":"docker.io/gravtice/nous-claude-agent-service:0.2.8"}
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

#### `POST /v1/images/prune`

语义：

- 在 VM 内执行 `nerdctl image prune`，清理未使用镜像以释放磁盘空间。

请求（可选）：

```json
{"all": true}
```

参数：

- `all`：
  - `true`（默认）：等价 `nerdctl image prune -a -f`（删除所有未被容器引用的镜像）
  - `false`：等价 `nerdctl image prune -f`（仅删除 dangling 镜像）

返回（成功）：

```json
{"ok": true, "all": true, "output": "Total reclaimed space: ...\n"}
```

备注：

- 不会删除正在运行容器所使用的镜像。
- 被清理的官方/离线镜像会在后续创建 Service 时按需重新 `pull/import`。

#### `GET /v1/images`

返回：

```json
{"images":["docker.io/gravtice/nous-claude-agent-service:0.2.8","local/xxx:tag"]}
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

请求结构（v1 基线）：

```json
{
  "type": "claude",
  "image_ref": "docker.io/gravtice/nous-claude-agent-service:0.2.8",
  "resources": {"cpu_cores": 2, "memory_mb": 1024, "pids": 256},
  "rw_mounts": ["/Users/alice/Work/project/output"],
  "env": {"ANTHROPIC_AUTH_TOKEN": "..."},
  "service_config": {"cwd": "/Users/alice/Work/project", "mcp_servers": {}}
}
```

可选字段（v0.2.0+）：

- `idle_timeout_seconds`：`int`，默认 `0`（禁用）。

示例：

```json
{
  "type": "claude",
  "image_ref": "docker.io/gravtice/nous-claude-agent-service:0.2.8",
  "resources": {"cpu_cores": 2, "memory_mb": 1024, "pids": 256},
  "rw_mounts": ["/Users/alice/Work/project/output"],
  "env": {"ANTHROPIC_AUTH_TOKEN": "..."},
  "service_config": {"cwd": "/Users/alice/Work/project"},
  "idle_timeout_seconds": 600
}
```

重点：开启 thinking（强烈建议）

- 默认不启用：若未设置 `max_thinking_tokens`，ASP 侧不会收到 `response.thinking.delta`。
- 推荐方式：在 `service_config` 里显式设置 `max_thinking_tokens`（正整数）：

```json
{"service_config":{"max_thinking_tokens":8000}}
```

其中 `type="claude"` 的 `service_config`（Claude Agent SDK: `ClaudeAgentOptions`）常用示例：

```json
{
  "cwd": "/Users/alice/Work/project",
  "model": "sonnet",
  "fallback_model": "haiku",
  "max_thinking_tokens": 8000,
  "permission_mode": "acceptEdits",
  "allowed_tools": ["Skill", "Read", "Write", "Bash", "AskUserQuestion", "mcp__playwright__*"],
  "setting_sources": ["project"],
  "mcp_servers": {
    "playwright": {
      "command": "npx",
      "args": ["-y", "@playwright/mcp@latest"]
    }
  },
  "agents": {
    "reviewer": {
      "description": "Code reviewer",
      "prompt": "Review the diff.",
      "tools": ["Read", "Grep"],
      "model": "sonnet"
    }
  }
}
```

官方参考：<https://platform.claude.com/docs/en/agent-sdk/python#claude-agent-options>

工具配置（`allowed_tools` / `disallowed_tools`）：

- `permission_mode` 仅控制 Claude Code 的交互确认模式（`bypassPermissions/acceptEdits/...`），不会自动放开 MCP。
- MCP 工具默认不对模型暴露：要启用 MCP，必须在 `service_config.allowed_tools` 中显式允许对应 `mcp__...` 工具名（支持 glob）。
- `allowed_tools` 字段一旦存在，即视为启用 allowlist：只有命中的工具才允许调用（内置工具 + MCP 工具都会受影响）。
  - **快捷方式（推荐）**：`allowed_tools: ["*"]` 表示“全开工具”。Runner 会透传该值，`claude-agent-service` 会将其展开为：
    - 全量内置工具（等价于 `GET /v1/services/types/claude/builtin_tools` 返回的工具集合）
    - `mcp_servers` 中每个 server 的 `mcp__<server>__*`
  - 想“全开”内置工具（不使用快捷方式）：先调用 `GET /v1/services/types/claude/builtin_tools` 获取列表，再把返回的 `builtin_tools` 全量写入 `allowed_tools`。
  - 想“全开”某个 MCP server 的工具：追加 `mcp__<server>__*`（例如 `mcp__playwright__*`）。
  - `allowed_tools: []` 等价于“启用 allowlist 但不放行任何工具”（内置与 MCP 都会被禁用），一般不要这么配。
- `disallowed_tools` 用于显式禁用（优先级高于 `allowed_tools`），同样支持 glob。

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
  - key 不允许以 `NOUS_RUNNER_` 开头（保留给 Runner/Service 注入）
  - Runner 注入的变量（容器内可见）：
    - `NOUS_RUNNER_SERVICE_PORT`
    - `NOUS_RUNNER_SERVICE_CONFIG_B64`
    - `NOUS_RUNNER_SHARE_DIRS_B64`
    - `NOUS_RUNNER_MAX_INLINE_BYTES`
  - key 仅允许字母/数字/下划线（且不允许数字开头），数量与 value 大小有上限
- `service_config`：
  - 透传给容器内服务；对 `type="claude"`，会被解释为 Python Claude Agent SDK 的 `ClaudeAgentOptions`
  - `mcp_servers`：
    - 支持 dict（推荐）：直接写 `{ "<server_name>": { ... }, ... }`
      - 兼容 Claude Code 配置格式：若 dict 顶层包含 `mcpServers`，`claude-agent-service` 会自动取其值作为 server 列表
    - 支持 string（文件路径）：指向 Claude Code MCP config JSON（顶层应包含 `mcpServers`），路径需位于 Share 白名单下
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

#### `POST /v1/services/{service_id}/stop`

语义：

- 停止一个已创建的 Agent Service（容器 stop，但不删除）
- 用于“会话可恢复但不占用运行资源”的场景

返回：

```json
{"service_id":"svc_...","state":"stopped"}
```

#### `POST /v1/services/{service_id}/start`

语义：

- 启动/恢复一个已停止的 Agent Service（容器 start）

返回：

```json
{"service_id":"svc_...","state":"running"}
```

#### `POST /v1/services/{service_id}/resume`

语义：

- 启动一个已存在的 Agent Service（容器 `start`），用于继续使用既有 `service_id/session_id`
- 不会重建容器；若 Guest 侧已丢失该 service，会返回 `RESUME_UNAVAILABLE`，需要重新创建 service

返回：

```json
{"service_id":"svc_...","state":"running","asp_url":"ws://127.0.0.1:<port>/v1/services/svc_.../chat"}
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

#### Service idle auto-stop（v0.2.0+）

当满足：

- service `state=running`
- `idle_timeout_seconds>0`
- 无活跃 ASP WS 连接
- `now - last_activity_at >= idle_timeout_seconds`

Runner 会对该 service 执行 stop，并写入 `stop_reason="idle_timeout"`。

Service 对象（`GET /v1/services` / `GET /v1/services/{id}`）可能新增（可选）：

- `idle_timeout_seconds`
- `last_activity_at`：RFC3339 时间字符串
- `stop_reason`：`manual | idle_timeout | unknown`（仅在 `state=stopped` 时有意义）

---

### 4.5 Tunnels（Host → Guest 端口映射）

用于把 **Host 上的本地服务**（例如 App 内置的 MCP Server，仅监听 `127.0.0.1`）映射到 Guest/容器可访问的 `127.0.0.1:<port>`。

#### `POST /v1/tunnels`

请求：

```json
{"host_port":7001}
```

返回：

```json
{"tunnel_id":"tun_...","host_port":7001,"guest_port":18080,"state":"running","created_at":"2026-01-23T00:00:00Z"}
```

说明：

- `guest_port` 可直接写入 `service_config.mcp_servers.*.url`（容器侧以 `--network=host` 运行时，访问 `127.0.0.1:<guest_port>` 即可命中转发）。
- 当前实现通过 Lima 的 SSH 连接创建 remote port forward（`ssh -R 127.0.0.1:<guest_port>:127.0.0.1:<host_port>`），依赖 VM sshd 允许 TCP forwarding。

#### `GET /v1/tunnels`（v0.2.0+，可用性由 `capabilities.tunnels_list` 指示）

语义：

- 列出当前 Runner 维护的 tunnels（仅返回当前可用/运行中的 entries）

返回：

```json
{
  "tunnels": [
    {
      "tunnel_id": "tun_...",
      "host_port": 9222,
      "guest_port": 18080,
      "state": "running",
      "created_at": "2026-01-30T00:00:00Z"
    }
  ]
}
```

#### `GET /v1/tunnels/by_host_port/{host_port}`（v0.2.0+，由 `capabilities.tunnels_by_host_port` 指示）

语义：

- 按 `host_port` 获取 tunnel（用于客户端在不持久化 `tunnel_id` 的情况下恢复状态）

返回：

```json
{"tunnel_id":"tun_...","host_port":9222,"guest_port":18080,"state":"running","created_at":"2026-01-30T00:00:00Z"}
```

错误：

- 不存在：`404 NOT_FOUND`
- 参数非法：`400 BAD_REQUEST`

#### `DELETE /v1/tunnels/by_host_port/{host_port}`（v0.2.0+）

语义：

- 按 `host_port` 删除 tunnel（用于客户端锁/解锁或资源回收）

返回：

```json
{"deleted": true}
```

#### `DELETE /v1/tunnels/{tunnel_id}`

返回：

```json
{"deleted": true}
```

---

### 4.6 Skills（v0.3.0+，由 `capabilities.skills_install` 指示）

> 目的：从仓库（或本地路径）发现并安装 skills 到 Runner 的 skills 目录，供上层 UI/工具链使用。

#### `GET /v1/skills`

列出 Runner 实例已安装的 skills（目录名即 skill 名称）。

返回：

```json
{
  "skills": [
    {
      "name": "frontend-design",
      "has_skill_md": true,
      "source": {
        "source": "vercel-labs/agent-skills",
        "url": "https://github.com/vercel-labs/agent-skills.git",
        "ref": "main",
        "subpath": "",
        "commit": "abc123...",
        "skill_path": "skills/frontend-design",
        "installed_at": "2026-01-28T00:00:00Z"
      }
    }
  ]
}
```

#### `POST /v1/skills/discover`

从一个 source（仓库/路径）发现 skills，但**不安装**；用于 UI 展示列表并让用户选择后再调用 install。

请求：

```json
{
  "source": "remotion-dev/skills",
  "ref": "",
  "subpath": ""
}
```

返回：

```json
{
  "skills": [
    {
      "install_name": "remotion",
      "name": "remotion-best-practices",
      "description": "Best practices for Remotion - Video creation in React",
      "skill_path": "skills/remotion"
    }
  ],
  "commit": "abc123..."
}
```

- `install_name`：Runner 安装时使用的目录名（也是后续 `DELETE /v1/skills/{skill_name}` 的参数）。

#### `POST /v1/skills/install`

从一个 source（仓库/路径）发现并安装 skills 到 Runner 的 skills 目录。

请求：

```json
{
  "source": "remotion-dev/skills",
  "ref": "",
  "subpath": "",
  "skills": [],
  "replace": false
}
```

字段说明：

- `source`：必填。支持的格式见下文 **Source 格式**。
- `ref`：可选。覆盖 `source` 内自带的 ref（如 `.../tree/<ref>/...`）。
- `subpath`：可选。覆盖 `source` 内自带的 subpath。
- `skills`：可选。若为空/缺省：安装所有发现到的 skills；否则只安装列表内匹配的 skill（仅匹配 discover 返回的 `install_name`，大小写不敏感）。
- `replace`：可选。默认 `false`。若 `true`：覆盖已存在的 skill 目录。

返回：

```json
{
  "installed": ["frontend-design", "skill-creator"],
  "commit": "abc123..."
}
```

错误（示例）：

- `409 SKILL_EXISTS`：目标 skill 已存在且 `replace=false`
- `404 SKILLS_NOT_FOUND`：未发现任何 skill
- `400 PATH_NOT_ALLOWED`：本地路径 source 不在任何 Share 白名单目录下

#### `DELETE /v1/skills/{skill_name}`

删除已安装 skill（`skill_name` 为安装目录名）。

返回：

```json
{"deleted": true}
```

#### Source 格式（兼容常用输入）

Runner 支持以下常见输入：

- GitHub shorthand：`owner/repo` 或 `owner/repo/subpath`
- GitHub @skill：`owner/repo@skill-name`（等价于 `skills=["skill-name"]`）
- GitHub URL：
  - `https://github.com/owner/repo`
  - `https://github.com/owner/repo/tree/<ref>`
  - `https://github.com/owner/repo/tree/<ref>/<subpath>`
- GitLab URL：
  - `https://gitlab.com/owner/repo`
  - `https://gitlab.com/owner/repo/-/tree/<ref>`
  - `https://gitlab.com/owner/repo/-/tree/<ref>/<subpath>`
- 其它 git URL（兜底）：必须满足其一
  - 含 `://`（例如 `https://...` / `ssh://...` / `file://...`）
  - 以 `git@` 开头
  - 以 `.git` 结尾
- 本地路径：必须是绝对路径，且路径必须位于某个 Share 白名单目录下（避免绕过挂载白名单把宿主机任意文件“搬进” skills 目录）。

#### Skills 发现规则（简述）

- 若 `subpath` 指向的目录本身含 `SKILL.md`：视为单个 skill。
- 否则按优先目录扫描（只看一层子目录）：`./skills/`、`./.codex/skills/`、`./.claude/skills/` 等。
- 若优先目录扫描未发现任何 skill：再做递归搜索（最大深度 5），并跳过常见无关目录：`node_modules`、`.git`、`dist`、`build`、`__pycache__`。
- skill 目录名必须满足：`[A-Za-z0-9._-]+` 且不以 `.` 开头（否则会被忽略）。

#### 安装落盘位置

- macOS：`~/Library/Application Support/NousAgentRunner/<instance_id>/skills/<skill_name>/`
- Runner 会在安装目录内写入 `.nous-source.json`，用于记录来源信息与后续管理。

---

## 5. 推荐集成流程（最小闭环）

1. 发现 `<port>` 与 `<token>`（见第 2 节），调用 `GET /v1/system/status` 确认 Runner 可用
2. `POST /v1/shares` 加入你的工程目录；如返回 `vm_restart_required=true` 或 `system.status.vm.restart_required=true`，调用 `POST /v1/system/vm/restart`
3. （可选）`POST /v1/images/pull` 预拉取官方镜像；`POST /v1/services` 在缺镜像时也会自动导入离线镜像或拉取在线镜像
4. （可选）若需要把 Host 本地服务暴露给容器：`POST /v1/tunnels`
5. `POST /v1/services` 创建 service，拿到 `service_id/asp_url`
6. 用 ASP 打开 `asp_url` WebSocket 对话（见 `docs/ASP.md`）
7. 结束后根据需求：
   - 暂停保留：`POST /v1/services/{service_id}/stop`
   - 删除释放：`DELETE /v1/services/{service_id}`

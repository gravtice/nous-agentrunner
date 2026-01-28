# Moltbot 集成 Nous Agent Runner（方案记录 / Codex）

> 结论：可行，且建议 **通过 Moltbot 插件（plugin）实现**，避免改 Moltbot 核心。

## 0. 参考版本与范围

- Moltbot 源码：`temp/moltbot`（分支 `main`，commit `9688454a30e618e878ca795fbe46da58b2e2e9d3`）
- Nous Agent Runner（本仓库）协议：
  - ASMP：`docs/v0.1.0/ASMP.md`
  - ASP：`docs/v0.1.0/ASP.md`

本方案目标是：让 Moltbot 在不侵入其核心 Agent Runner（Pi/CLI）实现的前提下，**把 Nous Agent Runner 当作“外部隔离子代理”**进行调用，从而获得：

- 更强的隔离（VM + 容器）
- 更明确的文件访问边界（Share 白名单 + rw_mounts）
- 可把重任务/高风险工具执行放到隔离环境

## 1. Moltbot 现状观察（可用扩展点）

Moltbot 已有成熟的插件系统，可用于“新增工具/命令/服务”：

- 插件 API 类型定义：`temp/moltbot/src/plugins/types.ts`
  - `registerTool(...)`：注册 Agent tool
  - `registerCommand(...)`：注册 `/command`（绕过 LLM，优先于内置命令/Agent）
  - `registerService(...)`：注册后台服务（可选）
- 插件 tool 的 allowlist/optional gating：`temp/moltbot/src/plugins/tools.ts`
  - `optional: true` 的 tool 默认不暴露给模型，必须被 allowlist 明确放行
  - 这点非常适合作为“高风险/高权限/外部执行”能力的默认防线
- 插件命令注册与保留命令拦截：`temp/moltbot/src/plugins/commands.ts`
- 现成“插件注册工具”的最小范例：`temp/moltbot/extensions/llm-task/index.ts`

结论：**把 Nous Agent Runner 适配成一个 Moltbot plugin**，并以“可选 tool + 可选命令”的方式暴露，是最小改动、最符合 Moltbot 现有治理（allowlist / policy）的集成路径。

## 2. Nous Agent Runner 能提供什么（对 Moltbot 的增益）

Nous Agent Runner 对外提供两层接口：

- ASMP（HTTP 控制面）：创建/启动/停止/删除 service，管理 shares/images
  - 运行时发现：通过文件读取端口与 token（零参数）`docs/v0.1.0/ASMP.md`
  - 创建 service：`POST /v1/services`（目前 v1 只支持 `type="claude"`）`docs/v0.1.0/ASMP.md`
- ASP（WebSocket 数据面）：对某个 `service_id` 做流式对话
  - `ws://127.0.0.1:<port>/v1/services/{service_id}/chat` `docs/v0.1.0/ASP.md`
  - 支持文本 + 文件（path/bytes）输入；输出 `response.delta`/`response.final`/`done`

对 Moltbot 的关键增益在于：**把工具执行与文件读写放到隔离域**，且受 Share 白名单与 rw_mounts 约束；同时保留 Moltbot 原有 channel/记忆/会话编排能力。

## 3. 推荐集成架构（KISS）

### 3.1 做成一个 Moltbot 插件：`nous-agent-runner`

插件提供两种入口（都可做成 optional + allowlist）：

1) Tool：`nous.run`（给 Moltbot 主 Agent 调用）
2) 命令：`/nous`（给人手触发：status/run/stop/cleanup）

### 3.2 最小闭环：一次性调用（PoC 优先）

PoC 阶段建议 **每次调用创建一个 service，跑完即删除**：

1. 发现 Runner：读 `runtime.json` + `token`（ASMP“零参数发现”）
2. `POST /v1/services` 创建临时 service
3. 连接 ASP WS：发送 `input`，流式收 `response.delta`，最终拿到 `response.final`，等待 `done`
4. `DELETE /v1/services/{service_id}` 清理

优点：实现最简单，不会踩 ASP 的并发限制；缺点：启动成本更高（但 PoC 可接受）。

### 3.3 生产化：会话绑定 + 资源复用（第二阶段）

当 PoC 跑通后，再做复用以降低开销：

- 以 Moltbot `sessionKey` 作为 key，维护 `sessionKey -> service_id` 映射
- ASP 限制：同一 `service_id` 同时只允许一条 WS（`docs/v0.1.0/ASP.md`）
  - 对每个 `service_id` 做互斥锁/队列，避免并发输入
  - 提供 `/nous reset` 触发重新创建（或 `stop`/`resume`）
- 失败策略：WS 断开/错误码 fatal 时，丢弃并重建 service

## 4. 配置与安全边界（必须默认保守）

### 4.1 默认安全策略（建议硬编码默认值，允许配置放宽）

- `rw_mounts` 默认 **空**（只读），写入能力必须显式配置
- `permission_mode` 默认 `plan`（或至少不要默认 `bypassPermissions`）
- `allowed_tools` 默认收敛（例如先只开 `Read/Write/Bash/AskUserQuestion` 的子集；PoC 可先只开 `Read`）
- 超时：对每次 run 设置 `timeoutMs`，避免资源泄漏

### 4.2 Tool 暴露策略（依赖 Moltbot 现成治理）

- 将 `nous.run` 注册为 `optional: true`，默认不提供给模型
- 只有当用户在 Moltbot config 的 tools allowlist 中显式加入该 tool，才允许模型调用
  - 这是最重要的“防止意外外部执行”的安全阀

### 4.3 凭据注入（高风险点）

创建 service 需要 `env`（例如 `ANTHROPIC_API_KEY`）。必须遵循：

- 不从聊天内容里直接解析/回显 token
- 插件日志中做敏感信息脱敏
- 优先支持从 Moltbot 的 auth profiles/配置中读取（若后续要做深度集成）

## 5. 建议的对外能力形态（让 Moltbot“变强”但不乱）

`nous.run` 适合做“外部隔离执行器”，典型用途：

- 运行高风险工具链（shell、编译、抓取、OCR、浏览器自动化）但把执行限制在 VM/容器内
- 对大文件使用 `source.type=path`，在 share 白名单内做处理（避免把内容塞进 prompt）
- 当主 Moltbot agent 的工具策略严格时，把“危险动作”委派给 `nous.run`，并要求其以 `plan` 输出 + 由主 agent 二次确认

## 6. 实施步骤（最小可交付）

### Stage A：PoC（1~2 天量级）

- 新建 Moltbot 插件目录（或 workspace plugin），包含：
  - `moltbot.plugin.json`（config schema）
  - `index.ts` 注册 tool + `/nous` 命令
- 实现 ASMP 客户端：
  - 运行时发现（读文件）
  - `POST /v1/services` / `DELETE /v1/services/{id}`
- 实现 ASP 客户端：
  - WS 连接、发送 `input`、聚合 `response.delta`/`response.final`、等待 `done`
- 输出格式：
  - tool 返回结构化结果（例如 `text` + `details.json`），避免把长日志硬塞回聊天

### Stage B：可用化（后续）

- 复用 service（sessionKey -> service_id），加锁/队列
- 增加 `/nous status`（查看 runner 状态、service 列表、当前绑定）
- 错误码分级与重试策略（ASMP/ASP）
- 单元测试（mock ASMP/ASP server）

## 7. 未决问题（需要你拍板）

1) Moltbot 与 Runner 的部署关系：
   - 只支持 macOS Apple Silicon（Runner v1 平台限制），还是要做“插件可选启用 + 其它平台降级”为纯本地 Moltbot？
2) service 类型：
   - v1 只支持 `type="claude"`；是否允许未来扩展到 `openai` 等？
3) 文件读写策略：
   - `rw_mounts` 是否允许按 workspace 自动推导？（建议默认禁止，必须显式配置）


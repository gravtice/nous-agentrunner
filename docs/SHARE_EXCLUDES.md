# Share Excludes（目录黑名单）设计方案

## 背景

- 现状：Host 侧通过 Lima 启动 VM，并使用 VirtioFS 共享目录。
- 路径透明策略：
  - Guest 内 mountPoint = `host_path`（同绝对路径）。
  - Container 内 bind mount dst = `host_path`（同绝对路径，默认只读；`rw_mounts` 再覆盖为可写）。
- 默认 share（darwin）：`/Users`、`/Volumes`（等价于“默认把用户 home 全挂进 VM/容器”）。

## 需求

在维持“同路径透明”不变的前提下，新增一组目录黑名单（`excludes`）：

- `excludes` 中的目录必须是某个 share 的子路径（严格子目录）。
- 命中 `excludes` 的目录及其子路径：
  - VM 内不可访问（以权限拒绝 EACCES 表现即可）。
  - Container 内不可访问（同样以 EACCES 表现）。
  - 所有通过 API/配置进入系统的路径参数（如 `rw_mounts`、大文件 `path`、技能源路径等）若命中黑名单，直接拒绝（`PATH_NOT_ALLOWED`）。
- `excludes` **只针对目录**；不支持文件级别、不支持 glob/regex。
- 额外提供一组**强制内置 excludes**（不可删除）：
  - `<HOME>/.claude`
  - `<HOME>/.codex`
  - `HOME` 取 Host 上运行 runnerd 的用户 home（随安装机器变化，不写死开发机路径）。
  - 仅当目录存在且满足 excludes 校验规则时加入 effective excludes。

## 非目标（明确不做）

- 不追求 `ENOENT`/目录项隐藏语义（需要目录项过滤，复杂且脆弱）。
- 不做 per-service excludes（全局即可）。
- 不做热更新：`shares.json` 变更后需要重启/重建 VM（与 share 变更同级）。

## 配置：扩展现有 `shares.json`

文件位置保持不变：`<AppSupportDir>/shares.json`。

向后兼容扩展：

```json
{
  "shares": [
    { "share_id": "shr_abcdef012345", "host_path": "/Users" }
  ],
  "excludes": [
    "/Users/zengjice/.claude"
  ]
}
```

- `excludes` 为字符串数组，每项为 Host 绝对路径目录。
- 语义：前缀匹配。例：排除 `/Users/zengjice/.claude` 等价于排除该目录及其所有子路径。
- `shares.json` 仅持久化**用户自定义** `excludes`；强制内置 excludes 不写入该文件，但会体现在 `GET /v1/shares` 的返回（effective excludes）。

## 归一化与校验（Host 侧）

对每个 exclude `p` 做如下处理，保证行为确定且避免绕过：

### 1) 基础格式

- trim 空白
- `filepath.Clean(p)`
- 必须是绝对路径

### 2) share 关系约束

- `p` 必须是某个 share 的**严格子路径**（`p != share_root`）。
- 需要通过“mount namespace 前缀”检查：`p` 必须落在某个 share 的 `HostPath` 前缀下（避免配置一个 VM 内根本不会出现的路径）。

### 3) canonical 安全约束

- 计算 `p` 的 canonical path（解析 `..`、解析 symlink）用于安全判断：
  - 若 `p` 存在：`EvalSymlinks(p)`
  - 若 `p` 不存在：使用“最近存在父目录 canonical + suffix”的方式推导（可复用现有 `canonicalizePathForCreate` 思路）
- canonical `p` 必须落在某个 share 的 canonical root 前缀下。

### 4) 收敛规则（去冗余）

- 去重
- 若 A 是 B 的前缀，仅保留 A（B 冗余）

### 5) 保护路径（必须可用）

为避免自毁配置，禁止 excludes 覆盖 Runner 必需目录：

- `DefaultSharedTmpDir`（用于 Host->Guest 传递 guest-runnerd 二进制等关键文件）

若命中保护路径：拒绝加载/写入配置，并返回明确错误。

## 生效语义与优先级

- Denylist 永远优先：任何请求/挂载若落在 excludes 下都必须失败或被覆盖屏蔽。
- 运行时表现：以 `EACCES`（权限拒绝）为准；不追求 `ENOENT`。
  - 备注：父目录 `readdir` 仍可看到该目录名，这是预期行为。

## 执行分层（必须三层都做）

不要依赖“bind mount 是否递归携带子 mount”等运行时细节；按以下三层同时生效。

### A) Host runnerd：所有路径入口统一拒绝

对所有进入系统的路径参数，在完成 canonical 校验后，加一条 excludes 前缀判断：

- 命中 excludes：直接返回 `PATH_NOT_ALLOWED`（可附带命中的 exclude 便于定位）。

覆盖点至少包括：

- service create：`rw_mounts[]`
- service_config 中涉及路径的字段（如 `mcp_servers` 为路径时）
- 离线镜像 tar `path`
- 技能安装/发现的本地路径输入（若有）

### B) Guest（VM）层：覆盖挂载实现 EACCES

目的：VM 内任何进程都无法访问被排除目录内容。

方案（KISS）：

1. 创建一个 deny 源目录（VM 本地，不依赖 Host）：
   - 例：`/run/nous-deny/dir`
   - 权限设为 `000`
2. 对每个 exclude `p`（Guest 内同路径）做覆盖挂载：
   - `mount --bind /run/nous-deny/dir <p>`

若 `<p>` 不存在：

- 默认不创建（避免在 VirtioFS share 上 mkdir 产生 Host 侧副作用），仅记录 warning。
- 安全性主要由容器层保证（见下一节）；VM 层尽量覆盖已有目录即可。

### C) Container 层：显式更具体 bind mount 覆盖

目的：即使容器运行时 bind mount 不是递归的，也不能绕过 VM 层的 sub-mount。

方案：

- 在容器启动 mounts 列表的最后，追加每个 exclude 的覆盖挂载：
  - `type=bind,src=/run/nous-deny/dir,dst=<exclude>,ro`

挂载顺序要求：

1. share roots（ro）
2. `rw_mounts`（rw）
3. excludes 覆盖挂载（deny，最后追加，优先级最高）

## VM 重启语义

- shares 与 excludes 都是 VM “启动/创建时一次性配置”的大项。
- 任一变化都需要 VM 重启/重建才能完全生效。
- API 层面应返回 `vm_restart_required=true`（与 shares 变更一致）。

## 示例

- share：`/Users`
- builtin excludes：`["<HOME>/.claude","<HOME>/.codex"]`
- user excludes：`["<HOME>/Work/private"]`（示例）

预期：

- VM：访问 `<HOME>/.claude` / `<HOME>/.codex` / `<HOME>/Work/private` 返回 `Permission denied`（EACCES）
- Container：访问同路径返回 `Permission denied`（EACCES）
- API：任何请求引用上述路径（或其子路径）返回 `PATH_NOT_ALLOWED`

## 已知限制（接受，不加戏）

- EACCES 语义下，父目录仍能看到被排除目录名（不是 ENOENT）。
- 该机制不对 VM 内特权进程提供强隔离；目标是阻断普通 service 进程访问。
- excludes 为“路径前缀”语义，不做 inode 等价/别名路径穷举（保持简单、可预测）。

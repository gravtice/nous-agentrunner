# ASMP（Agent Service Management Protocol）v0.3.0

本版本文档是 **v0.2.0 的增量说明**；完整 API 见 `docs/v0.1.0/ASMP.md`。

---

## 1. 兼容性约定

- v0.3.0 **不改 URL/鉴权/错误结构**（仍为 `/v1` + `Authorization: Bearer <token>`）。
- v0.3.0 新增 Skills 管理接口；老客户端不受影响。
- 能力发现：`GET /v1/system/status` 的 `capabilities.skills_install=true` 表示 Runner 支持本版本新增的 skills 安装能力。

---

## 2. Skills：从仓库安装 Skills（新增）

### 2.1 `GET /v1/skills`

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

### 2.2 `POST /v1/skills/discover`

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
- `name/description`：来自 `SKILL.md` YAML frontmatter（若缺失则可能为空）。

### 2.3 `POST /v1/skills/install`

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

- `source`：必填。支持的格式见 **2.3**。
- `ref`：可选。覆盖 `source` 内自带的 ref（如 `.../tree/<ref>/...`）。用于指定分支/标签/提交（尽力而为）。
- `subpath`：可选。覆盖 `source` 内自带的 subpath。用于指定仓库内子路径。
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

### 2.4 Source 格式（兼容 vercel-labs/skills 的常用输入）

Runner 支持以下常见输入（优先级与解析逻辑对齐 vercel-labs/skills）：

- GitHub shorthand：`owner/repo` 或 `owner/repo/subpath`
- GitHub @skill：`owner/repo@skill-name`（`skill-name` 解释为 `install_name`；等价于 `skills=["skill-name"]`）
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

### 2.5 Skills 发现规则（兼容 vercel-labs/skills 的 discoverSkills）

- 若 `subpath` 指向的目录本身含 `SKILL.md`：视为单个 skill。
- 否则按优先目录扫描（只看一层子目录）：`./skills/`、`./.codex/skills/`、`./.claude/skills/` 等（与 vercel-labs/skills 的优先目录列表对齐）。
- 若优先目录扫描未发现任何 skill：再做递归搜索（最大深度 5），并跳过常见无关目录：`node_modules`、`.git`、`dist`、`build`、`__pycache__`。
- skill 目录名必须满足：`[A-Za-z0-9._-]+` 且不以 `.` 开头（否则会被忽略）。

### 2.6 安装落盘位置

- macOS：`~/Library/Application Support/NousAgentRunner/<instance_id>/skills/<skill_name>/`
- Runner 会在安装目录内写入 `.nous-source.json`，用于记录来源信息与便于后续管理。

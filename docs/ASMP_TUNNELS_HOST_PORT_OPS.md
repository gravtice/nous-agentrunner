# ASMP：Tunnels 的 host_port 便捷接口（提案）

> **Status**: Draft（未实现）  
> **Last Updated**: 2026-01-30  
> 本文档记录对 ASMP `/v1/tunnels` 的两个兼容性增强提案：**按 `host_port` 删除** 与 **只读查询**。目标是降低上层集成（例如 CoWork 的 Browser Panel / CDP 锁）对 `tunnel_id` 状态持久化与异常恢复的复杂度。

---

## 1. 背景与问题

现状（以 `docs/v0.1.0/ASMP.md` 与当前实现为准）：

- 创建：`POST /v1/tunnels`（以 `host_port` 做幂等键）返回 `tunnel_id/guest_port`。
- 删除：仅支持 `DELETE /v1/tunnels/{tunnel_id}`。
- 查询：无 list/get API。

对上层集成的影响：

- 上层必须持久化 `tunnel_id`，否则锁/解锁（删 tunnel）时会陷入“丢失 tunnel_id”的恢复分支。
- 缺少只读查询，UI/诊断难以判断当前有哪些 tunnel、对应的 `guest_port` 是多少、是否仍“活着”。

---

## 2. 提案：新增 API（向后兼容）

### 2.1 `GET /v1/tunnels`（新增）

列出当前 Runner 维护的 tunnels。

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

建议语义：

- 仅返回“当前可用”的 tunnels（实现侧可通过 `done` channel / 进程存活做 best-effort 判定）。
- 若发现 stale entry（例如 tunnel 已退出），可在 list 时顺手清理以避免误导。

### 2.2 `GET /v1/tunnels/by_host_port/{host_port}`（新增）

按 `host_port` 获取 tunnel（用于上层在不持久化 `tunnel_id` 的情况下恢复状态）。

- 成功：返回 `Tunnel` 对象（同 `POST /v1/tunnels` 返回结构）
- 不存在：`404 NOT_FOUND`
- 参数非法：`400 BAD_REQUEST`

### 2.3 `DELETE /v1/tunnels/by_host_port/{host_port}`（新增）

按 `host_port` 删除 tunnel（用于上层锁/解锁或资源回收）。

返回：

```json
{"deleted": true}
```

错误：

- 不存在：`404 NOT_FOUND`
- 参数非法：`400 BAD_REQUEST`

---

## 3. 客户端收益（以 CoWork 为例）

- 锁/解锁只需要记住 `chrome_cdp_host_port`（本来就需要），无需再强依赖 `tunnel_id` 落盘与恢复。
- UI/诊断可直接查询：当前是否已暴露 CDP、暴露到哪个 `guest_port`、是否需要重建。

---

## 4. 实现建议（保持 KISS）

- 复用现有数据结构：`tunnelByHostPort` + `tunnels`。
- 抽出一个内部 helper：`findRunningTunnelByHostPort(hostPort) (*tunnelEntry, bool)`，并在内部做“stale 判定 + 清理”。
- `DELETE by_host_port` 可复用现有 `DELETE by tunnel_id` 的核心逻辑（避免两套取消/清理路径）。

（可选）能力发现：

- 若需要让客户端显式探测是否支持该提案，可在 `GET /v1/system/status` 的 `capabilities` 中增加标记：
  - `tunnels_list=true`
  - `tunnels_by_host_port=true`


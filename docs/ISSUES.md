# ISSUES（v0.1.0 / MVP）

记录开发过程中发现的 bug、技术债与可优化项（不代表本版本必须完成）。

## 技术债 / 可优化项

- [x] Host↔Guest 通信改为 `vsock + gRPC`：替换基于 `ssh.config` + SSH port-forward 的实现，减少依赖与边界复杂度。
- [ ] Guest→Host tunnel 目前依赖 Host `AF_VSOCK`：在不支持/不可用的环境下会自动禁用（`NOUS_AGENT_RUNNER_VSOCK_TUNNEL_PORT=-1`），但功能缺失；需要明确运行时要求或提供替代传输（例如基于 Lima/SSH 的反向转发）。

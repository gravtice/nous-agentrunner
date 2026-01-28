# ISSUES（v0.1.0 / MVP）

记录开发过程中发现的 bug、技术债与可优化项（不代表本版本必须完成）。

## 技术债 / 可优化项

- [ ] Host↔Guest 通信改为 `vsock + gRPC`：替换基于 `ssh.config` + SSH port-forward 的实现，减少依赖与边界复杂度。


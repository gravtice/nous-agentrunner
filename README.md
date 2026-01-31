# Nous Agent Runner

**Embed AI Agents in Your macOS App — Securely**

Nous Agent Runner is a lightweight runtime that lets you integrate AI agents (Claude, etc.) into your macOS applications with complete isolation. No complex infrastructure. No security headaches. Just a few API calls.

```swift
// Create an AI agent and start chatting
let service = try await client.createClaudeService(
    imageRef: "docker.io/gravtice/nous-claude-agent-service:0.2.4",
    rwMounts: ["/Users/alice/Projects"],
    serviceConfig: ["system_prompt": "You are a helpful coding assistant"]
)

let ws = WebSocket(url: service.aspURL)
ws.send(["type": "input", "contents": [["kind": "text", "text": "Refactor this code..."]]])
// Stream responses in real-time
```

## Why Nous Agent Runner?

| Challenge | Solution |
|-----------|----------|
| **AI agents need file access** | VirtioFS mounts with path whitelisting — agents see only what you allow |
| **Security is critical** | Kernel-level isolation via Linux VM + containerization |
| **Integration is complex** | Swift SDK with auto-discovery — 3 lines to get started |
| **Distribution is painful** | Single DMG packaging — embed runtime alongside your app |

## Features

- **Zero-Config Integration** — Auto-discovers runtime, no CLI arguments needed
- **Kernel-Level Isolation** — Linux VM via Apple Virtualization Framework
- **Container Security** — Each agent runs in its own container with resource limits
- **Path Whitelisting** — Explicit control over which directories agents can access
- **Streaming Responses** — Real-time text output via WebSocket
- **Multi-Modal Input** — Text, files, and images supported
- **Session Continuity** — Multi-turn conversations with disconnect recovery
- **Idle Auto-Stop** — Services automatically stop after inactivity
- **Extended Thinking** — Support for Claude's reasoning capabilities

## Quick Start

### 1. Download the Runtime

```bash
# Clone the repository
git clone https://github.com/gravtice/nous-agent-runner.git
cd nous-agent-runner

# Build binaries (requires Go 1.22+)
./scripts/macos/build_binaries.sh
```

### 2. Add Swift SDK to Your App

```swift
// Package.swift
dependencies: [
    .package(path: "path/to/nous-agent-runner/sdk/swift/NousAgentRunnerKit")
]
```

### 3. Integrate in 3 Steps

```swift
import NousAgentRunnerKit

// Step 1: Start the runtime
let daemon = try NousAgentRunnerDaemon()
let runtime = try await daemon.ensureRunning()

// Step 2: Create an agent service
let client = NousAgentRunnerClient(runtime: runtime)
let service = try await client.createService(
    type: "claude",
    imageRef: "docker.io/gravtice/nous-claude-agent-service:0.2.4",
    config: ClaudeServiceConfig(
        systemPrompt: "You are a helpful assistant",
        allowedTools: ["Read", "Write", "Bash"]
    )
)

// Step 3: Connect and chat
let ws = try await client.connectToService(service.id)
try await ws.send(input: "Hello, Claude!")

for try await message in ws.messages {
    switch message.type {
    case .responseDelta(let text):
        print(text, terminator: "")
    case .done:
        break
    }
}
```

## Architecture

```
┌─────────────────────────── Your macOS App ───────────────────────┐
│                                                                   │
│  ┌─ NousAgentRunnerKit (Swift SDK)                               │
│  │  └─ HTTP API (control) + WebSocket (chat)                     │
│  │                                                                │
└──┼────────────────────────────────────────────────────────────────┘
   │ localhost only
   ▼
┌─────────────────────────── Agent Runner ─────────────────────────┐
│  nous-agent-runnerd (Host Daemon)                                │
│  ├─ ASMP API: VM, services, images, shares                       │
│  ├─ ASP Gateway: WebSocket proxy to agents                       │
│  └─ Lima + AVF: Linux VM management                              │
│                                                                   │
│  ┌─────────────── Linux VM (Isolated) ──────────────────────┐    │
│  │  nous-guest-runnerd                                       │    │
│  │  └─ containerd + nerdctl                                  │    │
│  │                                                           │    │
│  │  ┌─ Agent Service Container ─────────────────────────┐   │    │
│  │  │  claude-agent-service (Claude SDK)                │   │    │
│  │  │  └─ Tools execute HERE, not on host               │   │    │
│  │  └───────────────────────────────────────────────────┘   │    │
│  │                                                           │    │
│  │  VirtioFS: /Users/alice/Projects → (same path in VM)     │    │
│  └───────────────────────────────────────────────────────────┘    │
└───────────────────────────────────────────────────────────────────┘
```

## API Overview

### Control Plane (ASMP)

HTTP/JSON API for managing the runtime:

| Endpoint | Purpose |
|----------|---------|
| `GET /v1/system/status` | Runtime status and capabilities |
| `POST /v1/shares` | Add directory to whitelist |
| `POST /v1/images/pull` | Pull agent service image |
| `POST /v1/services` | Create and start a service |
| `DELETE /v1/services/{id}` | Stop and remove a service |

### Data Plane (ASP)

WebSocket protocol for agent interaction:

| Message Type | Direction | Purpose |
|--------------|-----------|---------|
| `input` | Client → Agent | Send user message (text/files) |
| `response.delta` | Agent → Client | Streaming text output |
| `tool.use` | Agent → Client | Agent called a tool |
| `agent.ask` | Agent → Client | Agent needs user input |
| `done` | Agent → Client | Request complete |

Full protocol documentation: [`docs/v0.2.0/ASMP.md`](docs/v0.2.0/ASMP.md) | [`docs/v0.2.0/ASP.md`](docs/v0.2.0/ASP.md)

## Distribution

Package your app with the runtime embedded:

```bash
# Build everything
./scripts/macos/build_binaries.sh

# Package your app into a DMG
./scripts/macos/package_dmg.sh /path/to/YourApp.app

# Output: dist/YourApp.dmg (runtime included)
```

The packaged DMG contains everything needed — users don't need to install anything separately.

Note: macOS “Files and Folders” / “Full Disk Access” grants are tied to the app's code signature.
If you repackage with ad-hoc signing, the system may prompt again. To keep grants stable across updates,
sign with a real identity (set `NOUS_CODESIGN_IDENTITY` when running `./scripts/macos/package_dmg.sh`).

## Configuration

Configuration is file-based (zero CLI parameters):

```bash
# Priority: .env.local > .env.production > .env.development > .env.test
~/Library/Application Support/NousAgentRunner/<instance_id>/.env.local
```

Runtime paths are per-instance (based on `<instance_id>`):

- Config + state (macOS): `~/Library/Application Support/NousAgentRunner/<instance_id>/`
  - Config: `.env.local`, `.env.production`, `.env.development`, `.env.test`
  - Auth token: `token` (0600)
  - Runtime discovery: `runtime.json` (listen addr/port, pid, started_at)
- Logs (macOS): `~/Library/Logs/NousAgentRunner/<instance_id>/runnerd.log`
- Cache/temp (macOS): `~/Library/Caches/NousAgentRunner/`
  - Default temp dir (shared): `~/Library/Caches/NousAgentRunner/<instance_id>/SharedTmp/`
  - Lima home (shared across instances): `~/Library/Caches/NousAgentRunner/lima/`

You can query the exact paths at runtime via `GET /v1/system/paths`.

| Variable | Default | Description |
|----------|---------|-------------|
| `NOUS_AGENT_RUNNER_PORT` | Auto | HTTP API port |
| `NOUS_AGENT_RUNNER_VM_MEMORY_MB` | 4096 | VM memory allocation |
| `NOUS_AGENT_RUNNER_VM_CPU_CORES` | 4 | VM CPU cores |
| `NOUS_AGENT_RUNNER_REGISTRY_BASE` | `docker.io/gravtice/` | Approved image registry |

## Security Model

- **Localhost Only** — APIs bound to 127.0.0.1, no external exposure
- **Token Auth** — Per-instance bearer tokens, file-based (0600)
- **VM Isolation** — Kernel boundary via Apple Virtualization Framework
- **Container Isolation** — cgroups resource limits, filesystem bounds
- **Path Validation** — Canonical path resolution, symlink escape prevention
- **Read-Only Default** — Shares are read-only unless explicitly granted write access

## Requirements

- macOS 14.0+ (Sonoma)
- Apple Silicon (M1/M2/M3/M4)
- ~4GB RAM for VM (configurable)

## Project Structure

```
├── cmd/
│   ├── nous-agent-runnerd/      # Host daemon (Go)
│   └── nous-guest-runnerd/      # Guest daemon (Go)
├── sdk/swift/NousAgentRunnerKit/ # Swift SDK
├── services/claude-agent-service/ # Claude Agent Service (Python)
├── demo/macos/NousAgentRunnerDemo/ # Example SwiftUI app
├── docs/                         # Protocol specifications
└── scripts/macos/                # Build & packaging scripts
```

## Documentation

- [Implementation Plan](docs/v0.1.0/IMPLEMENTATION_PLAN.md) — Architecture design and rationale
- [ASMP Protocol](docs/v0.2.0/ASMP.md) — Control plane API reference
- [ASP Protocol](docs/v0.2.0/ASP.md) — Data plane WebSocket reference
- [Building Guide](docs/v0.1.0/BUILDING.md) — Build and packaging instructions
- [Demo App README](demo/macos/NousAgentRunnerDemo/README.md) — Integration example

## Roadmap

- [ ] Multi-VM isolation (per-service VM)
- [ ] OpenAI agent service
- [ ] Custom agent service templates
- [ ] Keychain integration for tokens
- [ ] Session persistence and recovery

## License

[Apache License 2.0](LICENSE)

---

**Built for developers who want AI agents in their apps without compromising on security.**

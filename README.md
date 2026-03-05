# Nous Agent Runner

**Embed AI Agents in Your macOS App with HTTP + WebSocket APIs — Securely**

Nous Agent Runner is a lightweight local runner platform that lets you integrate AI agents (Claude, etc.) into your macOS applications with complete isolation. The developer-facing API is split into HTTP/JSON control-plane endpoints (ASMP) and a WebSocket streaming data plane (ASP).

```swift
// Create an AI agent and start chatting
let service = try await client.createClaudeService(
    imageRef: "docker.io/gravtice/nous-claude-agent-service:0.2.10",
    rwMounts: ["/Users/alice/Projects"],
    env: ["ANTHROPIC_API_KEY": ProcessInfo.processInfo.environment["ANTHROPIC_API_KEY"] ?? ""],
    serviceConfig: ["system_prompt": "You are a helpful coding assistant"]
)

guard let serviceID = service["service_id"] as? String else {
    throw NousAgentRunnerError.invalidConfig("missing service_id")
}

let ws = try client.openChatWebSocket(serviceID: serviceID)
ws.resume()
try await ws.send(.string(#"{"type":"input","contents":[{"kind":"text","text":"Refactor this code..."}]}"#))
```

## API Model

- **ASMP (HTTP/JSON)** — manage VM, services, images, and shares.
- **ASP (WebSocket)** — stream chat input/output, tool events, and ask/answer interactions.

## Terminology

- **Nous Agent Runner** — product bundle you ship with your app.
- **Runner Daemon (`nous-agent-runnerd`)** — host-side local daemon that exposes ASMP and ASP gateway.
- **Agent Service** — per-agent container running inside the isolated Linux VM.
- **Runner Context (`NousAgentRunnerContext`)** — SDK-discovered connection metadata (`baseURL`, `token`, `instance_id`) for the daemon.
- **ASMP** — control plane API for lifecycle/infra operations.
- **ASP** — WebSocket data plane for interactive agent sessions.

## Typical Workflow

1. Build and bundle `nous-agent-runnerd` with your macOS app.
2. Start the daemon from your app (`ensureRunning`).
3. Create an agent service from a container image with explicit mounts.
4. Stream conversation over WebSocket (`response.delta` / `done`).

## Why Nous Agent Runner?

| Developer Concern | Nous Agent Runner Approach |
|-------------------|----------------------------|
| **Can I integrate fast in an existing app?** | Swift/TypeScript SDKs + daemon endpoint/token auto-discovery keep integration to a few API calls |
| **Can I stream responses in real time?** | ASP WebSocket channel provides low-latency `response.delta` event streaming |
| **Is isolation strong enough for local agent execution?** | Linux VM boundary (Apple Virtualization Framework) plus per-service containers |
| **Can I strictly control file access?** | VirtioFS mounts with canonical path validation and explicit share whitelisting |
| **Can I ship this without ops complexity?** | Bundle runner binaries into your app and distribute as a single DMG |

## Features

- **WebSocket Streaming Data Plane** — Real-time text/events over ASP (`/v1/services/{id}/chat`)
- **Zero-Config Integration** — Auto-discovers daemon endpoint and token, no CLI arguments needed
- **Kernel-Level Isolation** — Linux VM via Apple Virtualization Framework
- **Container Security** — Each agent runs in its own container with resource limits
- **Path Whitelisting** — Explicit control over which directories agents can access
- **Multi-Modal Input** — Text, files, and images supported
- **Session Continuity** — Multi-turn conversations with disconnect recovery
- **Idle Auto-Stop** — Services automatically stop after inactivity
- **Extended Thinking** — Support for Claude's reasoning capabilities

## Quick Start

### Prerequisites

- macOS 14.0+ on Apple Silicon
- Go 1.22+
- Git submodules initialized (`references/lima` is required)
- Node.js 18+ (only if you use the TypeScript SDK)

### 1. Clone and Prepare Source

```bash
git clone https://github.com/gravtice/nous-agent-runner.git
cd nous-agent-runner
git submodule update --init --recursive
```

### 2. Build Runner Binaries

```bash
./scripts/macos/build_binaries.sh
```

Build output goes to `dist/` (`nous-agent-runnerd`, `nous-guest-runnerd`, `limactl`, Lima guest assets).

### 3. Add SDK to Your App

Swift:

```swift
// Package.swift
dependencies: [
    .package(path: "path/to/nous-agent-runner/sdk/swift/NousAgentRunnerKit")
]
```

TypeScript (Node/Electron main process):

```bash
npm install nous-agent-runner-sdk
```

### 4. Integrate in 3 Steps (Swift)

```swift
import Foundation
import NousAgentRunnerKit

// Step 1: Start daemon and discover Runner Context
let daemon = try NousAgentRunnerDaemon()
let runnerContext = try await daemon.ensureRunning()

// Step 2: Create an agent service
let client = NousAgentRunnerClient(context: runnerContext)
let service = try await client.createClaudeService(
    imageRef: "docker.io/gravtice/nous-claude-agent-service:0.2.10",
    rwMounts: ["/Users/alice/Projects"],
    env: ["ANTHROPIC_API_KEY": ProcessInfo.processInfo.environment["ANTHROPIC_API_KEY"] ?? ""],
    serviceConfig: ["system_prompt": "You are a helpful assistant"]
)

// Step 3: Connect and chat
guard let serviceID = service["service_id"] as? String else {
    throw NousAgentRunnerError.invalidConfig("missing service_id")
}

let ws = try client.openChatWebSocket(serviceID: serviceID)
ws.resume()

try await ws.send(.string(#"{"type":"input","contents":[{"kind":"text","text":"Hello, Claude!"}]}"#))

while true {
    let frame = try await ws.receive()
    switch frame {
    case .string(let text):
        print(text)
    case .data(let data):
        print(String(decoding: data, as: UTF8.self))
    @unknown default:
        break
    }
}
```

### 5. Integrate (Node/Electron main process)

```ts
import { NousAgentRunnerDaemon, NousAgentRunnerClient } from "nous-agent-runner-sdk";

const daemon = new NousAgentRunnerDaemon();
const runnerContext = await daemon.ensureRunning();
const client = new NousAgentRunnerClient(runnerContext);

const service = await client.createClaudeService({
  imageRef: "docker.io/gravtice/nous-claude-agent-service:0.2.10",
  rwMounts: ["/Users/alice/Projects"],
  env: { ANTHROPIC_API_KEY: process.env.ANTHROPIC_API_KEY ?? "" },
  serviceConfig: { system_prompt: "You are a helpful assistant" },
});

const serviceId = String(service.service_id ?? "");
const ws = client.openChatWebSocket(serviceId);

ws.on("open", () => {
  ws.send(JSON.stringify({
    type: "input",
    contents: [{ kind: "text", text: "Hello, Claude!" }],
  }));
});

ws.on("message", (data) => {
  console.log(String(data));
});
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

HTTP/JSON API for managing the runner and service lifecycle:

| Endpoint | Purpose |
|----------|---------|
| `GET /v1/system/status` | Runner status and capabilities |
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

Note: one `service_id` allows only one active WebSocket connection; concurrent connections are rejected with `409 SERVICE_BUSY`.

Full protocol documentation: [`docs/ASMP.md`](docs/ASMP.md) | [`docs/ASP.md`](docs/ASP.md)

## Distribution

Package your app with the runner embedded:

```bash
# Build everything
./scripts/macos/build_binaries.sh

# Package your app into a DMG
./scripts/macos/package_dmg.sh /path/to/YourApp.app

# Output: dist/YourApp.dmg (runner included)
```

The packaged DMG contains everything needed — users don't need to install anything separately.

Note: macOS “Files and Folders” / “Full Disk Access” grants are tied to the app's code signature.
If you repackage with ad-hoc signing, the system may prompt again. To keep grants stable across updates,
sign with a real identity (set `NOUS_CODESIGN_IDENTITY` when running `./scripts/macos/package_dmg.sh`).

## Available Scripts

| Script | Purpose |
|--------|---------|
| `./scripts/macos/build_binaries.sh` | Build host/guest daemons, `limactl`, and Lima templates into `dist/` |
| `./scripts/macos/package_dmg.sh <app_path>` | Inject runner binaries into your `.app` and produce `dist/<AppName>.dmg` |
| `./scripts/macos/fetch_offline_assets.sh` | Pre-download and pre-bake VM/containerd assets for offline/slow-network installs |
| `./scripts/macos/demo_xcuitest.sh` | Run Demo UI automation (real model call path) |
| `./scripts/macos/make_dmg.sh` | Create DMG from `dist/NousAgentRunnerDemo.app` (demo helper) |

## Configuration

Configuration is file-based (zero CLI parameters):

```bash
# Priority: .env.local > .env.production > .env.development > .env.test
~/Library/Application Support/NousAgentRunner/<instance_id>/.env.local
```

Runner paths are per-instance (based on `<instance_id>`):

- Config + state (macOS): `~/Library/Application Support/NousAgentRunner/<instance_id>/`
  - Config: `.env.local`, `.env.production`, `.env.development`, `.env.test`
  - Auth token: `token` (0600)
  - Runner Context discovery: `runtime.json` (listen addr/port, pid, started_at)
- Logs (macOS): `~/Library/Logs/NousAgentRunner/<instance_id>/runnerd.log`
- Cache/temp (macOS): `~/Library/Caches/NousAgentRunner/`
  - Default temp dir (shared): `~/Library/Caches/NousAgentRunner/<instance_id>/SharedTmp/`
  - Lima home (shared across instances): `~/Library/Caches/NousAgentRunner/lima/`

You can query the exact paths via `GET /v1/system/paths`.

| Variable | Default | Description |
|----------|---------|-------------|
| `NOUS_AGENT_RUNNER_PORT` | Auto | HTTP API port |
| `NOUS_AGENT_RUNNER_VM_MEMORY_MB` | 4096 | VM memory allocation |
| `NOUS_AGENT_RUNNER_VM_CPU_CORES` | 4 | VM CPU cores |
| `NOUS_AGENT_RUNNER_REGISTRY_BASE` | `docker.io/gravtice/` | Approved image registry |

## Troubleshooting

- Build fails with missing `references/lima`: run `git submodule update --init --recursive`.
- First VM/service startup is slow: initial boot downloads VM/containerd assets. For deterministic installs, run `./scripts/macos/fetch_offline_assets.sh` before packaging.
- Repeated macOS file permission prompts after app updates: avoid ad-hoc signing for release builds; set `NOUS_CODESIGN_IDENTITY` when running `./scripts/macos/package_dmg.sh`.
- Need diagnostics/logs: check `~/Library/Logs/NousAgentRunner/<instance_id>/runnerd.log` and query `GET /v1/system/paths`.
- TypeScript SDK must run in Node/Electron main process, not browser renderer context.

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
├── sdk/typescript/nous-agent-runner-sdk/ # TypeScript SDK (Node/Electron)
├── services/claude-agent-service/ # Claude Agent Service (Python)
├── demo/macos/NousAgentRunnerDemo/ # Example SwiftUI app
├── docs/                         # Protocol specifications
└── scripts/macos/                # Build & packaging scripts
```

## Documentation

- [Implementation Plan (v0.2.4)](docs/v0.2.4/IMPLEMENTATION_PLAN.md) — Current staged development plan
- [Implementation Plan (v0.1.0/MVP)](docs/v0.1.0/IMPLEMENTATION_PLAN.md) — Initial architecture design
- [ASMP Protocol](docs/ASMP.md) — Control plane API reference
- [ASP Protocol](docs/ASP.md) — Data plane WebSocket reference
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

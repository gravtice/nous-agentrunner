# nous-agent-runner-sdk

TypeScript SDK for **Nous Agent Runner** (Node/Electron).

## Requirements

- Node.js `>=18`
- macOS 14+ (Runner runtime requirement)
- Runs in **Node/Electron main process** (not in the browser)

## Install

```bash
npm install nous-agent-runner-sdk
```

## Usage

```ts
import {
  NousAgentRunnerDaemon,
  NousAgentRunnerClient,
} from "nous-agent-runner-sdk";

const daemon = new NousAgentRunnerDaemon();
const runtime = await daemon.ensureRunning();

const client = new NousAgentRunnerClient(runtime);
const status = await client.getSystemStatus();
console.log(status);
```

### WebSocket chat

```ts
const service = await client.createClaudeService({
  imageRef: "docker.io/gravtice/nous-claude-agent-service:0.2.4",
  rwMounts: ["/Users/alice/Projects"],
  env: { ANTHROPIC_API_KEY: process.env.ANTHROPIC_API_KEY ?? "" },
  serviceConfig: { system_prompt: "You are a helpful assistant" },
});

const serviceId = String(service.service_id ?? "");
const ws = client.openChatWebSocket(serviceId);

ws.on("message", (data) => {
  const msg = JSON.parse(String(data));
  console.log(msg);
});

ws.on("open", () => {
  ws.send(JSON.stringify({ type: "input", contents: [{ kind: "text", text: "Hello" }] }));
});
```

## Runtime discovery (zero-parameter)

The SDK discovers the running daemon via:

- `~/Library/Application Support/NousAgentRunner/<instance_id>/runtime.json` (preferred)
- `~/Library/Application Support/NousAgentRunner/<instance_id>/.env.local` (fallback, then `.env.production/.env.development/.env.test`)
- `~/Library/Application Support/NousAgentRunner/<instance_id>/token` (Bearer token)

`instance_id` discovery matches the embedded app behavior (Swift/runnerd):

1. `NousAgentRunnerConfig.json` if bundled
2. Otherwise derive from `CFBundleIdentifier` (`sha256` hex prefix 12)
3. Fallback to `"default"`

## License

Apache-2.0 (see `LICENSE`).


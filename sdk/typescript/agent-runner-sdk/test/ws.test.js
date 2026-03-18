const test = require("node:test");
const assert = require("node:assert/strict");
const http = require("node:http");
const { WebSocketServer } = require("ws");

const { AgentRunnerClient, AgentRunnerContext } = require("../dist");

function listen(server) {
  return new Promise((resolve, reject) => {
    server.listen(0, "127.0.0.1", () => {
      const addr = server.address();
      if (!addr || typeof addr === "string") return reject(new Error("no addr"));
      resolve(addr.port);
    });
  });
}

function withTimeout(promise, ms) {
  return Promise.race([
    promise,
    new Promise((_, reject) =>
      setTimeout(() => reject(new Error("timeout")), ms),
    ),
  ]);
}

test("openChatWebSocket sends Authorization header", async () => {
  const server = http.createServer();
  const wss = new WebSocketServer({ server });

  const seen = new Promise((resolve, reject) => {
    wss.on("connection", (ws, req) => {
      try {
        assert.equal(req.url, "/v1/services/svc_123/chat");
        assert.equal(req.headers.authorization, "Bearer tok");
        ws.close();
        resolve();
      } catch (e) {
        reject(e);
      }
    });
  });

  const port = await listen(server);
  try {
    const runnerContext = new AgentRunnerContext({
      baseURL: new URL(`http://127.0.0.1:${port}`),
      token: "tok",
      instanceId: "x",
    });
    const client = new AgentRunnerClient(runnerContext);
    const ws = client.openChatWebSocket("svc_123");

    await withTimeout(seen, 2_000);
    await withTimeout(
      new Promise((resolve) => ws.once("close", resolve)),
      2_000,
    );
  } finally {
    wss.close();
    await new Promise((r) => server.close(r));
  }
});

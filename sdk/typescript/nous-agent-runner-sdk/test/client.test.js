const test = require("node:test");
const assert = require("node:assert/strict");
const http = require("node:http");

const { NousAgentRunnerClient, NousAgentRunnerError, NousAgentRunnerContext } =
  require("../dist");

function listen(server) {
  return new Promise((resolve, reject) => {
    server.listen(0, "127.0.0.1", () => {
      const addr = server.address();
      if (!addr || typeof addr === "string") return reject(new Error("no addr"));
      resolve(addr.port);
    });
  });
}

test("NousAgentRunnerClient injects Authorization header", async () => {
  const server = http.createServer((req, res) => {
    if (req.url !== "/v1/system/status") {
      res.writeHead(404);
      res.end("not found");
      return;
    }
    assert.equal(req.headers.authorization, "Bearer tok");
    res.writeHead(200, { "Content-Type": "application/json" });
    res.end(JSON.stringify({ ok: true }));
  });

  const port = await listen(server);
  try {
    const runnerContext = new NousAgentRunnerContext({
      baseURL: new URL(`http://127.0.0.1:${port}`),
      token: "tok",
      instanceId: "x",
    });
    const client = new NousAgentRunnerClient(runnerContext);
    const out = await client.getSystemStatus();
    assert.equal(out.ok, true);
  } finally {
    await new Promise((r) => server.close(r));
  }
});

test("NousAgentRunnerClient surfaces non-200 as http error", async () => {
  const server = http.createServer((req, res) => {
    res.writeHead(500, { "Content-Type": "text/plain" });
    res.end("oops");
  });

  const port = await listen(server);
  try {
    const runnerContext = new NousAgentRunnerContext({
      baseURL: new URL(`http://127.0.0.1:${port}`),
      token: "tok",
      instanceId: "x",
    });
    const client = new NousAgentRunnerClient(runnerContext);

    await assert.rejects(
      () => client.getSystemStatus(),
      (err) => {
        assert.ok(err instanceof NousAgentRunnerError);
        assert.equal(err.code, "http");
        assert.equal(err.status, 500);
        assert.equal(err.body, "oops");
        return true;
      },
    );
  } finally {
    await new Promise((r) => server.close(r));
  }
});

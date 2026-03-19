const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");

const {
  AgentRunnerContext,
  deriveInstanceIdFromBundleId,
  isSafeInstanceId,
  parseEnv,
  resolveAppSupportDir,
} = require("../dist");

test("deriveInstanceIdFromBundleId: sha256 prefix 12", () => {
  assert.equal(deriveInstanceIdFromBundleId("com.example.app"), "8a464e05bf03");
});

test("isSafeInstanceId: rejects unsafe characters", () => {
  assert.equal(isSafeInstanceId("abc-_.XYZ012"), true);
  assert.equal(isSafeInstanceId("a/b"), false);
  assert.equal(isSafeInstanceId(""), false);
});

test("parseEnv: strips quotes, ignores comments", () => {
  const env = parseEnv(`
# comment
AGENT_RUNNER_PORT=1234
FOO="bar"
BAR='baz'
`);
  assert.equal(env.AGENT_RUNNER_PORT, "1234");
  assert.equal(env.FOO, "bar");
  assert.equal(env.BAR, "baz");
});

test("resolveAppSupportDir uses HOME", () => {
  const prevHome = process.env.HOME;
  process.env.HOME = "/tmp/home";
  try {
    assert.equal(
      resolveAppSupportDir("inst"),
      "/tmp/home/.agentrunner/inst",
    );
  } finally {
    process.env.HOME = prevHome;
  }
});

test("AgentRunnerContext.discover prefers runtime.json", async () => {
  const tmpHome = await fs.mkdtemp(path.join(os.tmpdir(), "agent-runner-ts-sdk-"));
  const prevHome = process.env.HOME;
  process.env.HOME = tmpHome;
  try {
    const appSupportDir = resolveAppSupportDir("testinstance");
    await fs.mkdir(appSupportDir, { recursive: true });
    await fs.writeFile(
      path.join(appSupportDir, "runtime.json"),
      JSON.stringify({ listen_port: 1234 }) + "\n",
      "utf8",
    );
    await fs.writeFile(
      path.join(appSupportDir, ".env.local"),
      "AGENT_RUNNER_PORT=9999\n",
      "utf8",
    );
    await fs.writeFile(path.join(appSupportDir, "token"), "tok\n", "utf8");

    const rt = await AgentRunnerContext.discover({ instanceId: "testinstance" });
    assert.equal(rt.baseURL.toString(), "http://127.0.0.1:1234/");
    assert.equal(rt.token, "tok");
    assert.equal(rt.instanceId, "testinstance");
  } finally {
    process.env.HOME = prevHome;
    await fs.rm(tmpHome, { recursive: true, force: true });
  }
});

test("AgentRunnerContext.discover uses .env.local over .env.production", async () => {
  const tmpHome = await fs.mkdtemp(path.join(os.tmpdir(), "agent-runner-ts-sdk-"));
  const prevHome = process.env.HOME;
  process.env.HOME = tmpHome;
  try {
    const appSupportDir = resolveAppSupportDir("testinstance");
    await fs.mkdir(appSupportDir, { recursive: true });
    await fs.writeFile(
      path.join(appSupportDir, ".env.production"),
      "AGENT_RUNNER_PORT=1111\n",
      "utf8",
    );
    await fs.writeFile(
      path.join(appSupportDir, ".env.local"),
      "AGENT_RUNNER_PORT=2222\n",
      "utf8",
    );
    await fs.writeFile(path.join(appSupportDir, "token"), "tok\n", "utf8");

    const rt = await AgentRunnerContext.discover({ instanceId: "testinstance" });
    assert.equal(rt.baseURL.toString(), "http://127.0.0.1:2222/");
  } finally {
    process.env.HOME = prevHome;
    await fs.rm(tmpHome, { recursive: true, force: true });
  }
});

test("AgentRunnerContext.discover errors when token missing", async () => {
  const tmpHome = await fs.mkdtemp(path.join(os.tmpdir(), "agent-runner-ts-sdk-"));
  const prevHome = process.env.HOME;
  process.env.HOME = tmpHome;
  try {
    const appSupportDir = resolveAppSupportDir("testinstance");
    await fs.mkdir(appSupportDir, { recursive: true });
    await fs.writeFile(
      path.join(appSupportDir, ".env.local"),
      "AGENT_RUNNER_PORT=2222\n",
      "utf8",
    );

    await assert.rejects(
      () => AgentRunnerContext.discover({ instanceId: "testinstance" }),
      (err) => {
        assert.equal(err?.code, "missingConfig");
        return true;
      },
    );
  } finally {
    process.env.HOME = prevHome;
    await fs.rm(tmpHome, { recursive: true, force: true });
  }
});

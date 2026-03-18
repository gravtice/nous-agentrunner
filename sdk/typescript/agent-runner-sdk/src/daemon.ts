import { spawn, type ChildProcess } from "node:child_process";
import { access, constants, stat } from "node:fs/promises";
import path from "node:path";

import { AgentRunnerClient } from "./client";
import { AgentRunnerError } from "./errors";
import { AgentRunnerContext, discoverInstanceId } from "./context";

const RUNNERD_EXECUTABLE_NAMES = ["agent-runnerd"];

export class AgentRunnerDaemon {
  private instanceId?: string;
  private readonly runnerdPath?: string;
  private process?: ChildProcess;

  constructor(opts: { instanceId?: string; runnerdPath?: string } = {}) {
    this.instanceId = opts.instanceId;
    this.runnerdPath = opts.runnerdPath;
  }

  async ensureRunning(opts: { timeoutMs?: number; runnerdPath?: string } = {}) {
    const timeoutMs = opts.timeoutMs ?? 15_000;
    if (timeoutMs <= 0) {
      throw new AgentRunnerError("invalidConfig", "timeoutMs must be > 0");
    }

    const existing = await this.tryProbe();
    if (existing) return existing;

    if (!this.process) {
      const runnerdPath =
        opts.runnerdPath ?? this.runnerdPath ?? (await locateBundledExecutable("agent-runnerd"));

      const instanceId = await this.getInstanceId();
      const child = spawn(runnerdPath, [], {
        stdio: "ignore",
        env: {
          ...process.env,
          AGENT_RUNNER_INSTANCE_ID: instanceId,
        },
      });
      this.process = child;
    }

    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      if (this.process?.exitCode !== null && this.process?.exitCode !== undefined) {
        throw new AgentRunnerError(
          "io",
          `agent-runnerd exited (code=${this.process.exitCode})`,
        );
      }

      const runnerContext = await this.tryProbe();
      if (runnerContext) return runnerContext;
      await sleep(200);
    }

    throw new AgentRunnerError("timeout", "timeout waiting for agent-runnerd");
  }

  stop() {
    if (!this.process) return;
    this.process.kill();
    this.process = undefined;
  }

  private async tryProbe(): Promise<AgentRunnerContext | null> {
    try {
      const runnerContext = await AgentRunnerContext.discover({
        instanceId: await this.getInstanceId(),
      });
      const client = new AgentRunnerClient(runnerContext);
      await client.getSystemStatus();
      return runnerContext;
    } catch {
      return null;
    }
  }

  private async getInstanceId(): Promise<string> {
    if (this.instanceId) return this.instanceId;
    this.instanceId = await discoverInstanceId();
    return this.instanceId;
  }
}

async function locateBundledExecutable(name: string): Promise<string> {
  const candidates: string[] = [];
  const names = name === "agent-runnerd" ? RUNNERD_EXECUTABLE_NAMES : [name];

  const resourcesPath = (process as unknown as { resourcesPath?: unknown }).resourcesPath;
  if (typeof resourcesPath === "string" && resourcesPath.length > 0) {
    for (const candidateName of names) {
      candidates.push(path.join(resourcesPath, candidateName));
    }
  }

  const exe = process.execPath;
  for (const candidateName of names) {
    candidates.push(path.join(path.dirname(exe), candidateName));
    candidates.push(path.resolve(path.dirname(exe), "..", "Resources", candidateName));
    candidates.push(path.resolve(path.dirname(exe), "..", "MacOS", candidateName));
  }

  for (const p of candidates) {
    if (await isExecutableFile(p)) return p;
  }
  throw new AgentRunnerError("missingConfig", `missing bundled executable: ${name}`);
}

async function isExecutableFile(filePath: string): Promise<boolean> {
  try {
    const st = await stat(filePath);
    if (!st.isFile()) return false;
    await access(filePath, constants.X_OK);
    return true;
  } catch {
    return false;
  }
}

function sleep(ms: number) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

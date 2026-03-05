import { spawn, type ChildProcess } from "node:child_process";
import { access, constants, stat } from "node:fs/promises";
import path from "node:path";

import { NousAgentRunnerClient } from "./client";
import { NousAgentRunnerError } from "./errors";
import { NousAgentRunnerContext, discoverInstanceId } from "./context";

export class NousAgentRunnerDaemon {
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
      throw new NousAgentRunnerError("invalidConfig", "timeoutMs must be > 0");
    }

    const existing = await this.tryProbe();
    if (existing) return existing;

    if (!this.process) {
      const runnerdPath =
        opts.runnerdPath ?? this.runnerdPath ?? (await locateBundledExecutable("nous-agent-runnerd"));

      const child = spawn(runnerdPath, [], { stdio: "ignore" });
      this.process = child;
    }

    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      if (this.process?.exitCode !== null && this.process?.exitCode !== undefined) {
        throw new NousAgentRunnerError(
          "io",
          `nous-agent-runnerd exited (code=${this.process.exitCode})`,
        );
      }

      const runnerContext = await this.tryProbe();
      if (runnerContext) return runnerContext;
      await sleep(200);
    }

    throw new NousAgentRunnerError("timeout", "timeout waiting for nous-agent-runnerd");
  }

  stop() {
    if (!this.process) return;
    this.process.kill();
    this.process = undefined;
  }

  private async tryProbe(): Promise<NousAgentRunnerContext | null> {
    try {
      const runnerContext = await NousAgentRunnerContext.discover({
        instanceId: await this.getInstanceId(),
      });
      const client = new NousAgentRunnerClient(runnerContext);
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

  const resourcesPath = (process as unknown as { resourcesPath?: unknown }).resourcesPath;
  if (typeof resourcesPath === "string" && resourcesPath.length > 0) {
    candidates.push(path.join(resourcesPath, name));
  }

  const exe = process.execPath;
  candidates.push(path.join(path.dirname(exe), name));
  candidates.push(path.resolve(path.dirname(exe), "..", "Resources", name));
  candidates.push(path.resolve(path.dirname(exe), "..", "MacOS", name));

  for (const p of candidates) {
    if (await isExecutableFile(p)) return p;
  }
  throw new NousAgentRunnerError("missingConfig", `missing bundled executable: ${name}`);
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

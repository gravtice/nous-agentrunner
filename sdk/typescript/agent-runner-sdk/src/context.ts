import { createHash } from "node:crypto";
import { execFile } from "node:child_process";
import { readFile, stat } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { promisify } from "node:util";

import { AgentRunnerError } from "./errors";

const execFileAsync = promisify(execFile);
const APP_SUPPORT_DIRNAME = "AgentRunner";
const CONFIG_BASENAMES = ["AgentRunnerConfig.json"];
const PORT_ENV_KEYS = ["AGENT_RUNNER_PORT"];

export type AgentRunnerContextParams = {
  baseURL: URL;
  token: string;
  instanceId: string;
};

export class AgentRunnerContext {
  readonly baseURL: URL;
  readonly token: string;
  readonly instanceId: string;

  constructor(params: AgentRunnerContextParams) {
    this.baseURL = params.baseURL;
    this.token = params.token;
    this.instanceId = params.instanceId;
  }

  static async discover(opts: { instanceId?: string } = {}) {
    const instanceId = opts.instanceId ?? (await discoverInstanceId());
    if (!isSafeInstanceId(instanceId)) {
      throw new AgentRunnerError("invalidConfig", "invalid instance id");
    }

    const appSupportDir = resolveAppSupportDir(instanceId);
    const port = await loadPort(appSupportDir);
    const token = await loadToken(appSupportDir);
    const baseURL = new URL(`http://127.0.0.1:${port}`);
    return new AgentRunnerContext({ baseURL, token, instanceId });
  }
}

export function deriveInstanceIdFromBundleId(bundleId: string): string {
  const normalized = bundleId.trim().toLowerCase();
  if (normalized.length === 0) {
    throw new AgentRunnerError("invalidConfig", "bundle id is empty");
  }
  const hex = createHash("sha256").update(normalized, "utf8").digest("hex");
  return hex.slice(0, 12);
}

export function isSafeInstanceId(s: string): boolean {
  if (s.length === 0) return false;
  for (const ch of s) {
    const code = ch.charCodeAt(0);
    if (code >= 0x30 && code <= 0x39) continue; // 0-9
    if (code >= 0x41 && code <= 0x5a) continue; // A-Z
    if (code >= 0x61 && code <= 0x7a) continue; // a-z
    if (code === 0x2d || code === 0x2e || code === 0x5f) continue; // - . _
    return false;
  }
  return true;
}

export function isSafeSkillDirName(s: string): boolean {
  return isSafeInstanceId(s);
}

export function parseEnv(content: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const rawLine of content.split("\n")) {
    const trimmed = rawLine.trim();
    if (trimmed.length === 0 || trimmed.startsWith("#")) continue;
    const idx = trimmed.indexOf("=");
    if (idx <= 0) continue;
    const key = trimmed.slice(0, idx).trim();
    let value = trimmed.slice(idx + 1).trim();
    value = value.replace(/^["']+/, "").replace(/["']+$/, "");
    out[key] = value;
  }
  return out;
}

export function resolveAppSupportDir(instanceId: string): string {
  const home = resolveHomeDir();
  return path.join(
    home,
    "Library",
    "Application Support",
    APP_SUPPORT_DIRNAME,
    instanceId,
  );
}

export async function discoverInstanceId(): Promise<string> {
  const exe = process.execPath;

  const configCandidates = CONFIG_BASENAMES.flatMap((name) => [
    path.join(path.dirname(exe), name),
    path.resolve(path.dirname(exe), "..", "Resources", name),
  ]);
  for (const candidate of configCandidates) {
    const instanceId = await loadInstanceIdFromConfigJSON(candidate);
    if (instanceId) return instanceId;
  }

  if (process.platform === "darwin") {
    const infoPlist = await findInfoPlistNearExecutable(exe);
    if (infoPlist) {
      const bundleId = await loadBundleIdFromInfoPlist(infoPlist);
      if (bundleId) {
        return deriveInstanceIdFromBundleId(bundleId);
      }
    }
  }

  return "default";
}

async function loadInstanceIdFromConfigJSON(filePath: string): Promise<string> {
  let raw: string;
  try {
    raw = await readFile(filePath, "utf8");
  } catch {
    return "";
  }

  let obj: unknown;
  try {
    obj = JSON.parse(raw);
  } catch {
    return "";
  }
  if (!obj || typeof obj !== "object") return "";
  const instanceId = (obj as { instance_id?: unknown }).instance_id;
  if (typeof instanceId !== "string") return "";
  const trimmed = instanceId.trim();
  if (!isSafeInstanceId(trimmed)) return "";
  return trimmed;
}

async function findInfoPlistNearExecutable(exe: string): Promise<string> {
  let dir = path.dirname(exe);
  for (let i = 0; i < 10; i++) {
    if (path.basename(dir) === "Contents") {
      const candidate = path.join(dir, "Info.plist");
      if (await isRegularFile(candidate)) return candidate;
    }
    if (dir.toLowerCase().endsWith(".app")) {
      const candidate = path.join(dir, "Contents", "Info.plist");
      if (await isRegularFile(candidate)) return candidate;
    }

    const parent = path.dirname(dir);
    if (parent === dir) break;
    dir = parent;
  }
  return "";
}

async function loadBundleIdFromInfoPlist(infoPlistPath: string): Promise<string> {
  const bundleIdFromPlutil = await tryReadBundleIdWithPlutil(infoPlistPath);
  if (bundleIdFromPlutil) return bundleIdFromPlutil;

  let raw: string;
  try {
    raw = await readFile(infoPlistPath, "utf8");
  } catch {
    return "";
  }
  const m = raw.match(
    /<key>\s*CFBundleIdentifier\s*<\/key>\s*<string>\s*([^<]+)\s*<\/string>/,
  );
  return m?.[1]?.trim() ?? "";
}

async function tryReadBundleIdWithPlutil(infoPlistPath: string): Promise<string> {
  const plutil = "/usr/bin/plutil";
  if (process.platform !== "darwin") return "";

  try {
    const { stdout } = await execFileAsync(
      plutil,
      ["-convert", "json", "-o", "-", infoPlistPath],
      { encoding: "utf8" },
    );
    const obj = JSON.parse(stdout) as { CFBundleIdentifier?: unknown };
    const bundleId = obj?.CFBundleIdentifier;
    return typeof bundleId === "string" ? bundleId.trim() : "";
  } catch {
    return "";
  }
}

function resolveHomeDir(): string {
  const envHome = process.env.HOME?.trim();
  if (envHome) return envHome;

  const home = os.homedir();
  if (!home) {
    throw new AgentRunnerError("io", "missing home directory");
  }
  return home;
}

async function loadPort(appSupportDir: string): Promise<number> {
  const runtimePort = await loadPortFromRuntimeJSON(appSupportDir);
  if (runtimePort) return runtimePort;

  const candidates = [
    ".env.local",
    ".env.production",
    ".env.development",
    ".env.test",
  ];
  for (const name of candidates) {
    const envPath = path.join(appSupportDir, name);
    let contents: string;
    try {
      contents = await readFile(envPath, "utf8");
    } catch {
      continue;
    }
    const portStr = readPreferredEnv(parseEnv(contents), PORT_ENV_KEYS);
    if (!portStr) continue;
    const n = Number.parseInt(portStr, 10);
    if (Number.isFinite(n) && n > 0) {
      return n;
    }
  }

  throw new AgentRunnerError(
    "missingConfig",
    "AGENT_RUNNER_PORT not found",
  );
}

async function loadPortFromRuntimeJSON(appSupportDir: string): Promise<number> {
  const runtimePath = path.join(appSupportDir, "runtime.json");
  let raw: string;
  try {
    raw = await readFile(runtimePath, "utf8");
  } catch {
    return 0;
  }

  let obj: unknown;
  try {
    obj = JSON.parse(raw);
  } catch {
    return 0;
  }
  if (!obj || typeof obj !== "object") return 0;

  const port = (obj as { listen_port?: unknown }).listen_port;
  return typeof port === "number" && Number.isFinite(port) && port > 0 ? port : 0;
}

async function loadToken(appSupportDir: string): Promise<string> {
  const tokenPath = path.join(appSupportDir, "token");
  let token: string;
  try {
    token = (await readFile(tokenPath, "utf8")).trim();
  } catch {
    token = "";
  }
  if (token.length === 0) {
    throw new AgentRunnerError("missingConfig", "token file not found");
  }
  return token;
}

function readPreferredEnv(
  env: Record<string, string>,
  keys: readonly string[],
): string {
  for (const key of keys) {
    const value = env[key]?.trim();
    if (value) {
      return value;
    }
  }
  return "";
}

async function isRegularFile(filePath: string): Promise<boolean> {
  try {
    const st = await stat(filePath);
    return st.isFile();
  } catch {
    return false;
  }
}

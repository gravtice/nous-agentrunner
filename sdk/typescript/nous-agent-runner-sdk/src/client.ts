import { NousAgentRunnerError } from "./errors";
import { isSafeSkillDirName, NousAgentRunnerContext } from "./context";
import { openChatWebSocket } from "./ws";

export type FetchLike = (
  input: string | URL,
  init?: RequestInit,
) => Promise<Response>;

export class NousAgentRunnerClient {
  private readonly runnerContext: NousAgentRunnerContext;
  private readonly fetchFn: FetchLike;

  constructor(
    runnerContext: NousAgentRunnerContext,
    opts: { fetch?: FetchLike } = {},
  ) {
    this.runnerContext = runnerContext;
    const f = opts.fetch ?? (globalThis.fetch as unknown as FetchLike | undefined);
    if (!f) {
      throw new NousAgentRunnerError("invalidConfig", "fetch is not available");
    }
    this.fetchFn = f;
  }

  getSystemStatus() {
    return this.requestJSON("GET", "/v1/system/status", undefined, 30_000);
  }

  diagnoseGuestToHostTunnel() {
    return this.requestJSON(
      "POST",
      "/v1/system/diagnostics/guest_to_host_tunnel",
      undefined,
      600_000,
    );
  }

  getSystemPaths() {
    return this.requestJSON("GET", "/v1/system/paths", undefined, 30_000);
  }

  listShares() {
    return this.requestJSON("GET", "/v1/shares", undefined, 30_000);
  }

  addShare(hostPath: string) {
    return this.requestJSON(
      "POST",
      "/v1/shares",
      { host_path: hostPath },
      60_000,
    );
  }

  setShareExcludes(excludes: string[]) {
    return this.requestJSON(
      "PUT",
      "/v1/shares/excludes",
      { excludes },
      60_000,
    );
  }

  listSkills() {
    return this.requestJSON("GET", "/v1/skills", undefined, 30_000);
  }

  discoverSkills(source: string, opts: { ref?: string; subpath?: string } = {}) {
    const trimmed = source.trim();
    if (trimmed.length === 0) {
      throw new NousAgentRunnerError("invalidConfig", "source is required");
    }
    const body: Record<string, unknown> = { source: trimmed };
    if (opts.ref && opts.ref.trim().length > 0) body.ref = opts.ref;
    if (opts.subpath && opts.subpath.trim().length > 0) body.subpath = opts.subpath;
    return this.requestJSON("POST", "/v1/skills/discover", body, 1_800_000);
  }

  installSkills(
    source: string,
    opts: {
      ref?: string;
      subpath?: string;
      skills?: string[];
      replace?: boolean;
    } = {},
  ) {
    const trimmed = source.trim();
    if (trimmed.length === 0) {
      throw new NousAgentRunnerError("invalidConfig", "source is required");
    }
    const body: Record<string, unknown> = { source: trimmed };
    if (opts.ref && opts.ref.trim().length > 0) body.ref = opts.ref;
    if (opts.subpath && opts.subpath.trim().length > 0) body.subpath = opts.subpath;
    if (opts.skills && opts.skills.length > 0) body.skills = opts.skills;
    if (opts.replace) body.replace = true;
    return this.requestJSON("POST", "/v1/skills/install", body, 1_800_000);
  }

  deleteSkill(name: string) {
    const trimmed = name.trim();
    if (trimmed.length === 0 || !isSafeSkillDirName(trimmed)) {
      throw new NousAgentRunnerError("invalidConfig", "invalid skill name");
    }
    return this.requestJSON("DELETE", `/v1/skills/${trimmed}`, undefined, 60_000);
  }

  pullImage(ref: string) {
    return this.requestJSON("POST", "/v1/images/pull", { ref }, 1_800_000);
  }

  pruneImages(opts: { all?: boolean } = {}) {
    const body = opts.all === undefined ? undefined : { all: opts.all };
    return this.requestJSON("POST", "/v1/images/prune", body, 1_800_000);
  }

  restartVM() {
    return this.requestJSON("POST", "/v1/system/vm/restart", undefined, 1_800_000);
  }

  createClaudeService(params: {
    imageRef: string;
    rwMounts: string[];
    env?: Record<string, string>;
    idleTimeoutSeconds?: number;
    serviceConfig: Record<string, unknown>;
  }) {
    const idleTimeoutSeconds = params.idleTimeoutSeconds ?? 0;
    if (idleTimeoutSeconds < 0) {
      throw new NousAgentRunnerError(
        "invalidConfig",
        "idle_timeout_seconds must be >= 0",
      );
    }
    const body = {
      type: "claude",
      image_ref: params.imageRef,
      resources: { cpu_cores: 2, memory_mb: 1024, pids: 256 },
      rw_mounts: params.rwMounts,
      env: params.env ?? {},
      idle_timeout_seconds: idleTimeoutSeconds,
      service_config: params.serviceConfig,
    };
    return this.requestJSON("POST", "/v1/services", body, 1_800_000);
  }

  listServices() {
    return this.requestJSON("GET", "/v1/services", undefined, 30_000);
  }

  getService(serviceId: string) {
    return this.requestJSON("GET", `/v1/services/${serviceId}`, undefined, 30_000);
  }

  getBuiltinTools(serviceType: string) {
    return this.requestJSON(
      "GET",
      `/v1/services/types/${serviceType}/builtin_tools`,
      undefined,
      30_000,
    );
  }

  deleteService(serviceId: string) {
    return this.requestJSON("DELETE", `/v1/services/${serviceId}`, undefined, 300_000);
  }

  stopService(serviceId: string) {
    return this.requestJSON("POST", `/v1/services/${serviceId}/stop`, undefined, 300_000);
  }

  startService(serviceId: string) {
    return this.requestJSON("POST", `/v1/services/${serviceId}/start`, undefined, 300_000);
  }

  resumeService(serviceId: string) {
    return this.requestJSON("POST", `/v1/services/${serviceId}/resume`, undefined, 1_800_000);
  }

  createTunnel(hostPort: number, guestPort?: number) {
    const body: Record<string, unknown> = { host_port: hostPort };
    if (guestPort !== undefined) body.guest_port = guestPort;
    return this.requestJSON("POST", "/v1/tunnels", body, 60_000);
  }

  listTunnels() {
    return this.requestJSON("GET", "/v1/tunnels", undefined, 30_000);
  }

  getTunnelByHostPort(hostPort: number) {
    return this.requestJSON(
      "GET",
      `/v1/tunnels/by_host_port/${hostPort}`,
      undefined,
      30_000,
    );
  }

  deleteTunnel(tunnelId: string) {
    return this.requestJSON("DELETE", `/v1/tunnels/${tunnelId}`, undefined, 60_000);
  }

  deleteTunnelByHostPort(hostPort: number) {
    return this.requestJSON(
      "DELETE",
      `/v1/tunnels/by_host_port/${hostPort}`,
      undefined,
      60_000,
    );
  }

  openChatWebSocket(serviceId: string) {
    return openChatWebSocket(this.runnerContext, serviceId);
  }

  private async requestJSON(
    method: string,
    path: string,
    body: unknown,
    timeoutMs: number,
  ): Promise<Record<string, unknown>> {
    const url = new URL(path, this.runnerContext.baseURL);

    const controller = new AbortController();
    const t = setTimeout(() => controller.abort(), timeoutMs);
    try {
      const headers: Record<string, string> = {
        Authorization: `Bearer ${this.runnerContext.token}`,
      };

      let reqBody: string | undefined;
      if (body !== undefined) {
        headers["Content-Type"] = "application/json";
        reqBody = JSON.stringify(body);
      }

      const resp = await this.fetchFn(url, {
        method,
        headers,
        body: reqBody,
        signal: controller.signal,
      } as RequestInit);

      const text = await resp.text();
      if (resp.status !== 200) {
        throw new NousAgentRunnerError("http", `http ${resp.status}`, {
          status: resp.status,
          body: text,
        });
      }

      if (!text) return {};
      const obj = JSON.parse(text) as unknown;
      return obj && typeof obj === "object" ? (obj as Record<string, unknown>) : {};
    } catch (err: unknown) {
      if (err instanceof NousAgentRunnerError) {
        throw err;
      }
      if (err && typeof err === "object" && (err as { name?: unknown }).name === "AbortError") {
        throw new NousAgentRunnerError("timeout", "request timeout");
      }
      throw new NousAgentRunnerError(
        "io",
        err instanceof Error ? err.message : "request failed",
      );
    } finally {
      clearTimeout(t);
    }
  }
}

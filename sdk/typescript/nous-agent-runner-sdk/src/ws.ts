import WebSocket from "ws";

import { NousAgentRunnerError } from "./errors";
import { NousAgentRunnerRuntime } from "./runtime";

export function buildChatWebSocketURL(
  baseURL: URL,
  serviceId: string,
): URL {
  if (!serviceId || serviceId.trim().length === 0) {
    throw new NousAgentRunnerError("invalidConfig", "serviceId is required");
  }
  const url = new URL(baseURL.toString());
  url.protocol = baseURL.protocol === "https:" ? "wss:" : "ws:";
  url.pathname = `/v1/services/${serviceId}/chat`;
  url.search = "";
  url.hash = "";
  return url;
}

export function openChatWebSocket(
  runtime: NousAgentRunnerRuntime,
  serviceId: string,
): WebSocket {
  const url = buildChatWebSocketURL(runtime.baseURL, serviceId);
  return new WebSocket(url.toString(), {
    headers: { Authorization: `Bearer ${runtime.token}` },
  });
}

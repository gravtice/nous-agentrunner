import WebSocket from "ws";

import { AgentRunnerError } from "./errors";
import { AgentRunnerContext } from "./context";

export function buildChatWebSocketURL(
  baseURL: URL,
  serviceId: string,
): URL {
  if (!serviceId || serviceId.trim().length === 0) {
    throw new AgentRunnerError("invalidConfig", "serviceId is required");
  }
  const url = new URL(baseURL.toString());
  url.protocol = baseURL.protocol === "https:" ? "wss:" : "ws:";
  url.pathname = `/v1/services/${serviceId}/chat`;
  url.search = "";
  url.hash = "";
  return url;
}

export function openChatWebSocket(
  runnerContext: AgentRunnerContext,
  serviceId: string,
): WebSocket {
  const url = buildChatWebSocketURL(runnerContext.baseURL, serviceId);
  return new WebSocket(url.toString(), {
    headers: { Authorization: `Bearer ${runnerContext.token}` },
  });
}

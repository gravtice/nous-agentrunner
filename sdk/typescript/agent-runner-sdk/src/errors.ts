export type AgentRunnerErrorCode =
  | "missingConfig"
  | "invalidConfig"
  | "io"
  | "http"
  | "timeout";

export class AgentRunnerError extends Error {
  readonly code: AgentRunnerErrorCode;
  readonly status?: number;
  readonly body?: string;

  constructor(
    code: AgentRunnerErrorCode,
    message: string,
    opts: { status?: number; body?: string } = {},
  ) {
    super(message);
    this.name = "AgentRunnerError";
    this.code = code;
    this.status = opts.status;
    this.body = opts.body;
  }
}

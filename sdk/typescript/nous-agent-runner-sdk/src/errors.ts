export type NousAgentRunnerErrorCode =
  | "missingConfig"
  | "invalidConfig"
  | "io"
  | "http"
  | "timeout";

export class NousAgentRunnerError extends Error {
  readonly code: NousAgentRunnerErrorCode;
  readonly status?: number;
  readonly body?: string;

  constructor(
    code: NousAgentRunnerErrorCode,
    message: string,
    opts: { status?: number; body?: string } = {},
  ) {
    super(message);
    this.name = "NousAgentRunnerError";
    this.code = code;
    this.status = opts.status;
    this.body = opts.body;
  }
}

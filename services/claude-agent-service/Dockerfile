FROM node:20-slim AS claude-code

ARG TARGETOS
ARG TARGETARCH

RUN npm install -g @anthropic-ai/claude-code \
    && npm cache clean --force \
    && if [ -n "${TARGETOS:-}" ] && [ -n "${TARGETARCH:-}" ]; then \
        case "${TARGETOS}/${TARGETARCH}" in \
          linux/arm64) keep_dir="arm64-linux" ;; \
          linux/amd64) keep_dir="x64-linux" ;; \
          *) keep_dir="" ;; \
        esac; \
        if [ -n "${keep_dir}" ]; then \
          rg_dir="/usr/local/lib/node_modules/@anthropic-ai/claude-code/vendor/ripgrep"; \
          find "${rg_dir}" -mindepth 1 -maxdepth 1 -type d ! -name "${keep_dir}" -exec rm -rf {} +; \
        fi; \
      fi

FROM python:3.11-slim

WORKDIR /app

# Claude Agent SDK relies on Claude Code CLI ("claude") via SubprocessCLITransport.
COPY --from=claude-code /usr/local/bin/node /usr/local/bin/node
COPY --from=claude-code /usr/local/lib/node_modules/@anthropic-ai /usr/local/lib/node_modules/@anthropic-ai

RUN printf '%s\n' \
    '#!/bin/sh' \
    'exec node /usr/local/lib/node_modules/@anthropic-ai/claude-code/cli.js "$@"' \
    > /usr/local/bin/claude \
    && chmod +x /usr/local/bin/claude

RUN pip install --no-cache-dir uv \
    && rm -rf /root/.cache/pip

# Install Claude Agent SDK from the vendored reference repo.
COPY references/claude-agent-sdk-python /opt/claude-agent-sdk-python
RUN test -f /opt/claude-agent-sdk-python/pyproject.toml || (echo >&2 "missing /opt/claude-agent-sdk-python/pyproject.toml (did you run: git submodule update --init --recursive ?)"; exit 1)
RUN pip install --no-cache-dir -e /opt/claude-agent-sdk-python \
    && rm -rf /root/.cache/pip

COPY services/claude-agent-service /app
RUN pip install --no-cache-dir . \
    && rm -rf /root/.cache/pip

ENV NOUS_RUNNER_SERVICE_PORT=8000
EXPOSE 8000

CMD ["claude-agent-service"]

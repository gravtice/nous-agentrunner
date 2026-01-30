FROM python:3.11-slim

WORKDIR /app

RUN apt-get update \
    && apt-get install -y --no-install-recommends nodejs npm ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Claude Agent SDK relies on Claude Code CLI ("claude") via SubprocessCLITransport.
RUN npm install -g @anthropic-ai/claude-code \
    && npm cache clean --force

# Install Claude Agent SDK from the vendored reference repo.
COPY references/claude-agent-sdk-python /opt/claude-agent-sdk-python
RUN test -f /opt/claude-agent-sdk-python/pyproject.toml || (echo >&2 "missing /opt/claude-agent-sdk-python/pyproject.toml (did you run: git submodule update --init --recursive ?)"; exit 1)
RUN pip install --no-cache-dir -e /opt/claude-agent-sdk-python

COPY services/claude-agent-service /app
RUN pip install --no-cache-dir .

ENV NOUS_SERVICE_PORT=8000
EXPOSE 8000

CMD ["claude-agent-service"]

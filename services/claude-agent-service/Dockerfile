FROM python:3.11-slim

WORKDIR /app

RUN apt-get update \
    && apt-get install -y --no-install-recommends nodejs npm ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Claude Agent SDK relies on Claude Code CLI ("claude") via SubprocessCLITransport.
RUN npm install -g @anthropic-ai/claude-code \
    && npm cache clean --force

# agent-browser: preinstall (pinned) and force CDP-attach mode via wrapper.
ENV PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1
RUN npm install -g agent-browser@0.8.2 \
    && npm cache clean --force \
    && mv /usr/local/bin/agent-browser /usr/local/bin/agent-browser.real
COPY services/claude-agent-service/agent-browser-wrapper.py /usr/local/bin/agent-browser
RUN chmod +x /usr/local/bin/agent-browser

# Install Claude Agent SDK from the vendored reference repo.
COPY references/claude-agent-sdk-python /opt/claude-agent-sdk-python
RUN test -f /opt/claude-agent-sdk-python/pyproject.toml || (echo >&2 "missing /opt/claude-agent-sdk-python/pyproject.toml (did you run: git submodule update --init --recursive ?)"; exit 1)
RUN pip install --no-cache-dir -e /opt/claude-agent-sdk-python

COPY services/claude-agent-service /app
RUN pip install --no-cache-dir .

ENV NOUS_RUNNER_SERVICE_PORT=8000
EXPOSE 8000

CMD ["claude-agent-service"]

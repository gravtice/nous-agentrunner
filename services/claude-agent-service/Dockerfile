FROM python:3.11-slim

WORKDIR /app

# Install Claude Agent SDK from the vendored reference repo.
COPY references/claude-agent-sdk-python /opt/claude-agent-sdk-python
RUN pip install --no-cache-dir -e /opt/claude-agent-sdk-python

COPY services/claude-agent-service /app
RUN pip install --no-cache-dir .

ENV NOUS_SERVICE_PORT=8000
EXPOSE 8000

CMD ["claude-agent-service"]


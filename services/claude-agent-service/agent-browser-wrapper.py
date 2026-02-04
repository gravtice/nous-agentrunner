#!/usr/bin/env python3

import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.request


def die(message: str, code: int = 2) -> None:
    sys.stderr.write(message.rstrip() + "\n")
    raise SystemExit(code)


def strip_opt(args: list[str], name: str) -> list[str]:
    out: list[str] = []
    i = 0
    while i < len(args):
        a = args[i]
        if a == name:
            i += 2 if i + 1 < len(args) else 1
            continue
        if a.startswith(name + "="):
            i += 1
            continue
        out.append(a)
        i += 1
    return out


def has_opt(args: list[str], name: str) -> bool:
    i = 0
    while i < len(args):
        a = args[i]
        if a == name:
            return True
        if a.startswith(name + "="):
            return True
        i += 1
    return False


def first_command(args: list[str]) -> str | None:
    for a in args:
        if a == "--":
            return None
        if a.startswith("-"):
            continue
        return a
    return None


def post_json(url: str, payload: object, headers: dict[str, str]) -> tuple[int, dict[str, str], object]:
    data = json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(url, data=data, method="POST")
    req.add_header("Content-Type", "application/json")
    req.add_header("Accept", "application/json")
    req.add_header("Mcp-Protocol-Version", "2025-03-26")
    for k, v in headers.items():
        req.add_header(k, v)

    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            raw = resp.read()
            out_headers = {k: v for (k, v) in resp.headers.items()}
            try:
                body = json.loads(raw.decode("utf-8")) if raw else {}
            except Exception:
                body = {"_raw": raw.decode("utf-8", errors="replace")}
            return resp.status, out_headers, body
    except urllib.error.HTTPError as e:
        raw = e.read() if e.fp else b""
        out_headers = {k: v for (k, v) in (e.headers.items() if e.headers else [])}
        try:
            body = json.loads(raw.decode("utf-8")) if raw else {}
        except Exception:
            body = {"_raw": raw.decode("utf-8", errors="replace")}
        return e.code, out_headers, body


def mcp_initialize(mcp_url: str) -> str:
    payload = {
        "jsonrpc": "2.0",
        "id": 1,
        "method": "initialize",
        "params": {
            "protocolVersion": "2025-03-26",
            "capabilities": {"tools": {}},
            "clientInfo": {"name": "agent-browser-wrapper", "version": "1"},
        },
    }
    status, headers, body = post_json(mcp_url, payload, headers={})
    if status != 200:
        die(f"agent-browser wrapper error: MCP initialize failed (http={status}, body={body})")

    if isinstance(body, dict) and "error" in body:
        die(f"agent-browser wrapper error: MCP initialize error: {body}")

    sid = ""
    for k, v in headers.items():
        if k.lower() == "mcp-session-id":
            sid = str(v).strip()
            break
    if not sid:
        die("agent-browser wrapper error: MCP initialize did not return Mcp-Session-Id header")
    return sid


def mcp_tools_call(mcp_url: str, session_id: str, name: str, arguments: dict[str, object]) -> dict[str, object]:
    payload = {
        "jsonrpc": "2.0",
        "id": 2,
        "method": "tools/call",
        "params": {"name": name, "arguments": arguments},
    }
    status, _, body = post_json(mcp_url, payload, headers={"Mcp-Session-Id": session_id})
    if status != 200:
        die(f"agent-browser wrapper error: MCP tools/call failed (http={status}, tool={name}, body={body})")

    if not isinstance(body, dict):
        die(f"agent-browser wrapper error: invalid MCP response (tool={name}, body={body})")
    if "error" in body:
        die(f"agent-browser wrapper error: MCP tools/call error (tool={name}, err={body['error']})")

    result = body.get("result") if isinstance(body.get("result"), dict) else {}
    is_error = bool(result.get("isError"))
    content = result.get("content") if isinstance(result.get("content"), list) else []
    text = ""
    if content and isinstance(content[0], dict):
        text = str(content[0].get("text") or "")

    if not text:
        die(f"agent-browser wrapper error: missing tool output (tool={name})")

    try:
        tool_payload = json.loads(text)
    except Exception:
        tool_payload = {"_raw": text}

    if is_error:
        die(f"agent-browser wrapper error: tool '{name}' failed: {tool_payload}")

    if not isinstance(tool_payload, dict):
        die(f"agent-browser wrapper error: invalid tool payload (tool={name}, payload={tool_payload})")
    return tool_payload


def main() -> None:
    real = os.environ.get("AGENT_BROWSER_REAL", "/usr/local/bin/agent-browser.real")
    mcp_url = os.environ.get("COWORK_MCP_URL", "").strip()
    cdp_port = os.environ.get("COWORK_CHROME_CDP_PORT", "").strip()  # legacy fallback
    session = os.environ.get("AGENT_BROWSER_SESSION", "").strip()  # legacy default

    if not os.path.exists(real):
        die(f"agent-browser wrapper error: missing real binary at {real}", 127)

    argv = sys.argv[1:]
    cmd = first_command(argv)
    if cmd in {"install", "launch"}:
        die(f"agent-browser: '{cmd}' is disabled in CoWork (CDP attach only).")

    stripped = strip_opt(argv, "--cdp")
    stripped = strip_opt(stripped, "--cdp-ws")

    if mcp_url:
        session_id = mcp_initialize(mcp_url)
        created = mcp_tools_call(mcp_url, session_id, "browser_create", {})
        cdp = str(created.get("cdp_port") or "").strip()
        sess = str(created.get("session") or "").strip()
        if not cdp:
            die(f"agent-browser wrapper error: browser_create returned empty cdp_port: {created}")

        final = ["--cdp", cdp]
        if sess and not has_opt(stripped, "--session"):
            final.extend(["--session", sess])
        final.extend(stripped)

        proc = subprocess.Popen([real] + final)
        touch_every = 2.0
        next_touch = time.monotonic() + touch_every
        while True:
            ret = proc.poll()
            if ret is not None:
                raise SystemExit(ret)
            now = time.monotonic()
            if now >= next_touch:
                try:
                    mcp_tools_call(mcp_url, session_id, "browser_touch", {"session": sess})
                except BaseException:
                    pass
                next_touch = now + touch_every
            time.sleep(0.2)

    if not cdp_port:
        die(
            "agent-browser wrapper error: COWORK_MCP_URL is not set, and COWORK_CHROME_CDP_PORT is not set "
            "(this environment only supports CDP attach to CoWork Host Chrome)."
        )

    final = ["--cdp", cdp_port]
    if session and not has_opt(stripped, "--session"):
        final.extend(["--session", session])
    final.extend(stripped)

    os.execv(real, [real] + final)


if __name__ == "__main__":
    main()

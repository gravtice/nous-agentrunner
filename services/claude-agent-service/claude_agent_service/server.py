import asyncio
import base64
import contextlib
import json
import os
import shutil
import tempfile
from pathlib import Path
from typing import Any, Optional

from aiohttp import web

from claude_agent_sdk import ClaudeAgentOptions, ClaudeSDKClient
from claude_agent_sdk._errors import CLINotFoundError
from claude_agent_sdk.types import (
    AgentDefinition,
    AssistantMessage,
    PermissionUpdate,
    PermissionResultAllow,
    PermissionResultDeny,
    ResultMessage,
    StreamEvent,
    TextBlock,
    ThinkingBlock,
    ToolPermissionContext,
    ToolResultBlock,
    ToolUseBlock,
    UserMessage,
)


def _env_int(name: str, default: int) -> int:
    v = os.getenv(name)
    if not v:
        return default
    try:
        return int(v)
    except ValueError:
        return default


def _load_b64_json_env(name: str, default: Any) -> Any:
    raw = os.getenv(name, "")
    if not raw:
        return default
    try:
        decoded = base64.b64decode(raw.encode("utf-8"), validate=True)
        return json.loads(decoded.decode("utf-8"))
    except Exception:
        return default


def _decode_service_config() -> dict[str, Any]:
    raw = os.getenv("NOUS_SERVICE_CONFIG_B64", "")
    if not raw:
        return {}
    decoded = base64.b64decode(raw.encode("utf-8"), validate=True)
    cfg = json.loads(decoded.decode("utf-8"))
    if not isinstance(cfg, dict):
        raise ValueError("service_config must be an object")
    return cfg


def _merge_add_dirs(cfg: dict[str, Any], share_dirs: list[str]) -> None:
    if not share_dirs:
        return
    v = cfg.get("add_dirs")
    if v is None:
        cfg["add_dirs"] = share_dirs
        return
    if isinstance(v, list):
        seen = {str(x) for x in v}
        for d in share_dirs:
            if d not in seen:
                v.append(d)
        cfg["add_dirs"] = v


def _normalize_string_list(v: Any, field: str) -> list[str]:
    if v is None:
        return []
    if not isinstance(v, list):
        raise ValueError(f"{field} must be an array")
    out: list[str] = []
    for raw in v:
        if not isinstance(raw, str):
            raise ValueError(f"{field} must be an array of strings")
        s = raw.strip()
        if s:
            out.append(s)
    return out


def _normalize_setting_sources(cfg: dict[str, Any]) -> None:
    v = cfg.get("setting_sources")
    if v is None:
        cfg["setting_sources"] = ["project"]
        return
    sources = _normalize_string_list(v, "setting_sources")
    allowed = {"user", "project", "local"}
    for s in sources:
        if s not in allowed:
            raise ValueError("setting_sources must be one of: user, project, local")
    cfg["setting_sources"] = sources


def _normalize_agents(cfg: dict[str, Any]) -> None:
    v = cfg.get("agents")
    if v is None:
        return
    if not isinstance(v, dict):
        raise ValueError("agents must be an object")

    allowed_models = {"sonnet", "opus", "haiku", "inherit"}
    out: dict[str, AgentDefinition] = {}
    for name, raw in v.items():
        if not isinstance(name, str) or not name.strip():
            raise ValueError("agents keys must be non-empty strings")
        if not isinstance(raw, dict):
            raise ValueError(f"agent {name!r} must be an object")

        description = raw.get("description")
        prompt = raw.get("prompt")
        if not isinstance(description, str) or not description.strip():
            raise ValueError(f"agent {name!r} description is required")
        if not isinstance(prompt, str) or not prompt.strip():
            raise ValueError(f"agent {name!r} prompt is required")

        tools: list[str] | None = None
        if raw.get("tools") is not None:
            tools = _normalize_string_list(raw.get("tools"), f"agents.{name}.tools")
            if len(tools) == 0:
                tools = None

        model = raw.get("model")
        if model is not None:
            if not isinstance(model, str) or not model.strip():
                raise ValueError(f"agent {name!r} model must be a non-empty string")
            if model not in allowed_models:
                raise ValueError(f"agent {name!r} model must be one of: sonnet, opus, haiku, inherit")

        out[name] = AgentDefinition(
            description=description,
            prompt=prompt,
            tools=tools,
            model=model,
        )

    cfg["agents"] = out


def _normalize_options(cfg: dict[str, Any], share_dirs: list[str]) -> ClaudeAgentOptions:
    cfg = dict(cfg)
    cfg.setdefault("include_partial_messages", True)
    cfg.setdefault("permission_mode", "bypassPermissions")
    _normalize_setting_sources(cfg)
    if "allowed_tools" in cfg:
        cfg["allowed_tools"] = _normalize_string_list(cfg.get("allowed_tools"), "allowed_tools")
    if "disallowed_tools" in cfg:
        cfg["disallowed_tools"] = _normalize_string_list(cfg.get("disallowed_tools"), "disallowed_tools")
    _normalize_agents(cfg)
    _merge_add_dirs(cfg, share_dirs)
    return ClaudeAgentOptions(**cfg)


def _prompt_from_contents(contents: list[dict[str, Any]], session_dir: Path, max_inline: int) -> str:
    lines: list[str] = []
    for c in contents:
        kind = c.get("kind")
        if kind == "text":
            lines.append(str(c.get("text", "")))
            continue

        source = c.get("source") or {}
        stype = source.get("type")
        mime = source.get("mime") or ""
        if stype == "path":
            path = source.get("path") or ""
            lines.append(f"[{kind} path] {path} ({mime})")
            continue
        if stype == "bytes":
            enc = (source.get("encoding") or "").lower()
            if enc != "base64":
                raise ValueError("bytes encoding must be base64")
            data = source.get("data") or ""
            raw = base64.b64decode(data.encode("utf-8"), validate=True)
            if len(raw) > max_inline:
                raise ValueError("INLINE_BYTES_TOO_LARGE")
            suffix = ""
            if "/" in mime:
                suffix = "." + mime.split("/", 1)[1]
            fd, tmp_path = tempfile.mkstemp(prefix=f"{kind}-", suffix=suffix, dir=session_dir)
            os.close(fd)
            Path(tmp_path).write_bytes(raw)
            lines.append(f"[{kind} bytes] {tmp_path} ({mime})")
            continue

        raise ValueError(f"unsupported source type: {stype!r}")
    return "\n".join(lines).strip()


def _extract_assistant_text(msg: AssistantMessage) -> str:
    parts: list[str] = []
    for block in msg.content:
        if isinstance(block, TextBlock):
            parts.append(block.text)
    return "".join(parts)


def _stream_delta_text(ev: dict[str, Any]) -> Optional[str]:
    if ev.get("type") != "content_block_delta":
        return None
    delta = ev.get("delta") or {}
    if delta.get("type") != "text_delta":
        return None
    text = delta.get("text")
    if not isinstance(text, str) or text == "":
        return None
    return text


def _stream_delta_thinking(ev: dict[str, Any]) -> Optional[str]:
    if ev.get("type") != "content_block_delta":
        return None
    delta = ev.get("delta") or {}
    if delta.get("type") != "thinking_delta":
        return None
    text = delta.get("thinking")
    if not isinstance(text, str) or text == "":
        return None
    return text


async def ws_chat(request: web.Request) -> web.WebSocketResponse:
    ws = web.WebSocketResponse()
    await ws.prepare(request)

    session_id = request.query.get("session_id") or os.urandom(8).hex()
    await ws.send_json({"type": "session.started", "session_id": session_id})

    max_inline = _env_int("NOUS_MAX_INLINE_BYTES", 8 * 1024 * 1024)
    ask_timeout_seconds = float(os.getenv("NOUS_ASK_TIMEOUT_SECONDS", "300") or "300")
    share_dirs = _load_b64_json_env("NOUS_SHARE_DIRS_B64", [])
    if not isinstance(share_dirs, list):
        share_dirs = []
    share_dirs = [str(x) for x in share_dirs if str(x)]

    try:
        service_cfg = _decode_service_config()
    except Exception as e:
        await ws.send_json({"type": "error", "code": "BAD_CONFIG", "message": str(e)})
        await ws.close()
        return ws

    try:
        options = _normalize_options(service_cfg, share_dirs)
    except Exception as e:
        await ws.send_json({"type": "error", "code": "BAD_CONFIG", "message": str(e)})
        await ws.close()
        return ws

    session_tmp = tempfile.TemporaryDirectory(prefix=f"nous-claude-{session_id}-")
    session_dir = Path(session_tmp.name)

    try:
        pending_asks: dict[str, asyncio.Future[dict[str, Any]]] = {}
        current_permission_mode = options.permission_mode or "bypassPermissions"
        allowed_tools = {t for t in (options.allowed_tools or []) if isinstance(t, str) and t.strip()}
        disallowed_tools = {t for t in (options.disallowed_tools or []) if isinstance(t, str) and t.strip()}
        enforced_tools = {
            "AskUserQuestion",
            "Bash",
            "Edit",
            "Glob",
            "Grep",
            "MultiEdit",
            "Read",
            "WebFetch",
            "Write",
        }

        def cancel_pending_asks() -> None:
            for fut in pending_asks.values():
                if not fut.done():
                    fut.cancel()
            pending_asks.clear()

        async def set_local_permission_mode(mode: str) -> None:
            nonlocal current_permission_mode
            if current_permission_mode == mode:
                return
            current_permission_mode = mode
            with contextlib.suppress(Exception):
                await ws.send_json({"type": "permission_mode.updated", "mode": mode})

        async def can_use_tool(
            tool_name: str, input_data: dict[str, Any], _: ToolPermissionContext
        ) -> PermissionResultAllow | PermissionResultDeny:
            if tool_name == "EnterPlanMode":
                await set_local_permission_mode("plan")
                return PermissionResultAllow(
                    updated_permissions=[
                        PermissionUpdate(
                            type="setMode",
                            mode="plan",
                            destination="session",
                        )
                    ]
                )
            if tool_name == "ExitPlanMode":
                await set_local_permission_mode("bypassPermissions")
                return PermissionResultAllow(
                    updated_permissions=[
                        PermissionUpdate(
                            type="setMode",
                            mode="bypassPermissions",
                            destination="session",
                        )
                    ]
                )

            if tool_name in disallowed_tools:
                return PermissionResultDeny(message=f"tool disallowed: {tool_name}")
            if allowed_tools:
                if tool_name.startswith("mcp__") or tool_name in enforced_tools:
                    if tool_name not in allowed_tools:
                        return PermissionResultDeny(message=f"tool not allowed: {tool_name}")

            if tool_name != "AskUserQuestion":
                return PermissionResultAllow()

            ask_id = "ask_" + os.urandom(8).hex()
            fut: asyncio.Future[dict[str, Any]] = asyncio.get_running_loop().create_future()
            pending_asks[ask_id] = fut
            try:
                await ws.send_json({"type": "agent.ask", "ask_id": ask_id, "input": input_data})
                answers = await asyncio.wait_for(fut, timeout=ask_timeout_seconds)
                questions = input_data.get("questions", [])
                if not isinstance(questions, list):
                    questions = []
                return PermissionResultAllow(updated_input={"questions": questions, "answers": answers})
            except asyncio.TimeoutError:
                return PermissionResultDeny(message="ask timeout", interrupt=True)
            except asyncio.CancelledError:
                return PermissionResultDeny(message="ask cancelled", interrupt=True)
            except Exception as e:
                return PermissionResultDeny(message=str(e), interrupt=True)
            finally:
                pending_asks.pop(ask_id, None)

        options.can_use_tool = can_use_tool

        async with ClaudeSDKClient(options=options) as client:
            running: Optional[asyncio.Task[None]] = None
            cancel_event = asyncio.Event()

            async def run_query(contents: list[dict[str, Any]]) -> None:
                try:
                    cancel_event.clear()
                    prompt = _prompt_from_contents(contents, session_dir, max_inline)
                    await client.query(prompt, session_id=session_id)

                    final_text_parts: list[str] = []
                    thinking_sent: str = ""
                    seen_tool_uses: set[str] = set()
                    seen_tool_results: set[str] = set()
                    tool_use_name_by_id: dict[str, str] = {}

                    stream_tool_uses: dict[int, dict[str, Any]] = {}
                    result: ResultMessage | None = None
                    async for m in client.receive_response():
                        if cancel_event.is_set():
                            break
                        if isinstance(m, StreamEvent):
                            delta = _stream_delta_text(m.event)
                            if delta:
                                final_text_parts.append(delta)
                                await ws.send_json({"type": "response.delta", "text": delta})
                            tdelta = _stream_delta_thinking(m.event)
                            if tdelta:
                                thinking_sent += tdelta
                                await ws.send_json({"type": "response.thinking.delta", "text": tdelta})

                            ev = m.event
                            etype = ev.get("type")
                            if etype == "content_block_start":
                                idx = ev.get("index")
                                block = ev.get("content_block") or {}
                                if isinstance(idx, int) and block.get("type") == "tool_use":
                                    stream_tool_uses[idx] = {
                                        "id": block.get("id"),
                                        "name": block.get("name"),
                                        "input": block.get("input"),
                                        "input_json_parts": [],
                                    }
                            elif etype == "content_block_delta":
                                idx = ev.get("index")
                                state = stream_tool_uses.get(idx) if isinstance(idx, int) else None
                                if state:
                                    delta_obj = ev.get("delta") or {}
                                    if delta_obj.get("type") == "input_json_delta":
                                        part = delta_obj.get("partial_json")
                                        if isinstance(part, str) and part != "":
                                            state["input_json_parts"].append(part)
                            elif etype == "content_block_stop":
                                idx = ev.get("index")
                                state = stream_tool_uses.pop(idx, None) if isinstance(idx, int) else None
                                if state:
                                    tool_id = state.get("id")
                                    tool_name = state.get("name")
                                    if (
                                        isinstance(tool_id, str)
                                        and tool_id
                                        and isinstance(tool_name, str)
                                        and tool_name
                                        and tool_id not in seen_tool_uses
                                    ):
                                        tool_use_name_by_id[tool_id] = tool_name
                                        if tool_name == "EnterPlanMode":
                                            await set_local_permission_mode("plan")
                                        elif tool_name == "ExitPlanMode":
                                            await set_local_permission_mode("bypassPermissions")
                                        payload: dict[str, Any] = {
                                            "type": "tool.use",
                                            "id": tool_id,
                                            "name": tool_name,
                                        }
                                        parts = state.get("input_json_parts") or []
                                        if parts:
                                            raw = "".join(parts)
                                            try:
                                                payload["input"] = json.loads(raw)
                                            except Exception:
                                                payload["input_json"] = raw
                                        else:
                                            inp = state.get("input")
                                            if isinstance(inp, dict):
                                                payload["input"] = inp
                                        await ws.send_json(payload)
                                        seen_tool_uses.add(tool_id)
                            continue
                        if isinstance(m, AssistantMessage):
                            # Fallback if deltas are not available.
                            txt = _extract_assistant_text(m)
                            if txt:
                                final_text_parts = [txt]
                            for block in m.content:
                                if isinstance(block, ThinkingBlock):
                                    t = block.thinking
                                    if isinstance(t, str) and t != "":
                                        if t.startswith(thinking_sent):
                                            delta = t[len(thinking_sent) :]
                                            if delta:
                                                thinking_sent = t
                                                await ws.send_json(
                                                    {"type": "response.thinking.delta", "text": delta}
                                                )
                                        else:
                                            thinking_sent = t
                                            await ws.send_json(
                                                {"type": "response.thinking.delta", "text": t, "reset": True}
                                            )
                                elif isinstance(block, ToolUseBlock):
                                    if block.id not in seen_tool_uses:
                                        tool_use_name_by_id[block.id] = block.name
                                        if block.name == "EnterPlanMode":
                                            await set_local_permission_mode("plan")
                                        elif block.name == "ExitPlanMode":
                                            await set_local_permission_mode("bypassPermissions")
                                        await ws.send_json(
                                            {
                                                "type": "tool.use",
                                                "id": block.id,
                                                "name": block.name,
                                                "input": block.input,
                                            }
                                        )
                                        seen_tool_uses.add(block.id)
                                elif isinstance(block, ToolResultBlock):
                                    if block.tool_use_id not in seen_tool_results:
                                        tool_name = tool_use_name_by_id.get(block.tool_use_id)
                                        if tool_name == "ExitPlanMode" and not block.is_error:
                                            await set_local_permission_mode("bypassPermissions")
                                        await ws.send_json(
                                            {
                                                "type": "tool.result",
                                                "tool_use_id": block.tool_use_id,
                                                "content": block.content,
                                                "is_error": block.is_error,
                                            }
                                        )
                                        seen_tool_results.add(block.tool_use_id)
                        if isinstance(m, UserMessage) and isinstance(m.content, list):
                            for block in m.content:
                                if isinstance(block, ToolResultBlock):
                                    if block.tool_use_id not in seen_tool_results:
                                        tool_name = tool_use_name_by_id.get(block.tool_use_id)
                                        if tool_name == "ExitPlanMode" and not block.is_error:
                                            await set_local_permission_mode("bypassPermissions")
                                        await ws.send_json(
                                            {
                                                "type": "tool.result",
                                                "tool_use_id": block.tool_use_id,
                                                "content": block.content,
                                                "is_error": block.is_error,
                                            }
                                        )
                                        seen_tool_results.add(block.tool_use_id)
                        if isinstance(m, ResultMessage):
                            result = m
                            break

                    final_text = "".join(final_text_parts)
                    await ws.send_json(
                        {"type": "response.final", "contents": [{"kind": "text", "text": final_text}]}
                    )
                    if result is not None:
                        await ws.send_json(
                            {
                                "type": "response.usage",
                                "usage": result.usage,
                                "total_cost_usd": result.total_cost_usd,
                                "duration_ms": result.duration_ms,
                                "duration_api_ms": result.duration_api_ms,
                            }
                        )
                    await ws.send_json({"type": "done"})
                except Exception as e:
                    await ws.send_json({"type": "error", "code": "SERVICE_ERROR", "message": str(e)})
                    await ws.send_json({"type": "done"})

            async for msg in ws:
                if msg.type != web.WSMsgType.TEXT:
                    continue
                try:
                    payload = json.loads(msg.data)
                except Exception:
                    await ws.send_json({"type": "error", "code": "BAD_REQUEST", "message": "invalid json"})
                    continue

                mtype = payload.get("type")
                if mtype == "cancel":
                    cancel_event.set()
                    cancel_pending_asks()
                    if running and not running.done():
                        await client.interrupt()
                    continue

                if mtype == "ask.answer":
                    ask_id = payload.get("ask_id")
                    answers = payload.get("answers")
                    if not isinstance(ask_id, str) or ask_id.strip() == "":
                        await ws.send_json({"type": "error", "code": "BAD_REQUEST", "message": "ask_id is required"})
                        continue
                    if not isinstance(answers, dict):
                        await ws.send_json({"type": "error", "code": "BAD_REQUEST", "message": "answers must be an object"})
                        continue
                    fut = pending_asks.get(ask_id)
                    if fut is None or fut.done():
                        await ws.send_json({"type": "error", "code": "BAD_REQUEST", "message": "unknown ask_id"})
                        continue
                    fut.set_result({str(k): str(v) for k, v in answers.items()})
                    continue

                if mtype == "permission_mode.set":
                    mode = payload.get("mode")
                    if not isinstance(mode, str) or mode.strip() == "":
                        await ws.send_json({"type": "error", "code": "BAD_REQUEST", "message": "mode is required"})
                        continue
                    mode = mode.strip()
                    if mode not in {"default", "acceptEdits", "plan", "bypassPermissions"}:
                        await ws.send_json(
                            {"type": "error", "code": "BAD_REQUEST", "message": f"unsupported mode: {mode!r}"}
                        )
                        continue
                    try:
                        await client.set_permission_mode(mode)
                        await set_local_permission_mode(mode)
                    except Exception as e:
                        await ws.send_json({"type": "error", "code": "SERVICE_ERROR", "message": str(e)})
                    continue

                if mtype != "input":
                    await ws.send_json(
                        {"type": "error", "code": "BAD_REQUEST", "message": "unsupported message type"}
                    )
                    continue

                contents = payload.get("contents") or []
                if not isinstance(contents, list) or not contents:
                    await ws.send_json({"type": "error", "code": "BAD_REQUEST", "message": "contents is required"})
                    continue

                if running and not running.done():
                    await ws.send_json({"type": "error", "code": "BUSY", "message": "previous request still running"})
                    continue

                running = asyncio.create_task(run_query(contents))

            if running and not running.done():
                cancel_event.set()
                cancel_pending_asks()
                await client.interrupt()
                with contextlib.suppress(Exception):
                    await running
    except CLINotFoundError as e:
        await ws.send_json({"type": "error", "code": "CLI_NOT_FOUND", "message": str(e)})
        await ws.send_json({"type": "done"})
    except Exception as e:
        await ws.send_json({"type": "error", "code": "SERVICE_UNAVAILABLE", "message": str(e)})
        await ws.send_json({"type": "done"})
    finally:
        session_tmp.cleanup()

    await ws.close()
    return ws


async def health(_: web.Request) -> web.Response:
    issues: list[str] = []
    if shutil.which("claude") is None:
        issues.append("claude_cli_not_found")
    ok = len(issues) == 0
    return web.json_response({"ok": ok, "issues": issues}, status=200 if ok else 503)


async def run() -> None:
    port = _env_int("NOUS_SERVICE_PORT", 8000)

    app = web.Application()
    app.router.add_get("/health", health)
    app.router.add_get("/v1/chat", ws_chat)

    runner = web.AppRunner(app)
    await runner.setup()
    site = web.TCPSite(runner, "0.0.0.0", port)
    await site.start()

    # Run forever.
    while True:
        await asyncio.sleep(3600)

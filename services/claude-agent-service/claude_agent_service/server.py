import asyncio
import base64
import contextlib
import fnmatch
import json
import os
import shutil
import tempfile
import time
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

DEFAULT_ALLOWED_TOOLS_ALL = [
    "AskUserQuestion",
    "Bash",
    "Edit",
    "Glob",
    "Grep",
    "Skill",
    "MultiEdit",
    "Read",
    "WebFetch",
    "Write",
]


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


def _mcp_server_names_from_config(v: Any) -> list[str]:
    cfg: dict[str, Any]
    if isinstance(v, dict):
        cfg = v
    elif isinstance(v, str):
        raw = v.strip()
        if raw == "":
            return []
        if raw.startswith("{"):
            try:
                obj = json.loads(raw)
            except Exception:
                return []
            if not isinstance(obj, dict):
                return []
            cfg = obj
        else:
            try:
                raw = Path(raw).read_text(encoding="utf-8").strip()
                obj = json.loads(raw)
            except Exception:
                return []
            if not isinstance(obj, dict):
                return []
            cfg = obj
    else:
        return []

    servers = cfg.get("mcpServers")
    if isinstance(servers, dict):
        cfg = servers

    out: list[str] = []
    for k in cfg.keys():
        if isinstance(k, str):
            name = k.strip()
            if name:
                out.append(name)
    out.sort()
    return out


def _normalize_mcp_servers(cfg: dict[str, Any]) -> None:
    v = cfg.get("mcp_servers")
    if not isinstance(v, dict):
        return
    servers = v.get("mcpServers")
    if isinstance(servers, dict):
        cfg["mcp_servers"] = servers


def _expand_allowed_tools_all(cfg: dict[str, Any]) -> None:
    raw = cfg.get("allowed_tools")
    if not isinstance(raw, list):
        return
    allowed = [t.strip() for t in raw if isinstance(t, str) and t.strip()]
    if allowed != ["*"]:
        return

    tools = set(DEFAULT_ALLOWED_TOOLS_ALL)
    for server_name in _mcp_server_names_from_config(cfg.get("mcp_servers")):
        tools.add(f"mcp__{server_name}__*")
    cfg["allowed_tools"] = sorted(tools)


def _looks_like_glob(pattern: str) -> bool:
    return any(c in pattern for c in ("*", "?", "["))


def _split_tool_patterns(tools: list[str]) -> tuple[set[str], list[str]]:
    exact: set[str] = set()
    patterns: list[str] = []
    for t in tools:
        if _looks_like_glob(t):
            patterns.append(t)
        else:
            exact.add(t)
    return exact, patterns


def _matches_any(tool_name: str, exact: set[str], patterns: list[str]) -> bool:
    if tool_name in exact:
        return True
    for pattern in patterns:
        if fnmatch.fnmatchcase(tool_name, pattern):
            return True
    return False


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


def _normalize_model_fields(cfg: dict[str, Any]) -> None:
    for field in ("model", "fallback_model"):
        v = cfg.get(field)
        if v is None:
            continue
        if not isinstance(v, str) or not v.strip():
            raise ValueError(f"{field} must be a non-empty string")
        cfg[field] = v.strip()


def _normalize_options(cfg: dict[str, Any], share_dirs: list[str]) -> ClaudeAgentOptions:
    cfg = dict(cfg)
    cfg.setdefault("include_partial_messages", True)
    cfg.setdefault("permission_mode", "bypassPermissions")
    _normalize_mcp_servers(cfg)
    _expand_allowed_tools_all(cfg)
    _normalize_setting_sources(cfg)
    if "allowed_tools" in cfg:
        cfg["allowed_tools"] = _normalize_string_list(cfg.get("allowed_tools"), "allowed_tools")
    if "disallowed_tools" in cfg:
        cfg["disallowed_tools"] = _normalize_string_list(cfg.get("disallowed_tools"), "disallowed_tools")
    _normalize_model_fields(cfg)
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
    first_event_timeout_seconds = float(os.getenv("NOUS_FIRST_EVENT_TIMEOUT_SECONDS", "20") or "20")
    if first_event_timeout_seconds <= 0:
        first_event_timeout_seconds = 20.0
    share_dirs = _load_b64_json_env("NOUS_SHARE_DIRS_B64", [])
    if not isinstance(share_dirs, list):
        share_dirs = []
    share_dirs = [str(x) for x in share_dirs if str(x)]

    try:
        service_cfg = _decode_service_config()
    except Exception as e:
        await ws.send_json({"type": "error", "code": "BAD_CONFIG", "message": str(e), "fatal": True})
        await ws.close()
        return ws
    allowlist_configured = "allowed_tools" in service_cfg
    disallowlist_configured = "disallowed_tools" in service_cfg
    tool_restrictions_configured = allowlist_configured or disallowlist_configured

    try:
        options = _normalize_options(service_cfg, share_dirs)
    except Exception as e:
        await ws.send_json({"type": "error", "code": "BAD_CONFIG", "message": str(e), "fatal": True})
        await ws.close()
        return ws

    session_tmp = tempfile.TemporaryDirectory(prefix=f"nous-claude-{session_id}-")
    session_dir = Path(session_tmp.name)

    try:
        pending_asks: dict[str, asyncio.Future[dict[str, Any]]] = {}
        requested_permission_mode = options.permission_mode or "bypassPermissions"
        current_permission_mode = requested_permission_mode
        allowed_tools_raw = [t.strip() for t in (options.allowed_tools or []) if isinstance(t, str) and t.strip()]
        disallowed_tools_raw = [t.strip() for t in (options.disallowed_tools or []) if isinstance(t, str) and t.strip()]
        allowed_tools, allowed_tool_patterns = _split_tool_patterns(allowed_tools_raw)
        disallowed_tools, disallowed_tool_patterns = _split_tool_patterns(disallowed_tools_raw)
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
        bypass_cli_mode = "default" if tool_restrictions_configured else "bypassPermissions"
        if requested_permission_mode == "bypassPermissions" and tool_restrictions_configured:
            options.permission_mode = "default"

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
                            mode=bypass_cli_mode,
                            destination="session",
                        )
                    ]
                )

            if _matches_any(tool_name, disallowed_tools, disallowed_tool_patterns):
                return PermissionResultDeny(message=f"tool disallowed: {tool_name}")
            if allowlist_configured:
                if tool_name.startswith("mcp__") or tool_name in enforced_tools:
                    if not _matches_any(tool_name, allowed_tools, allowed_tool_patterns):
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
                    suppress_output = False
                    msg_iter = client.receive_response().__aiter__()
                    first_response_deadline = time.monotonic() + first_event_timeout_seconds
                    received_response_event = False
                    while True:
                        try:
                            if not received_response_event:
                                remaining = first_response_deadline - time.monotonic()
                                if remaining <= 0:
                                    raise asyncio.TimeoutError()
                                m = await asyncio.wait_for(msg_iter.__anext__(), timeout=remaining)
                            else:
                                m = await msg_iter.__anext__()
                        except StopAsyncIteration:
                            break
                        except asyncio.TimeoutError:
                            model = getattr(options, "model", None) or ""
                            hint = ""
                            if isinstance(model, str) and model.strip():
                                hint = f" (model={model.strip()!r})"
                            await ws.send_json(
                                {
                                    "type": "error",
                                    "code": "SERVICE_ERROR",
                                    "message": "no response from claude CLI within "
                                    + str(int(first_event_timeout_seconds))
                                    + "s"
                                    + hint,
                                }
                            )
                            await ws.send_json({"type": "done"})
                            with contextlib.suppress(Exception):
                                await ws.close()
                            return
                        if not received_response_event and isinstance(
                            m, (StreamEvent, AssistantMessage, ResultMessage)
                        ):
                            received_response_event = True
                        if cancel_event.is_set() and not suppress_output:
                            # Keep draining the SDK stream after interrupt so the next query starts clean.
                            suppress_output = True
                        if suppress_output:
                            if isinstance(m, ResultMessage):
                                result = m
                            continue
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

                    if cancel_event.is_set():
                        final_text = "".join(final_text_parts)
                        await ws.send_json(
                            {"type": "response.final", "contents": [{"kind": "text", "text": final_text}]}
                        )
                        await ws.send_json({"type": "done"})
                        return

                    if result is not None and result.is_error:
                        msg = result.result
                        if not isinstance(msg, str) or msg.strip() == "":
                            msg = "request failed"
                        await ws.send_json({"type": "error", "code": "SERVICE_ERROR", "message": msg, "fatal": False})
                        await ws.send_json({"type": "done"})
                        return
                    if result is None:
                        msg = "".join(final_text_parts).strip()
                        if not msg:
                            msg = "claude CLI ended without a result"
                        await ws.send_json({"type": "error", "code": "SERVICE_ERROR", "message": msg, "fatal": False})
                        await ws.send_json({"type": "done"})
                        return

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
                    await ws.send_json({"type": "error", "code": "SERVICE_ERROR", "message": str(e), "fatal": False})
                    await ws.send_json({"type": "done"})

            async for msg in ws:
                if msg.type != web.WSMsgType.TEXT:
                    continue
                try:
                    payload = json.loads(msg.data)
                except Exception:
                    await ws.send_json({"type": "error", "code": "BAD_REQUEST", "message": "invalid json", "fatal": False})
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
                        await ws.send_json({"type": "error", "code": "BAD_REQUEST", "message": "ask_id is required", "fatal": False})
                        continue
                    if not isinstance(answers, dict):
                        await ws.send_json({"type": "error", "code": "BAD_REQUEST", "message": "answers must be an object", "fatal": False})
                        continue
                    fut = pending_asks.get(ask_id)
                    if fut is None or fut.done():
                        await ws.send_json({"type": "error", "code": "BAD_REQUEST", "message": "unknown ask_id", "fatal": False})
                        continue
                    fut.set_result({str(k): str(v) for k, v in answers.items()})
                    continue

                if mtype == "permission_mode.set":
                    mode = payload.get("mode")
                    if not isinstance(mode, str) or mode.strip() == "":
                        await ws.send_json({"type": "error", "code": "BAD_REQUEST", "message": "mode is required", "fatal": False})
                        continue
                    mode = mode.strip()
                    if mode not in {"default", "acceptEdits", "plan", "bypassPermissions"}:
                        await ws.send_json(
                            {"type": "error", "code": "BAD_REQUEST", "message": f"unsupported mode: {mode!r}", "fatal": False}
                        )
                        continue
                    try:
                        await client.set_permission_mode(bypass_cli_mode if mode == "bypassPermissions" else mode)
                        await set_local_permission_mode(mode)
                    except Exception as e:
                        await ws.send_json({"type": "error", "code": "SERVICE_ERROR", "message": str(e), "fatal": False})
                    continue

                if mtype != "input":
                    await ws.send_json(
                        {"type": "error", "code": "BAD_REQUEST", "message": "unsupported message type", "fatal": False}
                    )
                    continue

                contents = payload.get("contents") or []
                if not isinstance(contents, list) or not contents:
                    await ws.send_json({"type": "error", "code": "BAD_REQUEST", "message": "contents is required", "fatal": False})
                    continue

                if running and not running.done():
                    await ws.send_json({"type": "error", "code": "BUSY", "message": "previous request still running", "fatal": False})
                    continue

                running = asyncio.create_task(run_query(contents))

            if running and not running.done():
                cancel_event.set()
                cancel_pending_asks()
                await client.interrupt()
                with contextlib.suppress(Exception):
                    await running
    except CLINotFoundError as e:
        await ws.send_json({"type": "error", "code": "CLI_NOT_FOUND", "message": str(e), "fatal": True})
    except Exception as e:
        await ws.send_json({"type": "error", "code": "SERVICE_UNAVAILABLE", "message": str(e), "fatal": True})
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

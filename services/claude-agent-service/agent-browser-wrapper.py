#!/usr/bin/env python3

import os
import sys


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


def main() -> None:
    real = os.environ.get("AGENT_BROWSER_REAL", "/usr/local/bin/agent-browser.real")
    cdp_port = os.environ.get("COWORK_CHROME_CDP_PORT", "").strip()
    session = os.environ.get("AGENT_BROWSER_SESSION", "").strip()

    if not os.path.exists(real):
        die(f"agent-browser wrapper error: missing real binary at {real}", 127)
    if not cdp_port:
        die(
            "agent-browser wrapper error: COWORK_CHROME_CDP_PORT is not set "
            "(this environment only supports CDP attach to CoWork Host Chrome)."
        )

    argv = sys.argv[1:]
    cmd = first_command(argv)
    if cmd in {"install", "launch"}:
        die(f"agent-browser: '{cmd}' is disabled in CoWork (CDP attach only).")

    stripped = strip_opt(argv, "--cdp")
    stripped = strip_opt(stripped, "--cdp-ws")

    final = ["--cdp", cdp_port]
    if session and not has_opt(stripped, "--session"):
        final.extend(["--session", session])
    final.extend(stripped)

    os.execv(real, [real] + final)


if __name__ == "__main__":
    main()

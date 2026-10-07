#!/usr/bin/env python3
"""Interactive CLI target picker. JSON via SRAM_CLI_CHOOSE_JSON or argv[1]."""

from __future__ import annotations

import json
import os
import select
import sys
import termios
import tty


def _open_tty_rw():
    """Return (input, output) streams; stdin may be a pipe when the shell passes JSON elsewhere."""
    if sys.stdin.isatty():
        return sys.stdin, sys.stdout
    try:
        t = open("/dev/tty", "r+", encoding="utf-8", buffering=1)
        return t, t
    except OSError:
        return None, None


def _parse_arrow(seq: str) -> str | None:
    """Map escape sequence to up/down (xterm CSI/SS3 and common variants)."""
    if not seq.startswith("\x1b"):
        return None
    if seq.endswith("A"):
        return "up"
    if seq.endswith("B"):
        return "down"
    return None


def _read_action(tty_in) -> str:
    """Read one user action while tty_in is already in raw mode."""
    b = os.read(tty_in.fileno(), 1)
    if not b:
        return "other"
    ch = b.decode("utf-8", errors="replace")
    if ch in ("\r", "\n"):
        return "enter"
    if ch == "\x03":
        raise KeyboardInterrupt
    if ch in ("j", "J"):
        return "down"
    if ch in ("k", "K"):
        return "up"
    if ch.isdigit():
        return f"digit:{ch}"
    if ch == "\x1b":
        buf = ch
        fd = tty_in.fileno()
        for _ in range(8):
            ready, _, _ = select.select([fd], [], [], 0.04)
            if not ready:
                break
            try:
                more = os.read(fd, 32)
            except OSError:
                break
            if not more:
                break
            buf += more.decode("utf-8", errors="replace")
            arrow = _parse_arrow(buf)
            if arrow:
                return arrow
            if len(buf) > 16:
                break
        return "other"
    return "other"


def _pick_interactive(targets: list[dict], tty_in, tty_out) -> str:
    if not targets:
        print("ERROR: no targets to choose", file=sys.stderr)
        sys.exit(1)
    if len(targets) == 1:
        return str(targets[0]["target"])

    idx = 0
    drawn = False
    fd = tty_in.fileno()
    old = termios.tcgetattr(fd)
    hint = "↑/↓ or j/k, number + Enter, Enter to confirm"

    def redraw() -> None:
        nonlocal drawn
        if drawn:
            tty_out.write(f"\033[{len(targets) + 2}A")
        tty_out.write(f"\033[2K\rSelect IAM role ({hint}):\n")
        for i, t in enumerate(targets):
            mark = ">" if i == idx else " "
            label = t.get("label") or t.get("target")
            num = i + 1
            tty_out.write(f"\033[2K\r{mark} {num}. {label}\n")
        tty_out.flush()
        drawn = True

    try:
        tty.setraw(fd)
        redraw()
        while True:
            action = _read_action(tty_in)
            if action == "enter":
                break
            if action == "up" and idx > 0:
                idx -= 1
                redraw()
            elif action == "down" and idx < len(targets) - 1:
                idx += 1
                redraw()
            elif action.startswith("digit:"):
                n = int(action.split(":", 1)[1])
                if 1 <= n <= len(targets):
                    idx = n - 1
                    redraw()
    finally:
        termios.tcsetattr(fd, termios.TCSADRAIN, old)

    tty_out.write("\n")
    tty_out.flush()
    return str(targets[idx]["target"])


def _pick_curses(targets: list[dict]) -> str:
    import curses

    if len(targets) == 1:
        return str(targets[0]["target"])

    def run(stdscr) -> str:
        curses.curs_set(0)
        stdscr.keypad(True)
        idx = 0
        hint = "↑/↓ or j/k, 1-9, Enter"
        while True:
            stdscr.erase()
            stdscr.addstr(0, 0, f"Select IAM role ({hint}):")
            for i, t in enumerate(targets):
                mark = ">" if i == idx else " "
                label = t.get("label") or t.get("target")
                stdscr.addstr(i + 1, 0, f"{mark} {i + 1}. {label}")
            stdscr.refresh()
            ch = stdscr.getch()
            if ch in (curses.KEY_ENTER, 10, 13):
                break
            if ch in (curses.KEY_UP, ord("k"), ord("K")):
                idx = max(0, idx - 1)
            elif ch in (curses.KEY_DOWN, ord("j"), ord("J")):
                idx = min(len(targets) - 1, idx + 1)
            elif ord("1") <= ch <= ord("9"):
                n = ch - ord("1")
                if n < len(targets):
                    idx = n
            elif ch == 27:  # bare Esc
                pass
        return str(targets[idx]["target"])

    return curses.wrapper(run)


def _pick_numbered(targets: list[dict], tty_in, tty_out) -> str:
    tty_out.write("Select IAM role (enter number, then Enter):\n")
    for i, t in enumerate(targets, start=1):
        label = t.get("label") or t.get("target")
        tty_out.write(f"  {i}) {label}\n")
    tty_out.flush()
    while True:
        tty_out.write("Choice: ")
        tty_out.flush()
        line = tty_in.readline().strip()
        if not line:
            continue
        try:
            n = int(line)
        except ValueError:
            tty_out.write("Enter a number from the list.\n")
            tty_out.flush()
            continue
        if 1 <= n <= len(targets):
            return str(targets[n - 1]["target"])
        tty_out.write(f"Enter 1–{len(targets)}.\n")
        tty_out.flush()


def _pick(targets: list[dict]) -> str:
    mode = (os.environ.get("SRAM_CLI_PICKER") or "").strip().lower()
    if mode == "numbered":
        tty_in, tty_out = _open_tty_rw()
        if tty_in is None:
            print("ERROR: interactive picker requires a TTY (/dev/tty)", file=sys.stderr)
            sys.exit(1)
        try:
            return _pick_numbered(targets, tty_in, tty_out)
        finally:
            if tty_in is not sys.stdin:
                tty_in.close()

    if mode != "raw":
        try:
            return _pick_curses(targets)
        except Exception:
            pass

    tty_in, tty_out = _open_tty_rw()
    if tty_in is None:
        print("ERROR: interactive picker requires a TTY (/dev/tty)", file=sys.stderr)
        sys.exit(1)
    try:
        try:
            return _pick_interactive(targets, tty_in, tty_out)
        except (termios.error, OSError, AttributeError):
            return _pick_numbered(targets, tty_in, tty_out)
    finally:
        if tty_in is not sys.stdin:
            tty_in.close()


def _load_targets() -> list[dict]:
    raw = os.environ.get("SRAM_CLI_CHOOSE_JSON", "")
    if not raw.strip() and len(sys.argv) >= 2:
        raw = sys.argv[1]
    if not raw.strip() and not sys.stdin.isatty():
        raw = sys.stdin.read()
    data = json.loads(raw) if raw.strip() else []
    if isinstance(data, dict) and "targets" in data:
        data = data["targets"]
    if not isinstance(data, list):
        print("ERROR: expected JSON array or {targets:[...]}", file=sys.stderr)
        sys.exit(1)
    return data


def main() -> None:
    print(_pick(_load_targets()))


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        sys.stdout.write("\n")
        sys.exit(130)

#!/usr/bin/env python3
"""Attach to a tmux session through a pty and render the client's byte
stream into a character grid (tracking CUP/EL/ED), then print the grid
— borders and status lines included, which capture-pane can't show.

    build/tui-screendump.py SOCKET TARGET COLS ROWS

Dev tool, not payload: the headless eye for TUI work (docs/
tui-layout.md "Verification"). Proved the 2026-08-14 corner-caps
verdict and the border-click #{mouse_pane} resolution; pair with a
pty that injects SGR mouse sequences (press \\x1b[<0;COL;ROWM,
release ...m, 1-based screen coords) to drive clicks headlessly."""
import os, pty, subprocess, sys, time, re, select, fcntl, termios, struct

SOCK = sys.argv[1]
TARGET = sys.argv[2]
COLS, ROWS = int(sys.argv[3]), int(sys.argv[4])

pid, fd = pty.fork()
if pid == 0:
    os.environ["TERM"] = "xterm-256color"
    os.execvp("tmux", ["tmux", "-L", SOCK, "attach", "-t", TARGET])
time.sleep(0.8)
fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", ROWS, COLS, 0, 0))
time.sleep(1.5)
subprocess.run(["tmux", "-L", SOCK, "refresh-client"], capture_output=True)
time.sleep(0.8)
buf = b""
try:
    while True:
        r, _, _ = select.select([fd], [], [], 0.6)
        if not r:
            break
        buf += os.read(fd, 262144)
except OSError:
    pass
subprocess.run(["tmux", "-L", SOCK, "detach-client"], capture_output=True)

grid = [[" "] * COLS for _ in range(ROWS)]
row = col = 0
i = 0
s = buf.decode("utf-8", "replace")
esc = re.compile(r"\x1b(\[[0-9;?]*[A-Za-z]|\][^\x07\x1b]*(\x07|\x1b\\)|[>=()][B0-9]?|[78MDE])")
while i < len(s):
    ch = s[i]
    if ch == "\x1b":
        m = esc.match(s, i)
        if not m:
            i += 1
            continue
        seq = m.group(1)
        if seq.startswith("[") and seq.endswith("H"):
            parts = seq[1:-1].split(";")
            row = (int(parts[0]) if parts[0] else 1) - 1
            col = (int(parts[1]) if len(parts) > 1 and parts[1] else 1) - 1
        elif seq.startswith("[") and seq.endswith("K"):
            if row < ROWS:
                for c in range(col, COLS):
                    grid[row][c] = " "
        elif seq.startswith("[") and seq.endswith("J"):
            for r in range(row, ROWS):
                start = col if r == row else 0
                for c in range(start, COLS):
                    grid[r][c] = " "
        i = m.end()
        continue
    if ch == "\r":
        col = 0
    elif ch == "\n":
        row = min(row + 1, ROWS - 1)
    elif ch == "\b":
        col = max(col - 1, 0)
    elif ch >= " ":
        if row < ROWS and col < COLS:
            grid[row][col] = ch
        col += 1
    i += 1

for r, line in enumerate(grid):
    print("%2d|%s|" % (r, "".join(line).rstrip()))

#!/usr/bin/env bash
#
# svc.sh NAME COMMAND [ARGUMENT ...] — idempotently run a workspace
# process as a window in the in-container `services` tmux session (the
# v2 successor to v1's vibe-svc). Safe on every start: an existing
# window with the same NAME is left alone, so post-start hooks can call
# it unconditionally; logs live in the window's scrollback and
# `vibe attach services` is the door in. No env file is loaded — wrap
# the command with `vibe run`-style loading yourself if it needs
# project env. The one shell-string quoting layer is the tmux respawn
# command at the bottom (v1 rule: do not add another).
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
session="services"

usage() {
  echo "Usage: svc.sh NAME COMMAND [ARGUMENT ...]" >&2
  exit 2
}

name="${1:-}"
shift || true
case "$name" in
  ""|*[!A-Za-z0-9_-]*)
    echo "svc.sh: NAME must be letters, digits, '_' or '-': ${name:-<empty>}" >&2
    usage
    ;;
esac
[ "$#" -gt 0 ] || usage

if ! command -v tmux >/dev/null 2>&1; then
  echo "svc.sh: tmux is not installed in this image (add an agent to image.agents, or install tmux)" >&2
  exit 1
fi

cmd="$(printf "%q " "$@")"

# Corpses are kept (docs/tui-layout.md "Workspace services"): the
# window outlives its process (remain-on-exit) so a crash stays
# visible — the sidebar's ✗ row, the crash log in the scrollback —
# until dismissed from the TUI or the window is killed by hand. The
# option must hold BEFORE the command runs (an instantly-dying command
# would close its window first), so windows are born holding the
# default shell, get the option, then respawn into the real command.
# Scoped per-window: a server-wide remain-on-exit would corpse the
# AGENT windows, whose self-closing exit is their lifecycle.
#
# The inner server loads the payload conf at server start; inert when
# the agent session already started it (same rule as agent-session.sh).
if ! tmux has-session -t "=$session" 2>/dev/null; then
  tmux -u -f "$script_dir/tmux-agent.conf" \
    new-session -d -s "$session" -n "$name"
elif tmux list-windows -t "=$session" -F '#{window_name}' 2>/dev/null | grep -qx -- "$name"; then
  exit 0
else
  tmux new-window -d -t "=$session:" -n "$name"
fi
tmux set-option -w -t "=$session:$name" remain-on-exit on
exec tmux respawn-window -k -t "=$session:$name" "$cmd"

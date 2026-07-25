#!/usr/bin/env bash
#
# prefix+v in vibe tui: grab the host clipboard image (`vibe clip
# --path-only`) and type the resulting container path into the agent
# pane — replaces the whole switch-tab / clip / copy / paste dance with
# one chord.
#
# Runs as a tmux run-shell job on the HOST server; run-shell provides
# TMUX, so plain `tmux` is always the right binary/socket (same rule as
# sidebar/dock). $1 = window id. Everything else comes from the engine:
# `vibe clip` resolves the project from the session's path and streams
# into the dev container through its own Docker client, so this script
# never names containers or runs docker itself.

set -euo pipefail

window="${1:-}"

note() {
  tmux display-message "$1" 2>/dev/null || true
}

# The invoking window's session path, straight from tmux (no
# shell-quoting risk — a repo path containing an apostrophe never
# rides a binding's shell string).
session_path="$(tmux display-message -p -t "$window" '#{session_path}' 2>/dev/null || true)"
if [ -z "$session_path" ]; then
  note "clip: no session context"
  exit 0
fi

exe="$(tmux show-options -gqv @vibe_exe 2>/dev/null)"
if [ -z "$exe" ] || [ ! -x "$exe" ]; then
  note "clip: engine binary unavailable"
  exit 0
fi

# --path-only: on success the LAST stdout line is exactly the container
# path (2>&1 folds stderr in only so a failure toast can show the real
# error line).
if ! out="$(cd "$session_path" && "$exe" clip --path-only 2>&1)"; then
  last_line="$(printf '%s\n' "$out" | tail -1)"
  note "vibe clip: ${last_line:-failed}"
  exit 0
fi
path="$(printf '%s\n' "$out" | tail -1)"
case "$path" in
  /*) ;;
  *)
    note "vibe clip: no container path in output"
    exit 0
    ;;
esac

# Prefer the pane marked as the agent; fall back to the window's active
# pane (ad-hoc windows never get roles stamped, and the engine-built
# agent pane carries no role either — empty role means agent here).
target="$(tmux list-panes -t "$window" -F '#{pane_id} #{@vibe_role}' 2>/dev/null \
  | awk 'NF == 1 || $2 == "agent" { print $1; exit }')"
if [ -z "$target" ]; then
  target="$(tmux list-panes -t "$window" -F '#{?pane_active,#{pane_id},}' 2>/dev/null | grep . | head -1)"
fi
if [ -z "$target" ]; then
  note "clip saved ($path) but no pane to type it into"
  exit 0
fi

# Literal keystrokes, no Enter — the path lands in the agent's prompt for
# you to submit (or prepend words to).
tmux send-keys -t "$target" -l "$path"
note "clip → $path"

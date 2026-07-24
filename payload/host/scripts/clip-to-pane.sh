#!/usr/bin/env bash
#
# prefix+v in vibe tui: grab the host clipboard image (clip-image.sh) and
# type the resulting container path into the agent pane — replaces the
# whole switch-tab / clip / copy / paste dance with one chord.
#
# Runs as a tmux run-shell job on the HOST server; run-shell provides
# TMUX, so plain `tmux` is the right binary/socket (same rule as
# sidebar/dock). $1 = trusted payload host dir (a store artifact path,
# stamped as @vibe_payload_dir by `vibe tui`), $2 = window id.
#
# The session path is derived FROM tmux here rather than interpolated
# into the binding's shell string: a repo path containing an apostrophe
# would otherwise break the binding's quoting. The payload dir is a
# store path we control (apostrophe-free by construction). The dev
# container is the engine's `<session>-dev` (session = vibe-<id12>,
# container naming per internal/model/names.go — engine ABI).

set -euo pipefail

payload_dir="${1:-}"
window="${2:-}"

note() {
  tmux display-message "$1" 2>/dev/null || true
}

# The invoking window's session path and name, straight from tmux (no
# shell-quoting risk).
session_path="$(tmux display-message -p -t "$window" '#{session_path}' 2>/dev/null || true)"
session_name="$(tmux display-message -p -t "$window" '#{session_name}' 2>/dev/null || true)"
if [ -z "$session_path" ] || [ -z "$session_name" ]; then
  note "clip: no session context"
  exit 0
fi

# The project's dev container, if running (clip-image streams into its
# /tmp; without one it explains itself).
container="$(docker ps --format '{{.Names}}' 2>/dev/null | grep -Fx "${session_name}-dev" || true)"

clip="$payload_dir/scripts/clip-image.sh"
if [ ! -f "$clip" ]; then
  note "clip: payload script missing"
  exit 0
fi

# --path-only: on success the LAST stdout line is exactly the container
# path (human chatter goes to stderr; 2>&1 folds it in only so a failure
# toast can show the real error line).
if ! out="$(bash "$clip" "$session_path" "" "$container" --path-only 2>&1)"; then
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

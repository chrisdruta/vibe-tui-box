#!/usr/bin/env bash
#
# prefix+v in vibe tui: grab the host clipboard image (`vibe clip`) and type
# the resulting container path into the agent pane — replaces the whole
# switch-tab / clip / copy / paste dance with one chord.
#
# Runs as a tmux run-shell job on the HOST server; run-shell provides TMUX, so
# plain `tmux` is the right binary/socket (same rule as sidebar/dock).
# $1 = trusted harness dir (a store version dir), $2 = window id.
#
# The session path is derived FROM tmux here rather than interpolated into the
# binding's shell string (M-1): a repo path containing an apostrophe would
# otherwise break the binding's quoting. The harness dir is a store path we
# control (apostrophe-free by construction) and is used to invoke the trusted
# launcher directly — the root ./vibe symlink is gone.

set -euo pipefail

harness_dir="${1:-}"
window="${2:-}"

note() {
  tmux display-message "$1" 2>/dev/null || true
}

# The invoking window's session path, straight from tmux (no shell-quoting risk).
session_path="$(tmux display-message -p -t "$window" '#{session_path}' 2>/dev/null || true)"
if [ -n "$session_path" ]; then
  cd "$session_path" || { note "clip: bad session path"; exit 0; }
fi
vibe_launch="$harness_dir/vibe"
[ -x "$vibe_launch" ] || vibe_launch="bash $harness_dir/vibe"

# --path-only: on success the LAST stdout line is exactly the container
# path (human chatter goes to stderr; 2>&1 folds it in only so a failure
# toast can show the real error line).
if ! out="$($vibe_launch clip --path-only 2>&1)"; then
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

# Prefer the pane tui.sh marked as the agent; fall back to the window's
# active pane (ad-hoc windows never get roles stamped).
target="$(tmux list-panes -t "$window" -F '#{pane_id} #{@vibe_role}' 2>/dev/null \
  | awk '$2 == "agent" { print $1; exit }')"
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

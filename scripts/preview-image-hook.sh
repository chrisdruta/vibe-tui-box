#!/usr/bin/env bash
#
# Claude Code hook: auto-preview images beside the TUI. Wired (via
# templates/claude-settings.json) to two events:
#   UserPromptSubmit — an image path pasted into the prompt (e.g. from
#     `vibe clip`) pops a preview split the moment you submit;
#   PostToolUse (matcher: Read) — whenever the agent reads an image file,
#     you see what it sees.
# The TUI itself can't render images (upstream: not planned), so this is the
# closest thing to inline display: a transient, unfocused tmux split running
# show-image.sh that closes itself after VIBE_PREVIEW_SECONDS (default 15).
#
# Hook contract: JSON on stdin; stdout must stay EMPTY (UserPromptSubmit
# stdout is injected into the model's context). Always exit 0 — a preview
# failure must never block the agent.
set -uo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

payload="$(cat)"
[ -n "${TMUX:-}" ] || exit 0
command -v jq >/dev/null 2>&1 || exit 0

event="$(jq -r '.hook_event_name // empty' <<<"$payload")"
case "$event" in
  UserPromptSubmit)
    # First absolute image path mentioned in the prompt (paths with spaces
    # won't match — fine for /tmp/clip-*.png and typical workspace paths).
    prompt="$(jq -r '.prompt // empty' <<<"$payload")"
    path="$(grep -oE '/[^[:space:]"'"'"']+\.(png|jpe?g|gif|webp|bmp)' <<<"$prompt" | head -1)"
    if [ -z "$path" ]; then
      case "$prompt" in
        *'[Image #'*)
          # Pasting the path of an EXISTING image makes the TUI attach the
          # file and replace the text with "[Image #N]" — the payload never
          # carries the path, and the agent won't Read a file it already
          # received, so neither hook event would fire. Best effort: preview
          # the newest recent `vibe clip` capture (workspace-mode clips and
          # images attached by other means won't match — acceptable).
          path="$(find /tmp -maxdepth 1 -name 'clip-*.png' -mmin -10 2>/dev/null | sort | tail -1)"
          ;;
      esac
    fi
    ;;
  PostToolUse)
    path="$(jq -r '.tool_input.file_path // empty' <<<"$payload")"
    case "$path" in
      *.png | *.jpg | *.jpeg | *.gif | *.webp | *.bmp) : ;;
      *) exit 0 ;;
    esac
    ;;
  *)
    exit 0
    ;;
esac
[ -n "$path" ] && [ -f "$path" ] || exit 0

# Debounce: prompt-paste and the agent's subsequent Read of the same file
# would otherwise pop two previews back to back. Keyed per window so parallel
# agent sessions in other windows don't suppress each other's previews.
window="$(tmux display-message -p -t "${TMUX_PANE:-}" '#{window_id}' 2>/dev/null)" || window=w0
last_file="/tmp/.vibe-preview-last-${window#@}"
now="$(date +%s)"
if [ -f "$last_file" ]; then
  read -r last_path last_time <"$last_file" || true
  if [ "$last_path" = "$path" ] && [ $((now - ${last_time:-0})) -lt 30 ]; then
    exit 0
  fi
fi
printf '%s %s\n' "$path" "$now" >"$last_file"

# One preview at a time: replace any pane a previous invocation left open.
tmux list-panes -F '#{pane_id} #{pane_title}' 2>/dev/null |
  awk '$2 == "vibe-preview" {print $1}' |
  while read -r pane; do tmux kill-pane -t "$pane" 2>/dev/null; done

# The split opens detached (-d) and never touches focus: the image is
# ordinary pane content (tmux composites native sixel — see show-image.sh),
# so it renders correctly and survives repaints no matter which pane is
# active or how busy the TUI is. The pane closes itself after
# VIBE_PREVIEW_SECONDS.
# The pane titles itself (OSC 2) rather than via `select-pane -T`, which can
# change the active pane.
tmux split-window -d -v -l '35%' \
  "printf '\\033]2;vibe-preview\\033\\\\'; bash '$script_dir/show-image.sh' '$path'; sleep ${VIBE_PREVIEW_SECONDS:-15}" 2>/dev/null || exit 0
exit 0

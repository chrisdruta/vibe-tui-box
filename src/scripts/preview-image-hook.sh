#!/usr/bin/env bash
#
# Claude Code hook: feed images into the review window. Wired (via
# templates/claude-settings.json) to two events:
#   UserPromptSubmit — an image path pasted into the prompt (e.g. from
#     `vibe clip`) queues into the viewer the moment you submit;
#   PostToolUse (matcher: Read) — whenever the agent reads an image file,
#     you can see what it sees.
# The TUI itself can't render images (upstream: not planned). So: ensure the
# dedicated "preview" tmux window exists (detached, never steals focus)
# running yazi, then tell that yazi to reveal the path over DDS
# (`ya emit-to <id> reveal PATH`) — it renders when its window is active,
# otherwise the window name lights up in the status bar.
#
# Hook contract: JSON on stdin; stdout must stay EMPTY (UserPromptSubmit
# stdout is injected into the model's context). Always exit 0 — a preview
# failure must never block the agent.
set -uo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

# Extension list: read VIBE_IMAGE_EXTS off its canonical home in
# preview-lib.sh WITHOUT sourcing — this hook's stdout must stay empty
# (UserPromptSubmit stdout is injected into model context), so no lib code
# may ever run here. Fallback keeps the hook alive if the assignment moves.
img_exts="$(sed -n 's/^VIBE_IMAGE_EXTS="\([^"]*\)".*/\1/p' "$script_dir/preview-lib.sh" 2>/dev/null | head -1)"
[ -n "$img_exts" ] || img_exts="png jpg jpeg gif bmp webp avif"
img_alt="$(printf '%s' "$img_exts" | tr ' ' '|')" # png|jpg|jpeg|...

payload="$(cat)"
[ -n "${TMUX:-}" ] || exit 0
command -v jq >/dev/null 2>&1 || exit 0
# No yazi/ya yet = image predates this pin (rebuild pending): stay a cheap
# no-op instead of spinning up a doomed window and burning retry sleeps on
# every image event.
command -v ya >/dev/null 2>&1 || exit 0

event="$(jq -r '.hook_event_name // empty' <<<"$payload")"
case "$event" in
  UserPromptSubmit)
    # First absolute image path mentioned in the prompt (extensions from
    # $img_alt above). Bare paths with spaces can't be delimited, but quoted
    # ones ("…" or '…') are caught below.
    prompt="$(jq -r '.prompt // empty' <<<"$payload")"
    path="$(grep -oiE '/[^[:space:]"'"'"']+\.('"$img_alt"')' <<<"$prompt" | head -1)"
    if [ -z "$path" ]; then # double-quoted path (spaces survive)
      path="$(grep -oiE '"/[^"]+\.('"$img_alt"')"' <<<"$prompt" | head -1)"
      path="${path%\"}"
      path="${path#\"}"
    fi
    if [ -z "$path" ]; then # single-quoted path
      path="$(grep -oiE "'/[^']+\.($img_alt)'" <<<"$prompt" | head -1)"
      path="${path%\'}"
      path="${path#\'}"
    fi
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
    grep -qiE '\.('"$img_alt"')$' <<<"$path" || exit 0
    ;;
  *)
    exit 0
    ;;
esac
[ -n "$path" ] && [ -f "$path" ] || exit 0

session="$(tmux display-message -p -t "${TMUX_PANE:-}" '#{session_id}' 2>/dev/null)" || exit 0
[ -n "$session" ] || exit 0
created="$(bash "$script_dir/review.sh" --ensure "$session" 2>/dev/null)" || exit 0
id="$(bash "$script_dir/review.sh" --client-id "$session" 2>/dev/null)" || exit 0
[ -n "$id" ] || exit 0

# Give a freshly created window's yazi a beat to open its DDS socket; emit-to
# fails (exit 1) while nobody listens, so retry briefly. Duplicate events
# (prompt-paste then the agent's Read of the same file) need no debounce:
# revealing the same path twice just re-selects it.
[ "$created" = "created" ] && sleep 0.5
for _ in 1 2 3 4; do
  ya emit-to "$id" reveal "$path" >/dev/null 2>&1 && exit 0
  sleep 0.5
done
# The hook contract forbids stdout/stderr noise, so leave the only breadcrumb
# where the preview stack already logs (vibe show --diag points people here).
echo "$(date -u +%FT%TZ) hook: reveal gave up (id=$id): $path" \
  >>"${XDG_RUNTIME_DIR:-/tmp}/.vibe-preview-debug.log" 2>/dev/null || true
exit 0

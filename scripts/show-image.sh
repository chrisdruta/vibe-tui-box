#!/usr/bin/env bash
#
# Preview an image in the terminal with chafa (sixel where the terminal
# supports it, unicode blocks otherwise). The companion to `vibe clip`: with no
# argument it shows the newest /tmp/clip-*.png so you can eyeball what an agent
# is about to see — the Claude Code TUI itself can't render images inline.
#
# Runs container-side, either directly in a pane or via `vibe show` from the
# host (devcontainer exec).
set -euo pipefail

path="${1:-}"
if [ -z "$path" ]; then
  # Newest clip by mtime; clip filenames sort by timestamp too, but mtime also
  # covers files copied in by other means.
  path="$(find /tmp -maxdepth 1 -name 'clip-*.png' -printf '%T@ %p\n' 2>/dev/null |
    sort -rn | head -1 | cut -d' ' -f2-)"
  if [ -z "$path" ]; then
    echo "No clipped images in /tmp — run \`vibe clip\` on the host first, or pass a path." >&2
    exit 1
  fi
fi
if [ ! -f "$path" ]; then
  echo "Not a readable image file: $path" >&2
  exit 1
fi

echo "$path"
if [ -n "${TMUX:-}" ]; then
  # Inside tmux force sixel in a passthrough envelope: tmux's own sixel image
  # engine ingests the image but fails to re-emit it to the client (seen with
  # Windows Terminal — raw sixel prints as text soup), while passthrough
  # (allow-passthrough on, set in config/tmux.conf) hands the bytes straight
  # to the outer terminal and renders.
  exec chafa -f sixel --passthrough tmux "$path"
fi
if [ -t 1 ]; then
  exec chafa "$path"
fi
# Not a tty (e.g. `devcontainer exec` without a pty): chafa can't probe the
# terminal, so force sixel at a fixed size for the host terminal to render.
exec chafa -f sixel -s 100x "$path"

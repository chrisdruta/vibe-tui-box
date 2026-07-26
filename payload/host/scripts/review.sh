#!/usr/bin/env bash
# The vibe review walk — ONE definition serving both doors: prefix+G
# and the palette's "review diff" item (docs/tui-layout.md "Editor
# surfaces"). Gates on a non-empty worktree diff, then walks every
# changed file in plugin-free nvim vimdiff (git difftool). Same
# run-shell detour as popup.sh: the caller expands client and session
# path; display-popup receives plain text. Users who want a different
# walker rebind prefix+G in ~/.config/vibe/tui.conf — no knob.
#
# Usage: review.sh CLIENT SESSION_PATH
#
# Host-side: bash-3.2-safe (stock macOS). Runs under the vibe server
# via run-shell, so plain `tmux` is the right binary/socket.
set -euo pipefail

client="${1:?client}"
path="${2:?session path}"

# diff --quiet: 0 clean, 1 changes, >1 not a repo / git trouble — only
# a real 1 earns the popup (a flash-and-die difftool popup explains
# nothing; the message does).
rc=0
git -C "$path" diff --quiet 2>/dev/null || rc=$?
if [ "$rc" -eq 0 ]; then
  exec tmux display-message -c "$client" "review: no changes"
elif [ "$rc" -ne 1 ]; then
  exec tmux display-message -c "$client" "review: not a git repository"
fi

# The popup's cwd carries the repo; the command string stays constant
# (no interpolation — the path never enters a shell word).
exec tmux display-popup -c "$client" -d "$path" -w 85% -h 85% -E \
  "git difftool --tool=nvimdiff -y"

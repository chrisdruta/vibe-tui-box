#!/usr/bin/env bash
# win.sh — open a new window in a session's workspace, optionally
# running an engine verb: the ONE definition behind every
# `new-window -c <session_path>` door (palette.sh, chooser.sh).
# Menu command strings pass only the server-controlled session id and
# charset-vetted words; the session path and engine binary are fetched
# here from tmux, never interpolated into a tmux shell string — a path
# with an apostrophe must not ride a binding (clip-to-pane.sh's rule).
#
#   win.sh TARGET                  host shell window
#   win.sh TARGET NAME VERB...     window named NAME running `vibe VERB...`
#
# The engine command still becomes new-window's /bin/sh string, but
# it is built HERE with real single-quote escaping and handed to tmux
# as one argv word — no tmux double-quote lexer to degrade it.
#
# Host-side: bash-3.2-safe (stock macOS). Runs under the vibe server
# via run-shell.
set -euo pipefail

target="${1:?target session}"
name="${2:-}"

path="$(tmux display-message -p -t "$target" '#{session_path}' 2>/dev/null || true)"
[ -n "$path" ] || exit 0

if [ -z "$name" ]; then
  exec tmux new-window -t "$target" -c "$path"
fi
shift 2

exe="$(tmux show-options -gqv @vibe_exe 2>/dev/null || true)"
[ -n "$exe" ] && [ -x "$exe" ] || exit 0

cmd="'${exe//\'/\'\\\'\'}'"
for a in "$@"; do
  cmd="$cmd '${a//\'/\'\\\'\'}'"
done
exec tmux new-window -t "$target" -c "$path" -n "$name" "$cmd"

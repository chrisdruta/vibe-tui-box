#!/usr/bin/env bash
# The vibe engine popup — ONE definition of the "engine verb in a
# popup" chrome, and the required detour to reach it: display-popup
# does NOT format-expand its shell-command (the tray's request cell
# shipped a literal '#{@vibe_exe}' into bash), while run-shell
# documentedly does. So every binding routes run-shell → this script
# with the client and engine path already expanded, and display-popup
# receives plain text. The explicit client matters for the same reason
# as palette.sh: a run-shell job has no current client.
#
# Usage: popup.sh CLIENT EXE VERB [ARGS…]
set -euo pipefail

client="${1:?client}"
exe="${2:?engine path}"
shift 2
[ $# -gt 0 ] || { echo "popup.sh: no engine verb given" >&2; exit 2; }

# Argv → one /bin/sh string, each word single-quoted so a store path
# (or anything else) with spaces or quotes cannot break out.
cmd=""
for word in "$exe" "$@"; do
  cmd="$cmd '${word//\'/\'\\\'\'}'"
done

exec tmux display-popup -c "$client" -w 85% -h 70% -E \
  "$cmd; printf '\\n[enter to close] '; read _"

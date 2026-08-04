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
# Usage: popup.sh [-w WIDTH] [-h HEIGHT] CLIENT EXE VERB [ARGS…]
set -euo pipefail

# Two sizes, one convention (2026-08-04): the 85%x70% default for
# reading surfaces (requests, ps, egress, doctor), and callers pass
# -w 70% -h 45% for short confirmations (stop, park). Review popups
# (review.sh) own their full-bleed 90%x90% separately.
width="85%" height="70%"
while [ $# -gt 0 ]; do
  case "$1" in
  -w) width="${2:?-w value}"; shift 2 ;;
  -h) height="${2:?-h value}"; shift 2 ;;
  *) break ;;
  esac
done

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

exec tmux display-popup -c "$client" -w "$width" -h "$height" -E \
  "$cmd; printf '\\n[enter to close] '; read _"

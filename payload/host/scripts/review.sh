#!/usr/bin/env bash
# The vibe editor-popup router — ONE definition behind prefix+f/g/G
# and the palette's files/git/review items (docs/tui-layout.md
# "Editor surfaces"). Routes into the CONTAINER's image-baked review
# stack over `vibe exec` (a cold host needs nothing installed),
# through the same run-shell detour as popup.sh: display-popup does
# not format-expand its shell-command, so the caller expands
# client/exe/path here and the popup receives plain text. The popup's
# -d puts the shell in the workspace so the engine resolves the right
# project.
#
# Usage: review.sh CLIENT EXE SESSION_PATH files|review|git
set -euo pipefail

client="${1:?client}"
exe="${2:?engine path}"
path="${3:?session path}"
mode="${4:?mode}"
case "$mode" in
files | review | git) ;;
*) echo "review.sh: unknown mode '$mode'" >&2; exit 2 ;;
esac

# Single-quote the engine path for the popup's /bin/sh string —
# popup.sh's quoting rule.
q="'${exe//\'/\'\\\'\'}'"

# A dead/stopped container makes `vibe exec` fail fast; hold the popup
# open long enough to read why instead of flashing away.
exec tmux display-popup -c "$client" -d "$path" -w 90% -h 90% -E \
  "$q exec -- bash /vibe/payload/container/edit.sh $mode || { printf '\\n[vibe] container unavailable? vibe up starts it · enter to close '; read -r _; }"

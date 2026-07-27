#!/usr/bin/env bash
# The vibe editor-popup router — ONE definition behind prefix+f/g and
# the palette's files/git items (docs/tui-layout.md "Editor
# surfaces"). Routes into the CONTAINER's image-baked review stack
# over `vibe exec` (a cold host needs nothing installed), through the
# same run-shell detour as popup.sh: display-popup does not
# format-expand its shell-command, so the caller expands
# client/exe/path here and the popup receives plain text. The popup's
# -d puts the shell in the workspace so the engine resolves the right
# project. (A `review` mode — diffview — lived here for one day;
# dogfood retired it in lazygit's favor, 2026-07-26.)
#
# Usage: review.sh CLIENT EXE SESSION_PATH files|git
#        review.sh CLIENT EXE SESSION_PATH file PATH [LINE]
#
# `file` is the ctrl+click door (open-path.sh): one container path,
# already resolved and vetted by the dispatcher, plus an optional
# line number — same popup chrome, same edit.sh exit.
set -euo pipefail

client="${1:?client}"
exe="${2:?engine path}"
path="${3:?session path}"
mode="${4:?mode}"
fpath=""
flineno=""
case "$mode" in
files | git) ;;
file)
  fpath="${5:?file path}"
  flineno="${6:-}"
  case "$flineno" in *[!0-9]*) flineno="" ;; esac
  ;;
*) echo "review.sh: unknown mode '$mode'" >&2; exit 2 ;;
esac

# Single-quote the engine path for the popup's /bin/sh string —
# popup.sh's quoting rule.
q="'${exe//\'/\'\\\'\'}'"

# The border title is the helper text — the always-visible "how do I
# leave" answer (q quits nvim from anywhere; lazygit's own q quits).
title=" files · enter open · - close file/back · q quit · Space keys "
[ "$mode" = git ] && title=" git · q quit · ? keys "
[ "$mode" = file ] && title=" ${fpath##*/}${flineno:+:$flineno} · q quit · Space keys "

# The file-mode extras travel the same single-quote escape as the
# engine path — popup.sh's quoting rule.
extra=""
if [ "$mode" = file ]; then
  extra=" '${fpath//\'/\'\\\'\'}' '$flineno'"
fi

# A dead/stopped container makes `vibe exec` fail fast; hold the popup
# open long enough to read why instead of flashing away.
exec tmux display-popup -c "$client" -d "$path" -T "$title" -w 90% -h 90% -E \
  "$q exec -- bash /vibe/payload/container/edit.sh $mode$extra || { printf '\\n[vibe] container unavailable? vibe up starts it · enter to close '; read -r _; }"

#!/usr/bin/env bash
#
# vibe tui host dock toggle — the bottom host-shell pane as an IDE-style
# panel (VS Code ctrl+` feel). Collapse shrinks the @vibe_role=host pane
# of the given window to a single row, so its top border + "host" title
# remain as a slim chrome bar across the bottom; toggle again restores
# the previous height. Pure resize — the shell and its state are never
# killed or respawned. A window without a host pane gets one grown on
# first toggle (v1's tui.sh pre-built it; the v2 engine builds only the
# agent window, so the dock owns its own creation).
#
# Invoked by the conf's prefix+t / palette t: dock.sh WINDOW_ID
#
# Host-side: bash-3.2-safe (stock macOS). Runs under the vibe server via
# run-shell, so plain `tmux` is the right binary/socket.
set -u

win="${1:-}"
[ -n "$win" ] || exit 0
tab="$(printf '\t')"

pane=""
h=0
while IFS="$tab" read -r id role height; do
  if [ "$role" = "host" ]; then
    pane="$id"
    h="$height"
    break
  fi
done <<EOF
$(tmux list-panes -t "$win" -F "#{pane_id}$tab#{@vibe_role}$tab#{pane_height}" 2>/dev/null)
EOF
if [ -z "$pane" ]; then
  # First toggle in this window: grow the dock at the layout default,
  # stamp its role/title, and keep focus where the user was. The path
  # comes from tmux directly, never interpolated into a shell string.
  sp="$(tmux display-message -p -t "$win" '#{session_path}' 2>/dev/null)"
  pane="$(tmux split-window -v -l '30%' -t "$win" -c "${sp:-$HOME}" -P -F '#{pane_id}' 2>/dev/null)" || exit 0
  [ -n "$pane" ] || exit 0
  tmux set-option -p -t "$pane" @vibe_role "host" \; \
    set-option -p -t "$pane" @vibe_title "host" \; \
    select-pane -l 2>/dev/null
  exit 0
fi

if [ "$h" -gt 2 ]; then
  # collapse: remember the height so expand restores exactly this shape
  tmux set-option -p -t "$pane" @vibe_dock_h "$h" \; resize-pane -t "$pane" -y 1
else
  prev="$(tmux show-options -pqv -t "$pane" @vibe_dock_h 2>/dev/null)"
  case "$prev" in
    # Never expanded before (or junk): the layout default — keep in sync
    # with tui.sh's initial split-window -l '30%' for the host pane.
    '' | *[!0-9]*) prev="30%" ;;
  esac
  tmux resize-pane -t "$pane" -y "$prev"
fi
exit 0

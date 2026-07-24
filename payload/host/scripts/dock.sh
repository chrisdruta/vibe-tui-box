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
# Modes (WINDOW_ID always last):
#   toggle (default)  prefix+t / palette t — collapse/expand, growing a
#                     missing dock at the 30% layout default
#   ensure            the conf's session-created hook — create the dock
#                     collapsed if the window has none; never touch an
#                     existing one
#   fit               the conf's window-resized hook — window resizes
#                     stretch panes PROPORTIONALLY, which inflates the
#                     collapsed 1-row bar (detached sessions are born
#                     80x24, then the first attach balloons it); snap a
#                     pane marked @vibe_dock_min back to one row. Same
#                     rule as the sidebar's fit.
#
# Collapsed state is explicit (@vibe_dock_min=1, cleared on expand) so
# fit never fights a height the user chose.
#
# Host-side: bash-3.2-safe (stock macOS). Runs under the vibe server via
# run-shell, so plain `tmux` is the right binary/socket.
set -u

mode="toggle"
case "${1:-}" in
ensure | fit)
  mode="$1"
  shift
  ;;
esac
win="${1:-}"
[ -n "$win" ] || exit 0
tab="$(printf '\t')"

dock_size() { # expanded height knob: @vibe_dock_size (conf), rows or NN%
  s="$(tmux show-options -gqv @vibe_dock_size 2>/dev/null)"
  case "$s" in
    [0-9] | [0-9][0-9] | [0-9][0-9][0-9]) ;;
    [0-9]% | [0-9][0-9]% | 100%) ;;
    *) s="30%" ;;
  esac
  printf '%s' "$s"
}

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
  [ "$mode" = "fit" ] && exit 0
  # No dock in this window yet: grow one, stamp its role/title, and
  # never steal focus (-d). Toggle opens it at the layout default;
  # ensure (the session-created hook) parks it collapsed as the slim
  # chrome bar. The path comes from tmux directly, never interpolated
  # into a shell string.
  size="$(dock_size)"
  [ "$mode" = "ensure" ] && size=1
  sp="$(tmux display-message -p -t "$win" '#{session_path}' 2>/dev/null)"
  pane="$(tmux split-window -d -v -l "$size" -t "$win" -c "${sp:-$HOME}" -P -F '#{pane_id}' 2>/dev/null)" || exit 0
  [ -n "$pane" ] || exit 0
  tmux set-option -p -t "$pane" @vibe_role "host" \; \
    set-option -p -t "$pane" @vibe_title "host" 2>/dev/null
  [ "$mode" = "ensure" ] && tmux set-option -p -t "$pane" @vibe_dock_min 1 2>/dev/null
  exit 0
fi
case "$mode" in
ensure)
  exit 0
  ;;
fit)
  if [ "$(tmux show-options -pqv -t "$pane" @vibe_dock_min 2>/dev/null)" = "1" ] && [ "$h" -gt 1 ]; then
    tmux resize-pane -t "$pane" -y 1 2>/dev/null
  fi
  exit 0
  ;;
esac

if [ "$h" -gt 2 ]; then
  # collapse: remember the height so expand restores exactly this shape
  tmux set-option -p -t "$pane" @vibe_dock_h "$h" \; \
    set-option -p -t "$pane" @vibe_dock_min 1 \; \
    resize-pane -t "$pane" -y 1
else
  prev="$(tmux show-options -pqv -t "$pane" @vibe_dock_h 2>/dev/null)"
  case "$prev" in
    # Never expanded before (or junk): the @vibe_dock_size knob, the
    # same default the toggle-create split-window above opens with.
    '' | *[!0-9]*) prev="$(dock_size)" ;;
  esac
  tmux set-option -p -t "$pane" @vibe_dock_min 0 \; \
    resize-pane -t "$pane" -y "$prev"
fi
exit 0

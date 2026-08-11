#!/usr/bin/env bash
#
# vibe tui dock toggle — the bottom shell pane as an IDE-style panel
# (VS Code ctrl+` feel). The dock owns the FULL window bottom
# (2026-07-31, Chris — created -f, so the sidebar sits above it): the
# collapsed strip runs edge to edge and reads as one chrome band with
# the tray below, instead of a right-column stub T-junctioning into the
# sidebar border. Collapse shrinks the @vibe_role=host pane
# of the given window to a single row, so its top border + title
# remain as a slim chrome bar across the bottom; toggle again restores
# the previous height. Pure resize — the shell and its state are never
# killed or respawned. A window without a dock gets one grown on
# first toggle (v1's tui.sh pre-built it; the v2 engine builds only the
# agent window, so the dock owns its own creation).
#
# The dock holds a CONTAINER shell by default (2026-08-10, Chris — the
# dock's actual job is launching a dev server and watching/stopping
# it, which lives in the container; the host shell was the default
# only by construction order) and flips to the host shell and back on
# the border sub-tabs: the title reads `[container] · host`, a clean
# click on the border row (MouseUp1Border — drags still resize) or
# prefix+T swaps shells. BOTH shells stay alive across flips: the
# inactive one parks as a window in the detached `dockshelf` session
# (one shelf window per origin window, named w<N> for origin @<N> —
# the name dodges tmux's `@` window-id target grammar), swapped in and
# out with swap-pane so a running dev server survives a visit to the
# host side. The shelf session wears @vibe_shelf=1 and every surface
# skips it: the sidebar's session porcelain renders it empty, ensure
# hooks exit early, and reap.sh never matches its name (no `vibe-`
# prefix). NOTE: @vibe_role stays "host" for the dock pane whichever
# shell it shows — the role names the dock SLOT (every conf gate keys
# on it), not the shell inside; @vibe_dock_shell carries that.
#
# When the engine is absent (bare tui, no @vibe_exe) the dock degrades
# to the host shell alone and the flip refuses quietly. A container
# shell against a down container dies into the pane-died vocabulary —
# the corpse holds the error and respawn (r / right-click) retries.
#
# Modes (WINDOW_ID always after the mode):
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
#   flip              prefix+T / palette T — swap container/host shell
#   flipborder PANE   MouseUp1Border — flip only when PANE (the event's
#                     #{mouse_pane}: the pane ABOVE the clicked border;
#                     border events carry no coordinates on 3.7b) sits
#                     directly on the dock
#   reapshelf         the conf's window-unlinked hook — drop shelf
#                     windows whose origin window is gone
#
# Collapsed state is explicit (@vibe_dock_min=1, cleared on expand) so
# fit never fights a height the user chose.
#
# Host-side: bash-3.2-safe (stock macOS). Runs under the vibe server via
# run-shell, so plain `tmux` is the right binary/socket.
set -u

shelf="dockshelf"

mode="toggle"
case "${1:-}" in
ensure | fit | flip | flipborder | reapshelf)
  mode="$1"
  shift
  ;;
esac
win="${1:-}"
mousep="${2:-}"
tab="$(printf '\t')"

if [ "$mode" = "reapshelf" ]; then
  tmux has-session -t "=$shelf" 2>/dev/null || exit 0
  live=""
  while IFS="$tab" read -r sname wid; do
    [ "$sname" = "$shelf" ] && continue
    live="$live $wid"
  done <<EOF
$(tmux list-windows -a -F "#{session_name}$tab#{window_id}" 2>/dev/null)
EOF
  for w in $(tmux list-windows -t "=$shelf" -F '#{window_name}' 2>/dev/null); do
    case "$w" in w[0-9]*) ;; *) continue ;; esac
    case " $live " in
      *" @${w#w} "*) ;;
      *) tmux kill-window -t "=$shelf:$w" 2>/dev/null ;;
    esac
  done
  exit 0
fi

[ -n "$win" ] || exit 0
# Never operate inside the shelf itself (its windows are parked dock
# panes; the session-created/resize hooks fire for it like any other).
[ "$(tmux display-message -p -t "$win" '#{@vibe_shelf}' 2>/dev/null)" = "1" ] && exit 0

dock_size() { # expanded height knob: @vibe_dock_size (conf), rows or NN%
  s="$(tmux show-options -gqv @vibe_dock_size 2>/dev/null)"
  case "$s" in
    [0-9] | [0-9][0-9] | [0-9][0-9][0-9]) ;;
    [0-9]% | [0-9][0-9]% | 100%) ;;
    *) s="30%" ;;
  esac
  printf '%s' "$s"
}

engine_cmd() { # /bin/sh string for the container shell; fails without an engine
  exe="$(tmux show-options -gqv @vibe_exe 2>/dev/null)"
  [ -n "$exe" ] && [ -x "$exe" ] || return 1
  printf "'%s' shell" "${exe//\'/\'\\\'\'}"
}

title_for() { # the border sub-tabs: brackets mark the shell in the dock
  if [ "$1" = "container" ]; then
    printf '[container] · host'
  else
    printf 'container · [host]'
  fi
}

pane=""
h=0
top=0
while IFS="$tab" read -r id role height ptop; do
  if [ "$role" = "host" ]; then
    pane="$id"
    h="$height"
    top="$ptop"
    break
  fi
done <<EOF
$(tmux list-panes -t "$win" -F "#{pane_id}$tab#{@vibe_role}$tab#{pane_height}$tab#{pane_top}" 2>/dev/null)
EOF
if [ -z "$pane" ]; then
  case "$mode" in fit | flip | flipborder) exit 0 ;; esac
  # No dock in this window yet: grow one, stamp its role/title, and
  # never steal focus (-d). Toggle opens it at the layout default;
  # ensure (the session-created hook) parks it collapsed as the slim
  # chrome bar. The path comes from tmux directly, never interpolated
  # into a shell string.
  size="$(dock_size)"
  [ "$mode" = "ensure" ] && size=1
  sp="$(tmux display-message -p -t "$win" '#{session_path}' 2>/dev/null)"
  if cmd="$(engine_cmd)"; then
    shell="container"
    pane="$(tmux split-window -d -f -v -l "$size" -t "$win" -c "${sp:-$HOME}" -P -F '#{pane_id}' "$cmd" 2>/dev/null)" || exit 0
  else
    shell="host"
    pane="$(tmux split-window -d -f -v -l "$size" -t "$win" -c "${sp:-$HOME}" -P -F '#{pane_id}' 2>/dev/null)" || exit 0
  fi
  [ -n "$pane" ] || exit 0
  tmux set-option -p -t "$pane" @vibe_role "host" \; \
    set-option -p -t "$pane" @vibe_title "$(title_for "$shell")" \; \
    set-option -p -t "$pane" @vibe_dock_shell "$shell" 2>/dev/null
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
flipborder)
  # Only the dock's own top border flips: the event's mouse pane is
  # the pane above the clicked border, and it must sit DIRECTLY on the
  # dock (its bottom + the border row = the dock's top). Borders
  # between stacked panes higher up stay inert; the sidebar rule's
  # clean click resolves to the sidebar (also directly on the dock)
  # and flips too — the recorded trade, see the conf's bind comment.
  [ -n "$mousep" ] || exit 0
  mb="$(tmux display-message -p -t "$mousep" '#{pane_bottom}' 2>/dev/null)"
  case "$mb" in '' | *[!0-9]*) exit 0 ;; esac
  [ "$((mb + 2))" -eq "$top" ] || exit 0
  mode="flip"
  ;;
esac

if [ "$mode" = "flip" ]; then
  cur="$(tmux show-options -pqv -t "$pane" @vibe_dock_shell 2>/dev/null)"
  [ -n "$cur" ] || cur="host" # pre-flip docks were host shells
  want="container"
  [ "$cur" = "container" ] && want="host"
  key="w${win#@}"
  other="$(tmux list-panes -t "=$shelf:$key" -F '#{pane_id}' 2>/dev/null | head -n 1)"
  if [ -z "$other" ]; then
    # Only CREATING the other shell needs the engine (a parked
    # container shell swaps back engine-free).
    cmd=""
    if [ "$want" = "container" ]; then
      cmd="$(engine_cmd)" || exit 0
    fi
    sp="$(tmux display-message -p -t "$win" '#{session_path}' 2>/dev/null)"
    if tmux has-session -t "=$shelf" 2>/dev/null; then
      if [ -n "$cmd" ]; then
        other="$(tmux new-window -d -t "=$shelf" -n "$key" -c "${sp:-$HOME}" -P -F '#{pane_id}' "$cmd" 2>/dev/null)"
      else
        other="$(tmux new-window -d -t "=$shelf" -n "$key" -c "${sp:-$HOME}" -P -F '#{pane_id}' 2>/dev/null)"
      fi
    else
      # Born detached at a real size so the parked shell never reflows
      # through an 80x24 default on its way back.
      if [ -n "$cmd" ]; then
        other="$(tmux new-session -d -s "$shelf" -n "$key" -c "${sp:-$HOME}" -x 200 -y 50 -P -F '#{pane_id}' "$cmd" 2>/dev/null)"
      else
        other="$(tmux new-session -d -s "$shelf" -n "$key" -c "${sp:-$HOME}" -x 200 -y 50 -P -F '#{pane_id}' 2>/dev/null)"
      fi
      tmux set-option -t "=$shelf" @vibe_shelf 1 2>/dev/null
    fi
  fi
  [ -n "$other" ] || exit 0
  # Pane options ride their panes through swap-pane, so the dock-slot
  # markers are carried over to the incoming pane by hand and stripped
  # from the parked one.
  minv="$(tmux show-options -pqv -t "$pane" @vibe_dock_min 2>/dev/null)"
  hv="$(tmux show-options -pqv -t "$pane" @vibe_dock_h 2>/dev/null)"
  tmux swap-pane -d -s "$pane" -t "$other" 2>/dev/null || exit 0
  tmux set-option -p -t "$other" @vibe_role "host" \; \
    set-option -p -t "$other" @vibe_title "$(title_for "$want")" \; \
    set-option -p -t "$other" @vibe_dock_shell "$want" 2>/dev/null
  [ -n "$minv" ] && tmux set-option -p -t "$other" @vibe_dock_min "$minv" 2>/dev/null
  [ -n "$hv" ] && tmux set-option -p -t "$other" @vibe_dock_h "$hv" 2>/dev/null
  tmux set-option -p -t "$pane" @vibe_role "shelf" \; \
    set-option -pu -t "$pane" @vibe_dock_min \; \
    set-option -p -t "$pane" @vibe_dock_shell "$cur" 2>/dev/null
  exit 0
fi

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

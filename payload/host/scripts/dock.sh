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
# only by construction order) and flips to the host shell and back
# with prefix+T, the palette, or the title row's right-click menu.
# The title row is the dock's ONE button (2026-08-14, Chris — border
# events carry no coordinates on the pinned 3.7b, so `[container] ·
# host` can never be two targets; show/hide is the frequent action
# and wins the plain click): a clean left click (MouseUp1Border —
# drags still resize) toggles collapse/expand, right-click
# (MouseUp3Border) opens the shell menu, and the leading ▸/▾ chevron
# in the title carries the collapsed state. Clicking the collapsed
# 1-row strip focuses it to type (the old auto-expand stole exactly
# that click); the superseded ▤ tray cell is gone with it.
# BOTH shells stay alive across flips: the
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
#   flipto SHELL      the title menu's shell items — flip only when the
#                     dock shows the OTHER shell (a stale menu pick
#                     must not double-flip)
#   toggleborder PANE MouseUp1Border — toggle only when PANE (the
#                     event's #{mouse_pane}: the pane ABOVE the clicked
#                     border; border events carry no coordinates on
#                     3.7b) sits directly on the dock
#   menuborder PANE CLIENT
#                     MouseUp3Border — the title row's shell menu:
#                     container / host / collapse-or-expand
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
ensure | fit | flip | flipto | toggleborder | menuborder | reapshelf)
  mode="$1"
  shift
  ;;
esac
win="${1:-}"
arg2="${2:-}" # toggleborder/menuborder: the event's mouse pane; flipto: the shell
arg3="${3:-}" # menuborder: the client
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
# The NAME check matters: session-created fires before flip can stamp
# @vibe_shelf on the fresh session, and that race once grew a dock
# inside the shelf.
case "$(tmux display-message -p -t "$win" "#{session_name}$tab#{@vibe_shelf}" 2>/dev/null)" in
"$shelf$tab"* | *"${tab}1") exit 0 ;;
esac

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

title_for() { # $1 shell, $2 collapsed(1)/expanded(0): brackets mark the
  # shell in the dock, the `shell:` label names the surface like the
  # sidebar's `projects`, and the chevron carries the panel state — the
  # tell that the title row is the toggle button
  chev="▾"
  [ "${2:-0}" = "1" ] && chev="▸"
  if [ "$1" = "container" ]; then
    printf '%s shell: [container] · host' "$chev"
  else
    printf '%s shell: container · [host]' "$chev"
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
  case "$mode" in fit | flip | flipto | toggleborder | menuborder) exit 0 ;; esac
  # No dock in this window yet: grow one, stamp its role/title, and
  # never steal focus (-d). Toggle opens it at the layout default;
  # ensure (the session-created hook) parks it collapsed as the slim
  # chrome bar. The path comes from tmux directly, never interpolated
  # into a shell string.
  size="$(dock_size)"
  min=0
  if [ "$mode" = "ensure" ]; then
    size=1
    min=1
  fi
  sp="$(tmux display-message -p -t "$win" '#{session_path}' 2>/dev/null)"
  if cmd="$(engine_cmd)"; then
    shell="container"
    pane="$(tmux split-window -d -f -v -l "$size" -t "$win" -c "${sp:-$HOME}" -P -F '#{pane_id}' "$cmd" 2>/dev/null)" || exit 0
  else
    shell="host"
    pane="$(tmux split-window -d -f -v -l "$size" -t "$win" -c "${sp:-$HOME}" -P -F '#{pane_id}' 2>/dev/null)" || exit 0
  fi
  [ -n "$pane" ] || exit 0
  # window-style: the dock body sits on the raised @thm_surface — the
  # lifted canvas IS the inset look (2026-08-13; corner caps were
  # tried and reverted next day — the conf's pane-border-format
  # comment has the record — and real side borders would cost content
  # columns).
  tmux set-option -p -t "$pane" @vibe_role "host" \; \
    set-option -p -t "$pane" @vibe_title "$(title_for "$shell" "$min")" \; \
    set-option -p -t "$pane" @vibe_dock_shell "$shell" \; \
    set-option -p -t "$pane" window-style "bg=#{@thm_surface}" 2>/dev/null
  [ "$min" = "1" ] && tmux set-option -p -t "$pane" @vibe_dock_min 1 2>/dev/null
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
toggleborder)
  # Only the dock's own title row toggles: with pane-border-status
  # top, a title row belongs to ITS pane, so the event's mouse pane is
  # the dock itself exactly when the click landed on the dock's title
  # (measured on the pinned 3.7b — a bare division border resolves to
  # the pane above/left instead, so the sidebar rule and stacked-pane
  # borders stay inert for free).
  [ "$arg2" = "$pane" ] || exit 0
  mode="toggle"
  ;;
menuborder)
  # The title row's right-click: the flip's mouse door, as a menu —
  # border events cannot hit-test `[container] · host` into two
  # targets, so the shells become items instead. Anchored above the
  # tray like the other bottom-edge doors; -M -O and the item
  # commands' literal #{...} constructs per the tray-door lesson
  # (chooser.sh records the mechanism).
  [ "$arg2" = "$pane" ] || exit 0
  case "${win#@}" in '' | *[!0-9]*) exit 0 ;; esac
  cur="$(tmux show-options -pqv -t "$pane" @vibe_dock_shell 2>/dev/null)"
  [ -n "$cur" ] || cur="host"
  size_lbl="collapse"
  [ "$(tmux show-options -pqv -t "$pane" @vibe_dock_min 2>/dev/null)" = "1" ] && size_lbl="expand"
  # The toggle item LEADS: a disabled item wears a leading dash, and a
  # leading-dash FIRST item parses as a display-menu flag ("invalid
  # flag -[", headless rig 2026-08-14) — the dash-less toggle label
  # ends option scanning so the shell items are always positional.
  args=()
  [ -n "$arg3" ] && args+=(-c "$arg3")
  args+=(-M -O -y S -T " shell ")
  args+=("$size_lbl" t "run-shell -b \"bash '#{@vibe_payload_dir}/scripts/dock.sh' '$win'\"")
  args+=("")
  if [ "$cur" = "container" ]; then
    args+=("-[container]" '' '')
    args+=("host" h "run-shell -b \"bash '#{@vibe_payload_dir}/scripts/dock.sh' flipto '$win' host\"")
  else
    args+=("container" c "run-shell -b \"bash '#{@vibe_payload_dir}/scripts/dock.sh' flipto '$win' container\"")
    args+=("-[host]" '' '')
  fi
  exec tmux display-menu "${args[@]}"
  ;;
esac

if [ "$mode" = "flip" ] || [ "$mode" = "flipto" ]; then
  cur="$(tmux show-options -pqv -t "$pane" @vibe_dock_shell 2>/dev/null)"
  [ -n "$cur" ] || cur="host" # pre-flip docks were host shells
  want="container"
  [ "$cur" = "container" ] && want="host"
  if [ "$mode" = "flipto" ]; then
    # An explicit destination: a menu picked against a since-flipped
    # dock must not bounce it back.
    case "$arg2" in container | host) want="$arg2" ;; *) exit 0 ;; esac
    [ "$want" = "$cur" ] && exit 0
  fi
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
      # Plain name target: set-option alone REJECTS the `=` exact
      # prefix ("no such session: =dockshelf", measured on 3.7b) while
      # has-session/list-panes/new-window all accept it — the silent
      # 2>/dev/null here once ate that error and the unstamped shelf
      # rendered as a project block in the sidebar.
      tmux set-option -t "$shelf" @vibe_shelf 1 2>/dev/null
    fi
  fi
  [ -n "$other" ] || exit 0
  # Pane options ride their panes through swap-pane, so the dock-slot
  # markers are carried over to the incoming pane by hand and stripped
  # from the parked one.
  minv="$(tmux show-options -pqv -t "$pane" @vibe_dock_min 2>/dev/null)"
  hv="$(tmux show-options -pqv -t "$pane" @vibe_dock_h 2>/dev/null)"
  st=0
  [ "$minv" = "1" ] && st=1
  tmux swap-pane -d -s "$pane" -t "$other" 2>/dev/null || exit 0
  tmux set-option -p -t "$other" @vibe_role "host" \; \
    set-option -p -t "$other" @vibe_title "$(title_for "$want" "$st")" \; \
    set-option -p -t "$other" @vibe_dock_shell "$want" \; \
    set-option -p -t "$other" window-style "bg=#{@thm_surface}" 2>/dev/null
  [ -n "$minv" ] && tmux set-option -p -t "$other" @vibe_dock_min "$minv" 2>/dev/null
  [ -n "$hv" ] && tmux set-option -p -t "$other" @vibe_dock_h "$hv" 2>/dev/null
  tmux set-option -p -t "$pane" @vibe_role "shelf" \; \
    set-option -pu -t "$pane" @vibe_dock_min \; \
    set-option -p -t "$pane" @vibe_dock_shell "$cur" 2>/dev/null
  exit 0
fi

# The toggle restamps the title too — the chevron is state, and state
# lives where it changes.
cur="$(tmux show-options -pqv -t "$pane" @vibe_dock_shell 2>/dev/null)"
[ -n "$cur" ] || cur="host"
if [ "$h" -gt 2 ]; then
  # collapse: remember the height so expand restores exactly this shape
  tmux set-option -p -t "$pane" @vibe_dock_h "$h" \; \
    set-option -p -t "$pane" @vibe_dock_min 1 \; \
    set-option -p -t "$pane" @vibe_title "$(title_for "$cur" 1)" \; \
    resize-pane -t "$pane" -y 1
else
  prev="$(tmux show-options -pqv -t "$pane" @vibe_dock_h 2>/dev/null)"
  case "$prev" in
    # Never expanded before (or junk): the @vibe_dock_size knob, the
    # same default the toggle-create split-window above opens with.
    '' | *[!0-9]*) prev="$(dock_size)" ;;
  esac
  tmux set-option -p -t "$pane" @vibe_dock_min 0 \; \
    set-option -p -t "$pane" @vibe_title "$(title_for "$cur" 0)" \; \
    resize-pane -t "$pane" -y "$prev"
fi
exit 0

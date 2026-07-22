#!/usr/bin/env bash
#
# vibe tui project sidebar — the cross-project glance as a vertical pane
# on the far left (graduated from the 2026-07-22 spike; it REPLACES the
# old status-line-2 strip). Two sections:
#   top    — the fleet: every project session on the vibe socket with its
#            state dots (the same @vibe_glyph / @vibe_dot_fg / @vibe_attn
#            data state-render.sh maintains for the tabs), the workspace
#            name in bold (bright = the session this sidebar lives in),
#            and the checkout's git branch underneath; click = switch
#   bottom — the agent roster, AGGREGATE across all projects ("glyph
#            name · project", bottom-anchored): every stateful window on
#            the socket; click = jump to that project AND that window
#
# GLOBAL across the whole UI: @vibe_sidebar_on (conf defaults it to 1) is
# the one switch, and the conf's ensure hooks (after-new-window /
# after-select-window / client-session-changed) grow a sidebar into every
# window as it is created or visited. A tmux pane can only live in one
# window, so "one global sidebar" is really one-per-window kept in
# lockstep — same look everywhere, one toggle.
#
# Modes:
#   toggle WINDOW_ID   flip @vibe_sidebar_on: off kills every sidebar pane
#                      on the server; on stamps this window (the hooks
#                      cover the rest as they're visited)
#   ensure WINDOW_ID   idempotent: sidebar present in WINDOW iff flag on
#   render [--once]    the draw loop inside the pane (--once: one frame)
#   click PANE ROW [CLIENT]  switch CLIENT to the project drawn on ROW —
#                      the conf's MouseDown1Pane binding routes sidebar
#                      clicks here; ROW resolves via @vibe_sidebar_map,
#                      which render publishes each frame, so there is no
#                      second copy of the layout arithmetic to drift
#
# Refresh is a 2s poll INSIDE each sidebar pane, but an idle tick is ONE
# display-message round trip: a full redraw happens only when
# @vibe_state_serial moved (state-render.sh bumps it with every dot
# write; tui.sh bumps it on session build/heal) or on every 5th tick —
# the 10s forced frame covers what has no serial: the branch line,
# renames, session create/destroy. The status line stays event-driven.
# Why not events outright: tmux wait-for has a lost-signal race and no
# timeout, bash 3.2 has no `wait -n` (so a fallback poll must exist
# anyway), and signaling sidebars FROM state-render.sh would put
# list-panes + kills on the hot path to save work on the cold one.
#
# Host-side: bash-3.2-safe (stock macOS). Runs under the vibe server
# (run-shell provides TMUX for toggle/ensure; the pane's environment for
# render), so plain `tmux` is always the right binary/socket.
set -u

mode="${1:-render}"
tab="$(printf '\t')"
us="$(printf '\037')"

sidebar_panes() { # sidebar pane ids in window $1, oldest first
  tmux list-panes -t "$1" -F "#{pane_id}$tab#{@vibe_role}" 2>/dev/null |
    awk -F "$tab" '$2 == "sidebar" { print $1 }'
}

sidebar_w() { # the one width knob: @vibe_sidebar_w (conf), default 30
  w="$(tmux show-options -gqv @vibe_sidebar_w 2>/dev/null)"
  case "$w" in '' | *[!0-9]*) w=30 ;; esac
  printf '%s' "$w"
}

create_in() {
  win="$1"
  self="$(cd "$(dirname "$0")" && pwd)/$(basename "$0")"
  # Full-height split BEFORE the leftmost pane; input off so stray clicks
  # can't type into the render loop; focus returns to where the user was.
  pane="$(tmux split-window -fhb -l "$(sidebar_w)" -t "$win" -P -F '#{pane_id}' \
    "exec bash '$self' render")"
  # No pane-level @vibe_glyph shadow here: the border format role-gates
  # the dot instead. (The old empty-string shadow leaked further than the
  # border — window-format lookups resolve user options through the
  # ACTIVE pane, so a focused sidebar erased its window's dot everywhere.)
  tmux set-option -p -t "$pane" @vibe_role "sidebar" \; \
    set-option -p -t "$pane" @vibe_title "projects" \; \
    select-pane -d -t "$pane" \; \
    select-pane -l
}

ensure_in() {
  win="$1"
  found=""
  for p in $(sidebar_panes "$win"); do
    if [ -z "$found" ]; then
      found="$p"
    else
      # the ensure hooks run async (-b) and can race a double-create on a
      # fast window-hop; heal to exactly one
      tmux kill-pane -t "$p" 2>/dev/null
    fi
  done
  [ -n "$found" ] || create_in "$win"
}

case "$mode" in
toggle)
  win="${2:-}"
  [ -n "$win" ] || exit 0
  if [ "$(tmux show-options -gqv @vibe_sidebar_on)" = "1" ]; then
    tmux set-option -g @vibe_sidebar_on 0
    for p in $(tmux list-panes -a -F "#{pane_id}$tab#{@vibe_role}" 2>/dev/null |
      awk -F "$tab" '$2 == "sidebar" { print $1 }'); do
      tmux kill-pane -t "$p" 2>/dev/null
    done
  else
    tmux set-option -g @vibe_sidebar_on 1
    ensure_in "$win"
  fi
  exit 0
  ;;
ensure)
  win="${2:-}"
  [ -n "$win" ] || exit 0
  [ "$(tmux show-options -gqv @vibe_sidebar_on)" = "1" ] || exit 0
  ensure_in "$win"
  exit 0
  ;;
click)
  pane="${2:-}"
  y="${3:-}"
  client="${4:-}"
  { [ -n "$pane" ] && [ -n "$y" ]; } || exit 0
  sid=""
  for entry in $(tmux show-options -pqv -t "$pane" @vibe_sidebar_map 2>/dev/null); do
    case "$entry" in
      "$y":*) sid="${entry#*:}" && break ;;
    esac
  done
  [ -n "$sid" ] || exit 0 # gutter/blank row — not a target
  case "$sid" in
    *:@*)
      # agent roster row: "SESSION:WINDOW" — make that window current in
      # its session, then bring this client over
      win="${sid##*:}"
      sess="${sid%%:*}"
      tmux select-window -t "$win" 2>/dev/null
      sid="$sess"
      ;;
  esac
  if [ -n "$client" ]; then
    tmux switch-client -c "$client" -t "$sid" 2>/dev/null
  else
    tmux switch-client -t "$sid" 2>/dev/null
  fi
  exit 0
  ;;
fit)
  # Window resizes stretch panes PROPORTIONALLY, so a client visiting a
  # window born at another size balloons/squeezes the sidebar (detached
  # --detach sessions are born 80 cols wide; live report, 2026-07-22).
  # The conf's window-resized hook calls this to snap the sidebar back to
  # its fixed chrome width. Fires only on window-size changes — manual
  # border drags don't resize the window, so they are never fought.
  win="${2:-}"
  [ -n "$win" ] || exit 0
  want="$(sidebar_w)"
  for p in $(sidebar_panes "$win"); do
    cur="$(tmux display-message -p -t "$p" '#{pane_width}' 2>/dev/null)"
    [ "$cur" = "$want" ] || tmux resize-pane -t "$p" -x "$want" 2>/dev/null
  done
  exit 0
  ;;
render) ;;
*) exit 0 ;;
esac

# ── render ───────────────────────────────────────────────────────────────
once=0
[ "${2:-}" = "--once" ] && once=1

# One palette: theme.sh, beside the tmux conf (whose @thm block is its
# lockstep twin). Sourced once at launch — a palette change lands on the
# next toggle, which is fine.
case "$0" in */*) here="${0%/*}" ;; *) here="." ;; esac
# shellcheck source=../../config/theme.sh disable=SC1091
. "$here/../../config/theme.sh"
c_fg="$(vibe_fg "$VIBE_THM_FG")"
c_dim="$(vibe_fg "$VIBE_THM_DIM")"
c_coral="$(vibe_fg "$VIBE_THM_CORAL")"
bold="$(printf '\033[1m')"
reset="$(printf '\033[0m')"
eol="$(printf '\033[K')"

# .git/HEAD read directly — no git subprocess per tick. Handles the .git
# FILE indirection (worktrees, submodule checkouts); detached HEAD shows
# the short sha.
branch_of() {
  p="$1"
  g="$p/.git"
  if [ -f "$g" ]; then
    gd="$(sed -n 's/^gitdir: //p' "$g" 2>/dev/null)"
    case "$gd" in
      '') return 0 ;;
      /*) g="$gd" ;;
      *) g="$p/$gd" ;;
    esac
  fi
  [ -r "$g/HEAD" ] || return 0
  IFS= read -r line <"$g/HEAD" || [ -n "$line" ] || return 0
  case "$line" in
    "ref: refs/heads/"*) printf '%s' "${line#ref: refs/heads/}" ;;
    *) printf '%.7s' "$line" ;; # detached: the short sha is the label
  esac
}

put() { # LINE CLICK_TARGET — fleet-section appender: buffer + row counter
  # + click map advance together (empty target = not clickable)
  buf="$buf
$1$eol"
  row=$((row + 1))
  [ -z "$2" ] || map="$map $row:$2"
}
put_at() { # ROW LINE CLICK_TARGET — absolute-row twin for the roster
  out="$out$(printf '\033[%d;1H' "$(($1 + 1))")$2$eol"
  [ -z "$3" ] || map="$map $1:$3"
}

frame() {
  info="$(tmux display-message -p -t "${TMUX_PANE:-}" '#{pane_width} #{pane_height} #{session_id} #{session_name}' 2>/dev/null)" || info=""
  read -r width height sid_self here <<EOF0
$info
EOF0
  case "$width" in '' | *[!0-9]*) width=30 ;; esac
  case "$height" in '' | *[!0-9]*) height=24 ;; esac
  # Text budget: 2-col left gutter, keep 1 clear on the right.
  max=$((width - 3))
  [ "$max" -lt 8 ] && max=8

  buf="$(printf '\033[H')$eol"
  # Click map: pane row -> session id, published as @vibe_sidebar_map for
  # the click mode above. A session claims its name row, branch row, AND
  # the blank row under it — deliberate click slop. put/put_at are the
  # ONLY appenders: one call advances the buffer, the row counter, and
  # the map together, so the drawn frame and the click targets cannot
  # skew. (Map keys are 0-based mouse_y rows.)
  row=0
  map=""
  agent_lines=""
  n_agents=0
  while IFS="$tab" read -r sid name path; do
    [ -n "$sid" ] || continue
    # Dots: window order, same semantics as the tabs — attention renders
    # coral (the tab-blend @vibe_dot_fg would vanish here), plain windows
    # (host shells, popups) emit nothing. The same pass collects the
    # AGGREGATE agent roster for the bottom section: every stateful
    # window across every project, "glyph name · project", click target
    # session+window.
    dots=""
    ndots=0
    # US (\037) separator, NOT tab: tab is whitespace-class, so read
    # COLLAPSES adjacent tabs and empty fields shift everything left —
    # a window with an unset option scrambled the roster (live bug).
    while IFS="$us" read -r glyph dfg attn wid wname wactive; do
      [ -n "$glyph" ] || continue
      ndots=$((ndots + 1))
      if [ "$attn" = "1" ]; then
        dotc="${c_coral}"
      else
        dotc="$(vibe_fg "${dfg:-$VIBE_THM_DIM}")"
      fi
      dots="$dots ${dotc}${glyph}"
      # roster row: the client's own active agent gets the coral mark +
      # bright name; everything else stays calm
      if [ "$sid" = "$sid_self" ] && [ "$wactive" = "1" ]; then
        amark="${c_coral}▍" acol="${bold}${c_fg}"
      else
        amark=" " acol="$c_fg"
      fi
      wn="$wname"
      [ "${#wn}" -gt 12 ] && wn="$(printf '%.11s' "$wn")…"
      pmax=$((max - ${#wn} - 6))
      pj=" ${c_dim}· $name"
      if [ "$pmax" -lt 4 ]; then
        pj=""
      elif [ "${#name}" -gt "$pmax" ]; then
        pj=" ${c_dim}· $(printf '%.*s' $((pmax - 1)) "$name")…"
      fi
      n_agents=$((n_agents + 1))
      agent_lines="$agent_lines
$sid:$wid$tab${amark}${reset} ${dotc}${glyph}${reset} ${acol}${wn}${reset}${pj}${reset}"
    done <<EOF2
$(tmux list-windows -t "$sid" -F "#{@vibe_glyph}$us#{@vibe_dot_fg}$us#{@vibe_attn}$us#{window_id}$us#{window_name}$us#{window_active}" 2>/dev/null)
EOF2
    if [ "$name" = "$here" ]; then
      mark="${c_coral}▍" name_c="$c_fg"
    else
      mark=" " name_c="$c_dim"
    fi
    # The dots ride on the name line (2 cols each: space + glyph), so the
    # name budget shrinks with them — otherwise a long name pushes the
    # dots onto a wrapped line AND shifts the click map (host bug,
    # 2026-07-22 screenshot).
    nmax=$((max - ndots * 2))
    [ "$nmax" -lt 8 ] && nmax=8
    shown="$name"
    [ "${#shown}" -gt "$nmax" ] && shown="$(printf '%.*s' $((nmax - 1)) "$shown")…"
    put "${mark}${reset} ${bold}${name_c}${shown}${reset}${dots}${reset}" "$sid"
    br="$(branch_of "$path")"
    if [ -n "$br" ]; then
      [ "${#br}" -gt $((max - 2)) ] && br="$(printf '%.*s' $((max - 3)) "$br")…"
      put "   ${c_dim}⎇ ${br}${reset}" "$sid"
    fi
    put "" "$sid"
  done <<EOF
$(tmux list-sessions -F "#{session_id}$tab#{session_name}$tab#{session_path}" 2>/dev/null | sort -t "$tab" -k2)
EOF
  printf '%s\033[J' "$buf"

  # ── agents: the aggregate roster, anchored to the pane bottom ─────────
  # Skipped entirely when the fleet section leaves no room (min: header +
  # one row + one blank gap). When only PART fits, the last visible row
  # becomes a dim overflow count instead of silently clipping.
  min_start=$((row + 2))
  n_show="$n_agents"
  start=$((height - n_agents - 1))
  if [ "$start" -lt "$min_start" ]; then
    n_show=$((height - min_start - 1))
    start="$min_start"
  fi
  if [ "$n_agents" -gt 0 ] && [ "$n_show" -ge 1 ]; then
    out=""
    put_at "$start" "${c_dim}agents${reset}" ""
    r=$((start + 1))
    i=0
    overflow=$((n_agents - n_show))
    while IFS="$tab" read -r target aline; do
      [ -n "$target" ] || continue
      if [ "$overflow" -gt 0 ] && [ "$i" -eq $((n_show - 1)) ]; then
        put_at "$r" "   ${c_dim}… +$((overflow + 1)) more${reset}" ""
        i=$((i + 1))
        break
      fi
      put_at "$r" "$aline" "$target"
      r=$((r + 1))
      i=$((i + 1))
      [ "$i" -ge "$n_show" ] && break
    done <<EOF3
${agent_lines#
}
EOF3
    printf '%s' "$out"
  fi

  map="${map# }"
  if [ "$map" != "$last_map" ]; then
    tmux set-option -p -t "${TMUX_PANE:-}" @vibe_sidebar_map "$map" 2>/dev/null
    last_map="$map"
  fi
}

last_map=""
last_serial=""
tick=0
printf '\033[?25l'
trap 'printf "\033[?25h"' EXIT
frame
[ "$once" = "1" ] && exit 0
while :; do
  sleep 2
  # ONE round trip per idle tick: die-check and change detection together.
  poll="$(tmux display-message -p -t "${TMUX_PANE:-}" '#{window_panes} #{@vibe_state_serial}' 2>/dev/null)" || exit 0
  n="${poll%% *}"
  serial="${poll#* }"
  case "$n" in '' | *[!0-9]*) exit 0 ;; esac
  # Last real pane gone (shell exited, window would linger on just us):
  # let the window die with it. The main window's agent corpse still
  # counts as a pane (remain-on-exit), so it keeps its sidebar.
  [ "$n" -le 1 ] && exit 0
  tick=$(((tick + 1) % 5))
  if [ "$serial" = "$last_serial" ] && [ "$tick" -ne 0 ]; then
    continue # nothing moved; the 10s forced frame covers serial-less bits
  fi
  last_serial="$serial"
  frame
done

#!/usr/bin/env bash
# The right-click agent menu — per-session surgery wherever an agent
# is drawn (docs/tui-layout.md "Launch surfaces", 2026-07-28; the bar
# door superseded the "vibe ps popup" lean, the row door superseded
# "sidebar rows stay render-only"). One script, three doors:
#
#   agent-menu.sh ghost CLIENT SESSION INDEX    tray ghost cell
#   agent-menu.sh tab   CLIENT SESSION WINDOW_ID  agent viewer tab
#   agent-menu.sh row   CLIENT PANE MOUSE_Y     sidebar agent row
#
# Every door converges on (sess, addr, wid?, dead?) and one item
# table: stop (address-direct over `vibe _stop` — no flag
# reverse-mapping), open viewer (viewer-less + alive), close viewer
# only (tabs — the stock Kill gesture named honestly), dismiss (dead
# rows — `vibe _dismiss` clears the ✗ record, the roster's "seen it").
# A TAB's stop also buries its own viewer: the death record rides the
# pane's title channel the stop just killed, so the pane-died hook
# could never see it as explained (the "Pane is dead" room).
#
# tab carries the WINDOW ID (mouse_status_range holds only the literal
# "window"; the index rides the {mouse} target, so the conf selects
# the clicked window first). ghost INDEX resolves through
# @vibe_ghost_map like agent-open.sh (range names clip at 15 bytes;
# indexes don't); row MOUSE_Y through @vibe_sidebar_map (the same
# no-second-layout-copy rule as left-click). Stale indexes and rows
# fall off the end and do nothing. -M -O per the tray-door lesson
# (chooser.sh records the mechanism). Item COMMAND strings keep their
# #{...} constructs LITERAL — display-menu expands them against the
# client at pick time. Every spliced value is charset-vetted here even
# though the sources are engine-rendered: they become shell words and
# tmux targets.
#
# Host-side: bash-3.2-safe (stock macOS). Runs under the vibe server
# via run-shell.
set -u

mode="${1:-}"
client="${2:-}"

exe="$(tmux show-options -gqv @vibe_exe 2>/dev/null)"
{ [ -n "$exe" ] && [ -x "$exe" ]; } || exit 0

sess=""
addr=""
wid=""
dead=""
svc=""
# Location-aware anchoring (2026-08-04 dogfood, second round: `M`
# reads the mouse from the command's OWN context, and a script-opened
# menu has none — same trap the -M -O matrix records — so the first
# fix put every menu in a corner). The ROW binding expands the
# client-absolute pointer (pane_left+mouse_x / pane_top+mouse_y) while
# the event is live and passes NUMBERS; row mode validates and anchors
# there, falling back to the old above-the-tray pin. Tray doors
# (ghost, tab) keep the plain -y S status quo — their rows ARE the
# bottom edge.
pos=(-y S)
case "$mode" in
ghost)
  sess="${3:-}"
  arg="${4:-}"
  [ -n "$sess" ] || exit 0
  case "$arg" in '' | *[!0-9]*) exit 0 ;; esac
  gmap="$(tmux show-options -t "$sess" -qv @vibe_ghost_map 2>/dev/null)"
  # shellcheck disable=SC2086  # word-splitting the map IS the parse
  set -- $gmap
  [ "$arg" -lt "$#" ] || exit 0
  shift "$arg"
  addr="$1"
  ;;
tab)
  sess="${3:-}"
  arg="${4:-}"
  [ -n "$sess" ] || exit 0
  case "$arg" in @*) ;; *) exit 0 ;; esac
  case "${arg#@}" in '' | *[!0-9]*) exit 0 ;; esac
  wid="$arg"
  addr="$(tmux show-options -w -t "$wid" -qv @vibe_session 2>/dev/null)"
  ;;
row)
  pane="${3:-}"
  y="${4:-}"
  { [ -n "$pane" ] && [ -n "$y" ]; } || exit 0
  case "$y" in '' | *[!0-9]*) exit 0 ;; esac
  mx="${5:-}"
  my="${6:-}"
  if [ -n "$mx" ] && [ -n "$my" ]; then
    case "$mx$my" in *[!0-9]*) ;; *) pos=(-x "$mx" -y "$my") ;; esac
  fi
  target=""
  for entry in $(tmux show-options -pqv -t "$pane" @vibe_sidebar_map 2>/dev/null); do
    case "$entry" in
    "$y":*) target="${entry#*:}" && break ;;
    esac
  done
  [ -n "$target" ] || exit 0
  sess="${target%%:*}"
  case "$target" in
  *:@*)
    # viewer row: the window's stamp carries the address, like a tab.
    wid="${target##*:}"
    case "${wid#@}" in '' | *[!0-9]*) exit 0 ;; esac
    addr="$(tmux show-options -w -t "$wid" -qv @vibe_session 2>/dev/null)"
    ;;
  *:agent-*)
    addr="${target#*:agent-}"
    ;;
  *:dead-*)
    addr="${target#*:dead-}"
    dead=1
    ;;
  *:svc-*)
    svc="${target#*:svc-}"
    ;;
  *:svcx-*)
    svc="${target#*:svcx-}"
    dead=1
    ;;
  *) exit 0 ;; # the name row / slop: not an agent target
  esac
  ;;
*) exit 0 ;;
esac

# Workspace-service rows (docs/tui-layout.md "Workspace services"):
# stop (live) and dismiss (kept corpse) are one operation — `vibe
# _svcstop` → agent-session.sh svc-kill — labeled by intent. Branches
# before the agent-address gate: svc names are window names, not
# addresses.
if [ -n "${svc:-}" ]; then
  case "$svc" in '' | *[!A-Za-z0-9_-]*) exit 0 ;; esac
  case "$sess" in '' | *[!\$A-Za-z0-9_-]*) exit 0 ;; esac
  path="$(tmux display-message -p -t "$sess" '#{session_path}' 2>/dev/null)"
  [ -n "$path" ] || exit 0
  qp="'${path//\'/\'\\\'\'}'"
  svcstop_cmd="run-shell -b \"cd $qp && '#{@vibe_exe}' _svcstop '$svc' >/dev/null 2>&1 && tmux display-message -c '#{client_name}' 'vibe: stopped service $svc' || tmux display-message -c '#{client_name}' 'vibe: stop failed — $svc (container down?)'\""
  args=()
  [ -n "$client" ] && args+=(-c "$client")
  args+=(-M -O "${pos[@]}" -T " $svc ")
  if [ -n "$dead" ]; then
    args+=("dismiss (clear ✗)" x "$svcstop_cmd")
  else
    args+=("stop service" x "$svcstop_cmd")
  fi
  exec tmux display-menu "${args[@]}"
fi

# Agent-convention addresses only — the engine and agent-session.sh
# both re-check, but these words are about to land in menu commands.
case "$addr" in *[!A-Za-z0-9_-]*) exit 0 ;; esac
case "$addr" in
agent | agent-*) ;;
*) exit 0 ;;
esac
case "$sess" in '' | *[!\$A-Za-z0-9_-]*) exit 0 ;; esac

path="$(tmux display-message -p -t "$sess" '#{session_path}' 2>/dev/null)"
[ -n "$path" ] || exit 0

# `vibe _stop`/`_dismiss` resolve the project from the working
# directory — review.sh's -d contract. Feedback lands as a
# display-message either way; the watch channel repaints the roster.
qp="'${path//\'/\'\\\'\'}'"
on_ok="tmux display-message -c '#{client_name}' 'vibe: stopped $addr'"
# Bury inside the success branch, failure-swallowed: a window the
# operator already closed by hand must not flip the report to failed.
[ -n "$wid" ] && on_ok="tmux kill-window -t '$wid' 2>/dev/null; $on_ok"
stop_cmd="run-shell -b \"cd $qp && '#{@vibe_exe}' _stop '$addr' >/dev/null 2>&1 && { $on_ok; } || tmux display-message -c '#{client_name}' 'vibe: stop failed — $addr (container down?)'\""
dismiss_cmd="run-shell -b \"cd $qp && '#{@vibe_exe}' _dismiss '$addr' >/dev/null 2>&1 && tmux display-message -c '#{client_name}' 'vibe: dismissed $addr' || tmux display-message -c '#{client_name}' 'vibe: dismiss failed — $addr (container down?)'\""

args=()
[ -n "$client" ] && args+=(-c "$client")
args+=(-M -O "${pos[@]}" -T " $addr ")
if [ -n "$dead" ]; then
  # Launch-again stays the chooser's door (address→flags needs the
  # manifest's kind list); the menu owns the roster's "seen it".
  args+=("dismiss (clear ✗)" x "$dismiss_cmd")
elif [ -n "$wid" ]; then
  # Stopping the session HUPs the attach client, so the viewer window
  # closes itself — the bury above covers the title-channel race.
  args+=("stop agent" x "$stop_cmd")
  args+=("close viewer only" c "kill-window -t '$wid'")
else
  args+=("open viewer" o "run-shell -b \"bash '#{@vibe_payload_dir}/scripts/agent-open.sh' '#{client_name}' '$sess' '$addr'\"")
  args+=("stop agent" x "$stop_cmd")
fi
exec tmux display-menu "${args[@]}"

#!/usr/bin/env bash
# The right-click agent menu — the bar's stop door (docs/tui-layout.md
# "Launch surfaces", 2026-07-28; supersedes the backlog's "the vibe ps
# popup is the likely door" lean). Right-click on an agent VIEWER TAB
# or a tray GHOST CELL opens surgery for that ONE session: stop it
# (address-direct over `vibe _stop` — no flag reverse-mapping), open a
# viewer for a ghost, or close just the viewer window (the old stock
# Kill gesture, now named honestly — the session lives on). Non-agent
# tabs never reach this script: the conf's press arm serves them the
# stock window menu.
#
#   agent-menu.sh ghost CLIENT SESSION INDEX
#   agent-menu.sh tab   CLIENT SESSION WINDOW_ID
#
# tab carries the WINDOW ID, not an index: mouse_status_range holds
# only the literal "window" for window cells (the index rides the
# {mouse} target alone), so the conf selects the clicked window and
# hands the now-current id over. The ghost INDEX resolves through
# @vibe_ghost_map exactly like
# agent-open.sh (range names clip at 15 bytes; indexes don't), and a
# stale index falls off the end and does nothing. -M -O per the
# tray-door lesson (chooser.sh records the mechanism). Item COMMAND
# strings keep their #{...} constructs LITERAL — display-menu expands
# them against the client when an item is picked. Every spliced value
# is charset-vetted here even though the sources are engine-rendered:
# they become shell words and tmux targets.
#
# Host-side: bash-3.2-safe (stock macOS). Runs under the vibe server
# via run-shell.
set -u

mode="${1:-}"
client="${2:-}"
sess="${3:-}"
arg="${4:-}"
[ -n "$sess" ] || exit 0

exe="$(tmux show-options -gqv @vibe_exe 2>/dev/null)"
{ [ -n "$exe" ] && [ -x "$exe" ]; } || exit 0
path="$(tmux display-message -p -t "$sess" '#{session_path}' 2>/dev/null)"
[ -n "$path" ] || exit 0

addr=""
wid=""
case "$mode" in
ghost)
  case "$arg" in '' | *[!0-9]*) exit 0 ;; esac
  gmap="$(tmux show-options -t "$sess" -qv @vibe_ghost_map 2>/dev/null)"
  # shellcheck disable=SC2086  # word-splitting the map IS the parse
  set -- $gmap
  [ "$arg" -lt "$#" ] || exit 0
  shift "$arg"
  addr="$1"
  ;;
tab)
  case "$arg" in @*) ;; *) exit 0 ;; esac
  case "${arg#@}" in '' | *[!0-9]*) exit 0 ;; esac
  wid="$arg"
  addr="$(tmux show-options -w -t "$wid" -qv @vibe_session 2>/dev/null)"
  ;;
*) exit 0 ;;
esac

# Agent-convention addresses only — the engine and agent-session.sh
# both re-check, but this word is about to land in a menu command.
case "$addr" in *[!A-Za-z0-9_-]*) exit 0 ;; esac
case "$addr" in
agent | agent-*) ;;
*) exit 0 ;;
esac

# `vibe _stop` resolves the project from the working directory —
# review.sh's -d contract. Feedback lands as a display-message either
# way; the sidebar's watch channel repaints the roster on its own.
# A TAB's stop also buries its own viewer: the death record rides the
# pane's title channel, which the stop just killed, so the pane-died
# hook can never see it as explained and would leave a corpse forever
# (the 2026-07-28 dogfood's "Pane is dead" room). We ordered this
# death — the window goes with it.
qp="'${path//\'/\'\\\'\'}'"
on_ok="tmux display-message -c '#{client_name}' 'vibe: stopped $addr'"
# Bury inside the success branch, failure-swallowed: a window the
# operator already closed by hand must not flip the report to failed.
[ -n "$wid" ] && on_ok="tmux kill-window -t '$wid' 2>/dev/null; $on_ok"
stop_cmd="run-shell -b \"cd $qp && '#{@vibe_exe}' _stop '$addr' >/dev/null 2>&1 && { $on_ok; } || tmux display-message -c '#{client_name}' 'vibe: stop failed — $addr (container down?)'\""

args=()
[ -n "$client" ] && args+=(-c "$client")
args+=(-M -O -y S -T " $addr ")
if [ "$mode" = ghost ]; then
  args+=("open viewer" o "run-shell -b \"bash '#{@vibe_payload_dir}/scripts/agent-open.sh' '#{client_name}' '#{session_id}' '$addr'\"")
  args+=("stop agent" x "$stop_cmd")
else
  # Stopping the session HUPs the attach client, so the viewer window
  # closes itself — no second item needed for that path.
  args+=("stop agent" x "$stop_cmd")
  args+=("close viewer only" c "kill-window -t '$wid'")
fi
exec tmux display-menu "${args[@]}"

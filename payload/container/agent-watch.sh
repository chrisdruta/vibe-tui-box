#!/usr/bin/env bash
#
# Container-side sentinel for the `vibe _watch` push channel
# (docs/tui-layout.md "The watch channel"). The host daemon holds one
# long-lived docker exec on this script; we emit one line whenever the
# inner tmux topology or the agent state records change, and the host
# reacts by re-running the agent-ps fetch — so the expensive docker
# round trips happen only when something actually moved, instead of on
# the sidebar's 30s slow tick.
#
# Protocol, one byte per line on stdout:
#   E  something changed (also fired once at start — the sync fetch)
#   H  heartbeat (~15s) — the host's staleness watchdog food
#
# The fingerprint is deliberately session-level: names and attached
# counts (viewers coming and going), plus the state records dir listing
# (mtimes move on every agent-state-hook write, so working/idle/exited
# transitions count). session_activity is deliberately EXCLUDED — it
# moves on every output byte and would turn the channel into a
# fetch-per-second firehose.
#
# Local 1s polling beside a unix socket is noise; a control-mode tmux
# client (true push, plus per-window granularity) is the recorded
# upgrade path once the channel earns it. Lifecycle: stdin is the
# leash — the read below doubles as the poll sleep, and EOF (the host
# daemon died or hung up) ends the loop, so no orphan sentinel ever
# outlives its exec stream. Docker keeps exec'd processes alive after
# the client detaches; without the leash every daemon reconnect would
# stack another poller.
set -u

case "${BASH_SOURCE[0]}" in */*) here="${BASH_SOURCE[0]%/*}" ;; *) here=. ;; esac
# shellcheck source=state-dir.sh disable=SC1091
. "$here/state-dir.sh"

last=""
quiet=0
while :; do
  fp="$(
    tmux list-sessions -F '#{session_name}|#{session_attached}' 2>/dev/null
    ls -ln "$VIBE_STATE_DIR" 2>/dev/null
  )"
  if [ "$fp" != "$last" ]; then
    last="$fp"
    quiet=0
    printf 'E\n' || exit 0
  else
    quiet=$((quiet + 1))
    if [ "$quiet" -ge 15 ]; then
      quiet=0
      printf 'H\n' || exit 0
    fi
  fi
  # The leash: a 1s read timeout IS the poll cadence; EOF or a real
  # error (host gone) exits. Timeouts return >128, EOF returns 1.
  if IFS= read -r -t 1 _; then :; elif [ "$?" -le 128 ]; then exit 0; fi
done

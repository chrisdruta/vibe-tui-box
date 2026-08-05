#!/usr/bin/env bash
#
# Container-side feeder for `vibe ps` agent rows (the v1 ps.sh port,
# docs/architecture.md (agent sessions)). Read-only: joins the inner tmux
# server's agent sessions (naming convention agent(-cmd)(-name)(-cold),
# from agent-session.sh) with the state records agent-state-hook.sh
# writes. Rendering moved engine-side with the v2 language split — this
# emits one machine row per agent session on stdout:
#
#   <name>|<state>|<epoch>|<detail>|<cli>|<model>
#
# state is the folded verdict (working/attention/idle/exited(RC)/
# running/gone), epoch is the state's reference time (empty when
# unknown), detail is the human column — the cli/model truth prefix
# plus a free-text qualifier ("detached", "no-tmux pid N", ""). cli and
# model repeat that truth as their own fields for the tmux surfaces
# that lay it out themselves (sidebar rows, tray ghost cells) instead
# of re-splitting a display string. Field charset is safe by
# construction: names are harness-minted, states are this script's
# literals, cli/model are allowlisted below.
#
# Staleness is evaluated HERE, at read time — never in the live path
# (no TTL, no timers). Liveness layers, most authoritative first, and
# always dominating the semantic record:
#   1. a live tmux session with a live pane  (the run's carrier)
#   2. /proc/<pid> for direct-exec runs (the instance is <pid>.<mint>
#      and exec chains keep that pid all the way into the agent)
#   3. the record's own `exited` written by the run-mode trap
# A non-exited record with no live carrier renders as `gone`: the run
# was killed too hard for any trap to fire. Sessions with no record at
# all (hookless agents) deliberately cap at `running` — no guessing.
set -euo pipefail

base="agent"
# The records dir contract lives in state-dir.sh — one derivation for
# the writer hooks and this reader.
case "${BASH_SOURCE[0]}" in */*) here="${BASH_SOURCE[0]%/*}" ;; *) here=. ;; esac
# shellcheck source=state-dir.sh disable=SC1091
. "$here/state-dir.sh"
state_dir="$VIBE_STATE_DIR"

# One pass over the inner server; empty when no server runs. `=`
# targets below mean exact-match — a bare -t name prefix-matches, and
# `agent` would happily hit `agent-codex`.
declare -A s_attached s_activity
while IFS='|' read -r name attached activity; do
  [ -n "$name" ] || continue
  s_attached[$name]=$attached
  s_activity[$name]=$activity
done < <(tmux list-sessions -F '#{session_name}|#{session_attached}|#{session_activity}' 2>/dev/null || true)

# Row candidates: agent-convention sessions ∪ state records.
declare -A candidates
for name in "${!s_attached[@]}"; do
  case "$name" in
    "$base" | "$base"-*) candidates[$name]=1 ;;
  esac
done
if [ -d "$state_dir" ]; then
  for f in "$state_dir"/*; do
    # Statusline sidecars and ANY write-temp, not sessions —
    # agent-state-hook writes record temps as $session.tmp.$$, which
    # must not render as a ghost agent row.
    case "$f" in *.model | *.tmp.*) continue ;; esac
    [ -f "$f" ] && candidates[$(basename "$f")]=1
  done
fi
[ "${#candidates[@]}" -gt 0 ] || exit 0

while IFS= read -r name; do
  rec_state="" rec_inst="" rec_ts="" rec_rc=""
  if [ -r "$state_dir/$name" ]; then
    read -r rec_state rec_inst rec_ts rec_rc <"$state_dir/$name" 2>/dev/null || true
  fi

  state="" ts="" detail=""
  if [ -n "${s_attached[$name]:-}" ]; then
    pane_dead="$(tmux list-panes -t "=$name" -F '#{pane_dead}' 2>/dev/null | head -1 || true)"
    if [ "$pane_dead" = "1" ]; then
      # Corpse pane (user remain-on-exit): trust a recorded exit; a
      # non-exited record means no trap fired — the run is gone.
      if [ "$rec_state" = "exited" ]; then state="exited(${rec_rc:-?})"; else state="gone"; fi
      ts="$rec_ts"
    elif [ -n "$rec_state" ] && [ "$rec_state" != "exited" ]; then
      state="$rec_state"
      ts="$rec_ts"
    elif [ "$rec_state" = "exited" ]; then
      # Recorded exit but the session lives: a fresh hookless run
      # reattached the session after the recorded one ended.
      state="running"
      ts="${s_activity[$name]:-}"
    else
      state="running" # hookless cap: alive is all we know
      ts="${s_activity[$name]:-}"
    fi
    [ "${s_attached[$name]}" = "0" ] && detail="detached"
  elif [ -n "$rec_state" ]; then
    if [ "$rec_state" = "exited" ]; then
      state="exited(${rec_rc:-?})"
    else
      # No tmux carrier: a direct-exec run if its pid survives.
      pid="${rec_inst%%.*}"
      if [ -n "$pid" ] && [ -d "/proc/$pid" ]; then
        state="$rec_state" detail="no-tmux pid $pid"
      else
        state="gone"
      fi
    fi
    ts="$rec_ts"
  else
    continue
  fi
  # Truth prefix for the detail column: the CLI actually running (the
  # inner window is named after the binary at session creation) and the
  # statusline-fed model sidecar. Sessions are addresses; rows show
  # what runs at them. Both values are free bytes (window names, file
  # contents): allowlist to the ASCII the engine's row encoder keeps
  # (it strips 0x7F+, so multibyte separators would vanish) and never
  # let `|` reach the field protocol; the substitutions carry `|| true`
  # because a session can vanish between the snapshot above and this
  # row — set -e must not kill the feeder mid-listing.
  cli="" model=""
  if [ -n "${s_attached[$name]:-}" ]; then
    cli="$(tmux list-windows -t "=$name" -F '#{window_name}' 2>/dev/null | head -1 || true)"
    cli="${cli//[^a-zA-Z0-9:._-]/}"
    cli="${cli:0:32}"
  fi
  if [ -r "$state_dir/$name.model" ]; then
    model="$(head -c 32 "$state_dir/$name.model" 2>/dev/null || true)"
    model="${model//[^a-zA-Z0-9 ._-]/}"
  fi
  who="$cli"
  [ -n "$model" ] && who="${who:+$who - }$model"
  [ -n "$who" ] && detail="${who}${detail:+ - $detail}"
  # The name came from a filename: re-strip it like every other field
  # so a stray delimiter can't shift the row protocol.
  name="${name//[^a-zA-Z0-9_-]/}"
  [ -n "$name" ] || continue
  printf '%s|%s|%s|%s|%s|%s\n' "$name" "$state" "$ts" "$detail" "$cli" "$model"
done < <(printf '%s\n' "${!candidates[@]}" | sort)

# Workspace services (docs/tui-layout.md "Workspace services"): one
# row per window of the `services` session — the svc.sh windows
# post-start hooks stand up. No state records and no title channel: a
# service window is a pane on THIS server, so #{pane_dead} +
# #{pane_dead_status} carry liveness AND the exit code directly (the
# fact agent runs never get — theirs dies at the docker-exec client
# boundary). The vocabulary folds to running/exited(RC); nothing here
# converses. The cli field's literal `svc` is the row's kind marker
# for the engine parser; names are svc.sh-vetted but re-stripped —
# free bytes never reach the field protocol.
#
# The epoch means time IN STATE, like the agent rows': a running
# service counts from its process start (/proc starttime — tmux has no
# pane-start format, and #{window_activity} resets with every byte a
# chatty service prints); a dead one counts from #{window_activity},
# which stopped moving when the pane died. Unknown stays empty.
if tmux has-session -t "=services" 2>/dev/null; then
  now="$(date +%s)"
  up=""
  read -r up _ </proc/uptime 2>/dev/null || true
  up="${up%%.*}"
  clk="$(getconf CLK_TCK 2>/dev/null || true)"
  case "$clk" in '' | *[!0-9]*) clk=100 ;; esac
  while IFS='|' read -r wname wdead wstatus wpid wactivity; do
    wname="${wname//[^a-zA-Z0-9_-]/}"
    [ -n "$wname" ] || continue
    wstate="running" wepoch=""
    if [ "$wdead" = "1" ]; then
      wstatus="${wstatus//[^0-9]/}"
      wstate="exited(${wstatus:-?})"
      wepoch="${wactivity//[^0-9]/}"
    else
      case "$wpid" in '' | *[!0-9]*) wpid="" ;; esac
      if [ -n "$up" ] && [ -n "$wpid" ] && [ -r "/proc/$wpid/stat" ]; then
        # starttime is stat field 22; comm can hold spaces and parens,
        # so count from after the closing ')' (state is field 3 there).
        stat="$(cat "/proc/$wpid/stat" 2>/dev/null || true)"
        # shellcheck disable=SC2086  # word-splitting the stat fields is the point
        set -- ${stat##*) }
        ticks="${20:-}"
        case "$ticks" in '' | *[!0-9]*) ticks="" ;; esac
        [ -n "$ticks" ] && wepoch=$((now - up + ticks / clk))
      fi
    fi
    printf '%s|%s|%s||svc|\n' "$wname" "$wstate" "$wepoch"
  done < <(tmux list-windows -t "=services" \
    -F '#{window_name}|#{pane_dead}|#{pane_dead_status}|#{pane_pid}|#{window_activity}' 2>/dev/null || true)
fi
exit 0

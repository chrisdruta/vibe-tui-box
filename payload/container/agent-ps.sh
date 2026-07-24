#!/usr/bin/env bash
#
# Container-side feeder for `vibe ps` agent rows (the v1 ps.sh port,
# docs/architecture.md (agent sessions)). Read-only: joins the inner tmux
# server's agent sessions (naming convention agent(-cmd)(-name)(-cold),
# from agent-session.sh) with the state records agent-state-hook.sh
# writes. Rendering moved engine-side with the v2 language split — this
# emits one machine row per agent session on stdout:
#
#   <name>|<state>|<epoch>|<detail>
#
# state is the folded verdict (working/attention/idle/exited(RC)/
# running/gone), epoch is the state's reference time (empty when
# unknown), detail is a free-text qualifier ("detached",
# "no-tmux pid N", ""). Field charset is safe by construction: names
# are harness-minted, states are this script's literals.
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
state_dir="${XDG_RUNTIME_DIR:-/tmp}/vibe-agent-state-$(id -u)"

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
    pane_dead="$(tmux list-panes -t "=$name" -F '#{pane_dead}' 2>/dev/null | head -1)"
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
  printf '%s|%s|%s|%s\n' "$name" "$state" "$ts" "$detail"
done < <(printf '%s\n' "${!candidates[@]}" | sort)
exit 0

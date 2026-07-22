#!/usr/bin/env bash
#
# vibe ps — every agent and service at a glance (BACKLOG "agent state at a
# glance"). Read-only: joins the inner tmux server's agent sessions
# (naming convention agent(-cmd)(-cold), from agent-entry.sh) with the
# state records agent-state-hook.sh writes, plus the services session per
# svc.sh's window-exists-means-running model.
#
# Staleness is evaluated HERE, at read time — never in the live path (no
# TTL, no timers; the sol-review rule). Liveness layers, most
# authoritative first, and always dominates the semantic record:
#   1. a live tmux session with a live pane  (the run's carrier)
#   2. /proc/<pid> for DEV_AGENT_TMUX=0 runs (the instance is <pid>.<mint>
#      and exec chains keep that pid all the way into the agent)
#   3. the record's own `exited` written by the env-run.sh trap
# A non-exited record with no live carrier renders as `gone`: the run was
# killed too hard for any trap to fire. Sessions with no record at all
# (hookless agents, pre-identity pins) deliberately cap at `running` +
# activity age — no guessing.
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh disable=SC1091
source "$script_dir/lib.sh" # config.env + DEV_* defaults + REPO_ROOT

base="${DEV_AGENT_TMUX_SESSION:-agent}"
svc_session="${DEV_ATTACH_TMUX_SESSION:-services}"
state_dir="${XDG_RUNTIME_DIR:-/tmp}/vibe-agent-state-$(id -u)"
now="$(date +%s)"

# Theme palette + state map: theme.sh beside the tmux conf (whose @thm
# block is its lockstep twin). Colors only when stdout is a terminal.
# shellcheck source=../config/theme.sh disable=SC1091
source "$script_dir/../config/theme.sh"
colors=""
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
  colors=1
  c_green="$(vibe_fg "$VIBE_THM_GREEN")" c_dim="$(vibe_fg "$VIBE_THM_DIM")"
  c_red="$(vibe_fg "$VIBE_THM_RED")" c_bold=$'\e[1m' c_off=$'\e[0m'
else
  c_green="" c_dim="" c_red="" c_bold="" c_off=""
fi

age() { # seconds -> compact human age
  local s=$1
  ((s < 0)) && s=0
  if ((s < 60)); then printf '%ds' "$s"
  elif ((s < 3600)); then printf '%dm' $((s / 60))
  elif ((s < 86400)); then printf '%dh' $((s / 3600))
  else printf '%dd' $((s / 86400)); fi
}

# One pass over the inner server; empty when no server runs. `=` targets
# below mean exact-match — a bare -t name prefix-matches, and `agent`
# would happily hit `agent-codex`.
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

# shellcheck disable=SC2154  # vibe_glyph/vibe_state_hex: set by vibe_state_style
set_style() { # state -> $glyph (colored char) + $label_c (state label color)
  if vibe_state_style "$1"; then # the shared map (theme.sh)
    label_c=""
    [ -n "$colors" ] && label_c="$(vibe_fg "$vibe_state_hex")"
    glyph="${label_c}${vibe_glyph}${c_off}"
  else
    glyph="${c_dim}●${c_off}" label_c="" # unknown state: dim, no guessing
  fi
}

row() { # NAME STATE WHEN EXTRA — pad plain text, colorize after, so the
  set_style "$2" # invisible escape bytes never skew the columns
  printf '  %s %-18s %s%s %-5s %s\n' \
    "$glyph" "$1" "$label_c" "$(printf '%-12s' "$2")$c_off" "$3" "$4"
}

proj=""
# Identity from the launcher-injected env (host trust record), not a workspace
# file (M-1). Sanitized: it decorates a status header.
proj_name="$(printf '%s' "${VIBE_PROJECT_NAME:-}" | tr -cd 'A-Za-z0-9._-' | head -c 48)"
[ -n "$proj_name" ] && proj=" $c_dim($proj_name)$c_off"
printf '%sAGENTS%s%s\n' "$c_bold" "$c_off" "$proj"

if [ "${#candidates[@]}" -eq 0 ]; then
  printf "  %s(none — 'vibe agent' starts one)%s\n" "$c_dim" "$c_off"
else
  while IFS= read -r name; do
    rec_state="" rec_inst="" rec_ts="" rec_rc=""
    if [ -r "$state_dir/$name" ]; then
      read -r rec_state rec_inst rec_ts rec_rc <"$state_dir/$name" 2>/dev/null || true
    fi

    state="" when="" extra=""
    if [ -n "${s_attached[$name]:-}" ]; then
      pane_dead="$(tmux list-panes -t "=$name" -F '#{pane_dead}' 2>/dev/null | head -1)"
      if [ "$pane_dead" = "1" ]; then
        # Corpse pane (user remain-on-exit): trust a recorded exit; a
        # non-exited record means no trap fired — the run is gone.
        if [ "$rec_state" = "exited" ]; then state="exited(${rec_rc:-?})"; else state="gone"; fi
        [ -n "$rec_ts" ] && when="$(age $((now - rec_ts)))"
      elif [ -n "$rec_state" ] && [ "$rec_state" != "exited" ]; then
        state="$rec_state"
        when="$(age $((now - ${rec_ts:-now})))"
      elif [ "$rec_state" = "exited" ]; then
        # Recorded exit but the session lives: a fresh hookless/pre-identity
        # run reattached the session after the recorded one ended.
        state="running"
        when="$(age $((now - ${s_activity[$name]:-now})))"
      else
        state="running" # hookless cap: alive is all we know
        when="$(age $((now - ${s_activity[$name]:-now})))"
      fi
      [ "${s_attached[$name]}" = "0" ] && extra="${c_dim}detached$c_off"
    elif [ -n "$rec_state" ]; then
      if [ "$rec_state" = "exited" ]; then
        state="exited(${rec_rc:-?})"
      else
        # No tmux carrier: a DEV_AGENT_TMUX=0 run if its pid survives.
        pid="${rec_inst%%.*}"
        if [ -n "$pid" ] && [ -d "/proc/$pid" ]; then
          state="$rec_state" extra="${c_dim}no-tmux pid $pid$c_off"
        else
          state="gone"
        fi
      fi
      [ -n "$rec_ts" ] && when="$(age $((now - rec_ts)))"
    else
      continue
    fi
    row "$name" "$state" "$when" "$extra"
  done < <(printf '%s\n' "${!candidates[@]}" | sort)
fi

# Services: window-exists means running (svc.sh); a dead pane only
# survives under a user remain-on-exit conf and reads as crashed.
if [ -n "${s_attached[$svc_session]:-}" ]; then
  printf '%sSERVICES%s %s(session: %s)%s\n' "$c_bold" "$c_off" "$c_dim" "$svc_session" "$c_off"
  while IFS='|' read -r win dead activity; do
    [ -n "$win" ] || continue
    if [ "$dead" = "1" ]; then
      printf '  %s✗%s %-24s %s(crashed — next container start respawns it)%s\n' \
        "$c_red" "$c_off" "$win" "$c_dim" "$c_off"
    else
      printf '  %s●%s %-24s %s\n' "$c_green" "$c_off" "$win" \
        "$(age $((now - ${activity:-now})))"
    fi
  done < <(tmux list-windows -t "=$svc_session" -F '#{window_name}|#{pane_dead}|#{window_activity}' 2>/dev/null || true)
fi
exit 0

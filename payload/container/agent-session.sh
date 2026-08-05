#!/usr/bin/env bash
#
# Container-side session carrier for `vibe agent` — the v2 port of v1's
# agent-entry.sh (docs/architecture.md (agent sessions)). The engine execs this
# with real argv (`agent [FLAGS…] -- CMD [ARGS…]`) whenever the payload
# is mounted and the image has tmux; the agent CLI runs inside an inner
# tmux session so it survives its viewer, and a rerun reattaches (-A)
# instead of double-launching. The one remaining shell-string quoting
# layer is the tmux pane command at the bottom (v1 rule: do not add
# another).
#
# v1 deltas: no lib.sh/config.env — identity arrives as VIBE_PROJECT /
# VIBE_PROJECT_NAME from the engine and the agent command as argv after
# `--`; no env-run.sh — the engine injects the frozen env file into this
# exec, and the inner server is dedicated to the agent, so panes
# inheriting it is fine (the v1 concern was a server shared with
# interactive shells).

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

usage() {
  echo "Usage: agent-session.sh agent [--cold] [-a] [-s NAME] [--restart] -- COMMAND [ARGUMENT ...]" >&2
  echo "       agent-session.sh stop [--cold] [-a] [-s NAME] -- COMMAND [ARGUMENT ...]" >&2
  echo "       agent-session.sh attach [SESSION [WINDOW]]" >&2
  echo "       agent-session.sh kill SESSION" >&2
  echo "       agent-session.sh svc-kill NAME" >&2
  echo "       agent-session.sh svc-select NAME" >&2
  echo "       agent-session.sh dismiss SESSION" >&2
  echo "       agent-session.sh run COMMAND [ARGUMENT ...]" >&2
  echo "       agent-session.sh reap" >&2
  exit 2
}

mode="${1:-}"
shift || true

# `vibe tui` kill cleanup, not a user surface: a host-side pane death
# orphans its docker-exec tmux client in here (docker never kills the
# exec'd process with its client), leaving ghost viewers that inflate
# attached counts and outlive the UI. At kill time every VIBE_NESTED
# client is dead by definition — the UI that spawned them is gone — and
# plain `vibe agent` clients never carry the marker, so they are never
# touched. Only detaches clients: agents keep running.
if [ "$mode" = "reap" ]; then
  tmux list-clients -F '#{client_pid} #{client_tty}' 2>/dev/null |
    while read -r cpid ctty; do
      [ -n "$cpid" ] || continue
      if tr '\0' '\n' <"/proc/$cpid/environ" 2>/dev/null | grep -qx 'VIBE_NESTED=1'; then
        tmux detach-client -t "$ctty" 2>/dev/null || true
      fi
    done
  exit 0
fi

# attach mode is the door into an arbitrary inner session — by default
# `services`, the session post-start hooks populate via svc.sh. -A
# attaches or creates: attaching to a not-yet-started services session
# just yields an empty session with a shell, which is also the right
# place to start one by hand.
if [ "$mode" = "attach" ]; then
  session="${1:-services}"
  window="${2:-}"
  case "$session" in
    ""|*[!A-Za-z0-9_-]*)
      echo "agent-session.sh attach: SESSION must be letters, digits, '_' or '-': $session" >&2
      exit 2
      ;;
  esac
  case "$window" in
    *[!A-Za-z0-9_-]*)
      echo "agent-session.sh attach: WINDOW must be letters, digits, '_' or '-': $window" >&2
      exit 2
      ;;
  esac
  if [ -n "${TMUX:-}" ]; then
    echo "agent-session.sh attach: already inside the inner tmux; use tmux switch-client" >&2
    exit 2
  fi
  # Agent-convention names attach EXISTING sessions only: `vibe agent`
  # is the door that launches, and the -A create below would otherwise
  # mint a bare-shell session under an agent address on any typo or
  # stale viewer click (the truncated-range dogfood conjured exactly
  # such a junk "agent-cod"). Non-agent names keep -A's create — an
  # empty `services` session is a feature.
  case "$session" in
    agent | agent-*)
      if ! tmux has-session -t "=$session" 2>/dev/null; then
        echo "agent-session.sh attach: no session '$session' — launch agents with 'vibe agent'" >&2
        exit 1
      fi
      ;;
  esac
  # An optional WINDOW pre-selects within the session (the sidebar's
  # service-row click lands on the clicked window, not whatever was
  # active); best-effort — a window gone since the click still attaches.
  if [ -n "$window" ]; then
    tmux select-window -t "=$session:$window" 2>/dev/null || true
  fi
  exec tmux -u -f "$script_dir/tmux-agent.conf" new-session -A -s "$session"
fi

# kill mode is stop's address-direct twin — attach's precedent, for
# the tui's right-click menu: that door already holds the FULL address
# the `vibe ps` join reported, and reverse-mapping an address into
# stop's flags would recompute the grammar in a second place (the
# drift stop's own header warns about). Agent-convention names ONLY:
# `services` and friends are lifecycle surfaces, not menu kills.
# kill-session HUPs the pane; the run-mode EXIT trap records the death
# for `vibe ps`. Idempotent like stop.
if [ "$mode" = "kill" ]; then
  session="${1:-}"
  case "$session" in
    *[!A-Za-z0-9_-]* | "")
      echo "agent-session.sh kill: SESSION must be letters, digits, '_' or '-': $session" >&2
      exit 2
      ;;
  esac
  case "$session" in
    agent | agent-*) ;;
    *)
      echo "agent-session.sh kill: not an agent session address: '$session'" >&2
      exit 2
      ;;
  esac
  if tmux kill-session -t "=$session" 2>/dev/null; then
    echo "stopped agent session '$session'"
  else
    echo "agent session '$session' is not running"
  fi
  exit 0
fi

# svc-kill mode ends ONE workspace-service window of the `services`
# session — the TUI's stop (live) and dismiss (corpse) verbs, which
# are the same tmux operation with different intents: kill-window ends
# a running service or clears a kept corpse (svc.sh's remain-on-exit).
# Window-scoped on purpose — agent sessions have their own kill above,
# and killing the last services window ends the session, which is
# exactly the empty-roster truth. Idempotent like kill.
# svc-select re-aims the `services` session at one window — the
# sidebar's service-row click when a shared viewer is already open
# (agent-open.sh -s): the current window is session state, so every
# attached client follows. Best-effort like attach's own pre-select.
if [ "$mode" = "svc-select" ]; then
  name="${1:-}"
  case "$name" in
    *[!A-Za-z0-9_-]* | "")
      echo "agent-session.sh svc-select: NAME must be letters, digits, '_' or '-': $name" >&2
      exit 2
      ;;
  esac
  if tmux select-window -t "=services:$name" 2>/dev/null; then
    echo "selected service window '$name'"
  else
    echo "service window '$name' does not exist"
    exit 1
  fi
  exit 0
fi

if [ "$mode" = "svc-kill" ]; then
  name="${1:-}"
  case "$name" in
    *[!A-Za-z0-9_-]* | "")
      echo "agent-session.sh svc-kill: NAME must be letters, digits, '_' or '-': $name" >&2
      exit 2
      ;;
  esac
  if tmux kill-window -t "=services:$name" 2>/dev/null; then
    echo "removed service window '$name'"
  else
    echo "service window '$name' does not exist"
  fi
  exit 0
fi

# dismiss mode clears a DEAD session's state record — the sidebar's ✗
# row exists only as that record (`vibe ps` rows are live sessions ∪
# records), so removing it is the "seen it" gesture the roster lacked
# (2026-07-28, the sidebar right-click design). Refuses a running
# session outright: its record is live truth, and the hook would
# rewrite it on the next event anyway — dismiss is for the dead.
if [ "$mode" = "dismiss" ]; then
  session="${1:-}"
  case "$session" in
    *[!A-Za-z0-9_-]* | "")
      echo "agent-session.sh dismiss: SESSION must be letters, digits, '_' or '-': $session" >&2
      exit 2
      ;;
  esac
  case "$session" in
    agent | agent-*) ;;
    *)
      echo "agent-session.sh dismiss: not an agent session address: '$session'" >&2
      exit 2
      ;;
  esac
  if tmux has-session -t "=$session" 2>/dev/null; then
    echo "agent-session.sh dismiss: '$session' is running — stop it first" >&2
    exit 1
  fi
  # shellcheck source=state-dir.sh disable=SC1091
  . "$script_dir/state-dir.sh"
  # The .model sidecar goes with the record, or the NEXT run at this
  # address briefly wears the previous run's model label.
  rm -f "$VIBE_STATE_DIR/$session" "$VIBE_STATE_DIR/$session.model" 2>/dev/null || true
  echo "dismissed '$session'"
  exit 0
fi

# run mode is the pane-side wrapper agent mode launches: it trades the
# zero-cost exec for an EXIT trap that records process death — the one
# transition no Claude hook can report, and the liveness layer that
# dominates semantic state. EXIT fires on errexit and trappable signals
# too; the explicit exit keeps the agent's code.
if [ "$mode" = "run" ]; then
  [ "$#" -gt 0 ] || usage
  # shellcheck disable=SC2154  # rc is assigned inside the single-quoted trap
  trap 'rc=$?; VIBE_AGENT_EXIT=$rc bash "$script_dir/agent-state-hook.sh" __exit </dev/null >/dev/null 2>&1 || true; exit $rc' EXIT
  "$@"
  exit "$?"
fi

# stop shares agent mode's flag surface: the flags only exist to name
# the session (agent(-cmd)(-name)(-cold)), and computing that name in a
# second place is how the two would drift.
case "$mode" in agent | stop) ;; *) usage ;; esac

# --cold starts the agent without repo instruction files for an unbiased
# session. -a marks the command after `--` as a `vibe agent -a` override
# (the session gets its own name, so the override never steals the
# default session). -s NAME runs a parallel instance in its own session
# (agent-NAME): without it, -A reattaches the one default session —
# persistence by design, but "another agent" usually means another
# AGENT. Every variant gets its own tmux session and own identity.
# --restart replaces the persistent session instead of reattaching it.
cold=0
override=0
restart=0
session_suffix=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --cold)
      cold=1
      shift
      ;;
    --restart)
      # Restart is a flag on AGENT mode only (the usage grammar); stop
      # mode used to swallow it silently — reject, don't ignore.
      if [ "$mode" != "agent" ]; then
        echo "vibe agent: --restart is not a stop flag (use one of --stop | --restart)" >&2
        exit 2
      fi
      restart=1
      shift
      ;;
    -a)
      override=1
      shift
      ;;
    -s)
      if [ "$#" -lt 2 ] || [ -z "$2" ]; then
        usage
      fi
      # The name lands in tmux session names, state-file names, and the
      # title channel: keep it to the safe charset all three share
      # (tmux rejects '.' and ':' in session names outright).
      case "$2" in
        *[!A-Za-z0-9_-]*)
          echo "vibe agent -s: NAME must be letters, digits, '_' or '-': $2" >&2
          exit 2
          ;;
      esac
      session_suffix="$2"
      shift 2
      ;;
    --)
      shift
      break
      ;;
    *)
      usage
      ;;
  esac
done
[ "$#" -gt 0 ] || usage
agent_cmd=("$@")

# session is the stable ADDRESS (what stop/-s/-a target); display is
# the TRUTH label every UI surface shows (tab, border, roster, ps).
# Composed HERE beside the address and shipped through the identity
# env + title channel — never re-derived from the address elsewhere:
# one grammar, one place. ASCII ':' joins the disambiguators so every
# downstream surface (window names, ps rows through the engine's
# ASCII-only encoder) keeps them intact.
session="agent"
display="${agent_cmd[0]##*/}"
[ "$override" = "1" ] && session="$session-${agent_cmd[0]##*/}"
if [ -n "$session_suffix" ]; then
  session="$session-$session_suffix"
  display="$display:$session_suffix"
fi

if [ "$cold" = "1" ]; then
  case "${agent_cmd[0]##*/}" in
    claude) agent_cmd+=(--safe-mode) ;;
    codex) agent_cmd+=(-c project_doc_max_bytes=0) ;;
    *)
      echo "vibe agent --cold: no known instruction-skip flags for: ${agent_cmd[0]}" >&2
      exit 2
      ;;
  esac
  session="$session-cold"
  display="$display:cold"
fi

# stop ends the named session's run — the one lifecycle affordance
# persistence needs (everything else keeps agents alive by design).
# kill-session HUPs the pane; the run-mode EXIT trap records the death
# for `vibe ps`. Idempotent: a session that is not running is already
# what stop promises.
if [ "$mode" = "stop" ]; then
  if tmux kill-session -t "=$session" 2>/dev/null; then
    echo "stopped agent session '$session'"
  else
    echo "agent session '$session' is not running"
  fi
  exit 0
fi

# The claude wiring rides the read-only payload mount instead of v1's
# settings-merge into the user's config, split across the two surfaces
# the CLI offers: the vibe PLUGIN (--plugin-dir, loaded in place per
# session — never installed, so nothing lands on the agent-state
# volume) carries the agent-state hooks, the subagent statusline, and
# /vibe:request; the SETTINGS file keeps the keys a plugin cannot
# express (statusLine, autoMemoryEnabled, autoUpdates, sandbox). The
# settings pin autoMemoryEnabled=false (a hardened container opts IN to
# cross-session memory, it doesn't inherit the CLI's upstream default);
# when the manifest opted in (agent.memory: auto, arriving as
# VIBE_AGENT_MEMORY from the engine), a derived copy flips the one key.
# jq ships beside tmux in the tools recipe, but any failure falls back
# to the static file — memory is a preference, never worth blocking the
# agent over.
case "${agent_cmd[0]##*/}" in
  claude)
    settings="$script_dir/claude-settings.json"
    if [ "${VIBE_AGENT_MEMORY:-off}" = "auto" ]; then
      derived="${XDG_RUNTIME_DIR:-/tmp}/vibe-claude-settings-$session.json"
      if jq '.autoMemoryEnabled = true' "$settings" >"$derived.$$" 2>/dev/null &&
        mv -f "$derived.$$" "$derived" 2>/dev/null; then
        settings="$derived"
      else
        rm -f "$derived.$$" 2>/dev/null || true
      fi
    fi
    agent_cmd+=(--settings "$settings" --plugin-dir "$script_dir/claude-plugin")
    ;;
esac

# Identity for the agent-state hook: SESSION is the stable logical name,
# INSTANCE is unique per run so a restarted agent can never inherit a
# previous run's state records. The `env` prefix lives inside the ONE
# cmd array so the tmux %q path and the direct-exec path below cannot
# disagree; on -A reattach the fresh mint is discarded with the unused
# command string and the pane's original run keeps its identity — that
# run is what the state records describe.
#
# CARRIER tells the hook whether this identity's run lives inside the
# inner tmux session named $session — computed from the SAME condition
# that picks the branch below. The hook needs it because background
# forks of an agent (Claude fork-session jobs) inherit the identity env
# but not $TMUX: with carrier=tmux their events still drive the title
# channel, while carrier=none runs can never stomp the title of an
# unrelated same-named tmux session.
carrier=none
[ -z "${TMUX:-}" ] && carrier=tmux
cmd=(env "VIBE_AGENT_SESSION=$session" "VIBE_AGENT_INSTANCE=$$.$(date +%s)" \
  "VIBE_AGENT_CARRIER=$carrier" "VIBE_AGENT_CMD=${agent_cmd[0]##*/}" \
  "VIBE_AGENT_DISPLAY=$display" \
  bash "$script_dir/agent-session.sh" run "${agent_cmd[@]}")

# Already inside a tmux session (a shell in the inner server): run the
# agent directly rather than nesting servers.
if [ -n "${TMUX:-}" ]; then
  exec "${cmd[@]}"
fi

# Reattach-mismatch guard: -A discards the freshly composed command
# whenever the session already exists, so a changed agent.cmd (read
# live from the manifest — no rebuild involved) would silently keep
# delivering the OLD agent forever. The window name is minted from the
# agent binary at session creation (-n below, automatic-rename off), so
# it names what actually runs; on a mismatch ask before reattaching —
# or just warn when there is no terminal to ask.
want="${agent_cmd[0]##*/}"
if [ "$restart" != "1" ]; then
  running="$(tmux list-windows -t "=$session" -F '#{window_name}' 2>/dev/null | head -n1 || true)"
  if [ -n "$running" ] && [ "$running" != "$want" ]; then
    echo "vibe: session '$session' is running $running, but the requested agent is $want." >&2
    if [ -t 0 ]; then
      read -r -p "restart with $want? this ends the running $running [y/N] " ans || ans=""
      case "$ans" in
        [Yy]*) restart=1 ;;
        *) echo "reattaching to $running ('vibe agent --restart' switches later)" >&2 ;;
      esac
    else
      echo "reattaching anyway ('vibe agent --restart' switches)" >&2
    fi
  fi
fi
if [ "$restart" = "1" ]; then
  tmux kill-session -t "=$session" 2>/dev/null || true
fi

# The inner server loads only the payload conf (status off, titles on,
# passthrough) — never the user's ~/.tmux.conf; -f applies at server
# start and is inert on -A reattach, like every other creation flag.
# Name the window after the agent binary (-n also turns off
# automatic-rename): `tmux list-windows -a` reading "claude | codex"
# beats windows named after wrappers. (`new-session -e` for the
# identity vars was considered and rejected in v1: session env would
# leak into every future pane, letting a manually launched agent there
# impersonate this run's instance.)
exec tmux -u -f "$script_dir/tmux-agent.conf" \
  new-session -A -s "$session" -n "${agent_cmd[0]##*/}" \
  "$(printf "%q " "${cmd[@]}")"

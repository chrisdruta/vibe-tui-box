#!/usr/bin/env bash
#
# Container-side session carrier for `vibe agent` — the v2 port of v1's
# agent-entry.sh (docs/agent-session-design.md). The engine execs this
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
# interactive shells). attach/reap modes arrive with later slices.

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

usage() {
  echo "Usage: agent-session.sh agent [--cold] [-a] [-s NAME] -- COMMAND [ARGUMENT ...]" >&2
  exit 2
}

mode="${1:-}"
shift || true
[ "$mode" = "agent" ] || usage

# --cold starts the agent without repo instruction files for an unbiased
# session. -a marks the command after `--` as a `vibe agent -a` override
# (the session gets its own name, so the override never steals the
# default session). -s NAME runs a parallel instance in its own session
# (agent-NAME): without it, -A reattaches the one default session —
# persistence by design, but "another agent" usually means another
# AGENT. Every variant gets its own tmux session and own identity.
cold=0
override=0
session_suffix=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --cold)
      cold=1
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

session="agent"
[ "$override" = "1" ] && session="$session-${agent_cmd[0]##*/}"
[ -n "$session_suffix" ] && session="$session-$session_suffix"

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
fi

# Identity for the agent-state hook (slice 2): SESSION is the stable
# logical name, INSTANCE is unique per run so a restarted agent can
# never inherit a previous run's state records. The `env` prefix lives
# inside the ONE cmd array so the tmux %q path and the direct-exec path
# below cannot disagree; on -A reattach the fresh mint is discarded with
# the unused command string and the pane's original run keeps its
# identity — that run is what the state records describe.
cmd=(env "VIBE_AGENT_SESSION=$session" "VIBE_AGENT_INSTANCE=$$.$(date +%s)" "${agent_cmd[@]}")

# Already inside a tmux session (a shell in the inner server): run the
# agent directly rather than nesting servers.
if [ -n "${TMUX:-}" ]; then
  exec "${cmd[@]}"
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

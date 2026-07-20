#!/usr/bin/env bash
#
# Container-side entry for `vibe agent` and `vibe attach`. The host launcher
# used to inline this logic as single-quoted `bash -lc` payloads with
# positional smuggling and printf %q re-quoting — the most edit-hazardous
# code in the repo. As a real script it receives real argv straight from
# `docker exec`; the only quoting layer left is the tmux command
# string, isolated at the bottom. Container-side only: bash 4+ is fine.

set -o errexit
set -o nounset
set -o pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh disable=SC1091
source "$script_dir/lib.sh" # config.env + DEV_* defaults + REPO_ROOT
cd -- "$REPO_ROOT"

mode="${1:-}"
shift || true

case "$mode" in
  attach)
    # Door into a long-lived services session a project's post-start hook
    # stands up. Session name: argument > DEV_ATTACH_TMUX_SESSION > "main".
    session="${1:-${DEV_ATTACH_TMUX_SESSION:-main}}"
    exec tmux -u new-session -A -s "$session"
    ;;
  agent) ;;
  *)
    echo "Usage: agent-entry.sh agent|attach [ARGUMENT ...]" >&2
    exit 2
    ;;
esac

# --cold starts the agent without repo instruction files (CLAUDE.md/AGENTS.md
# and .claude customizations) for an unbiased session. -a/--agent overrides
# DEV_AGENT_CMD for this invocation (e.g. vibe agent -a codex). Either variant
# gets its own tmux session so it never reattaches to the default warm one.
cold=0
agent_override=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --cold)
      cold=1
      shift
      ;;
    -a | --agent)
      if [ "$#" -lt 2 ]; then
        echo "Usage: vibe agent [--cold] [-a|--agent COMMAND] [ARGUMENT ...]" >&2
        exit 2
      fi
      agent_override="$2"
      shift 2
      ;;
    *)
      break
      ;;
  esac
done

session="${DEV_AGENT_TMUX_SESSION:-agent}"
set -f # split DEV_AGENT_CMD/-a on spaces without expanding globs (e.g. *)
if [ -n "$agent_override" ]; then
  # shellcheck disable=SC2206  # word-splitting the command string is the point
  agent_cmd=($agent_override)
  session="$session-${agent_cmd[0]}"
else
  # shellcheck disable=SC2206
  agent_cmd=(${DEV_AGENT_CMD:-claude})
fi
set +f

if [ "$cold" = "1" ]; then
  case "${agent_cmd[0]}" in
    claude) agent_cmd+=(--safe-mode) ;;
    codex) agent_cmd+=(-c project_doc_max_bytes=0) ;;
    *)
      echo "vibe agent --cold: no known instruction-skip flags for: ${agent_cmd[0]}" >&2
      exit 2
      ;;
  esac
  session="$session-cold"
fi

# With DEV_AGENT_TMUX=1 the agent runs inside a tmux session: it survives a
# dropped terminal, and a second `vibe agent` reattaches (-A) instead of
# double-launching. `.env` still loads only inside the pane process via
# env-run.sh — never into the tmux server or an interactive shell. tmux takes
# the pane command as a shell string, hence the one remaining %q re-quote.
cmd=("$script_dir/env-run.sh" "${agent_cmd[@]}" "$@")
if [ "${DEV_AGENT_TMUX:-0}" = "1" ] && [ -z "${TMUX:-}" ]; then
  exec tmux new-session -A -s "$session" "$(printf "%q " "${cmd[@]}")"
fi
exec "${cmd[@]}"

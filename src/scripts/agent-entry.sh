#!/usr/bin/env bash
#
# Container-side entry for `vibe agent` and `vibe attach`. The host launcher
# used to inline this logic as single-quoted `bash -lc` payloads with
# positional smuggling and printf %q re-quoting — the most edit-hazardous
# code in the repo. As a real script it receives real argv straight from
# `docker exec`; the only quoting layer left is the tmux command
# string, isolated at the bottom. Container-side only: bash 4+ is fine.

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh disable=SC1091
source "$script_dir/lib.sh" # config.env + DEV_* defaults + REPO_ROOT
cd -- "$REPO_ROOT"

mode="${1:-}"
shift || true

case "$mode" in
  attach)
    # Door into the services session `vibe-svc` populates (one window per
    # service, stood up by the project's post-start hook). Session name:
    # argument > DEV_ATTACH_TMUX_SESSION > "services" — the same resolution
    # vibe-svc uses, so the door and the populater always agree.
    session="${1:-${DEV_ATTACH_TMUX_SESSION:-services}}"
    exec tmux -u new-session -A -s "$session"
    ;;
  agent) ;;
  reap-nested)
    # `vibe tui --kill/--fresh` cleanup, not a user surface: a host-side
    # pane death orphans its docker-exec tmux client in here (docker never
    # kills the exec'd process with its client), leaving ghost viewers
    # that inflate attached counts and outlive the UI. At kill time every
    # VIBE_NESTED client is dead by definition — the UI that spawned them
    # is gone — and plain `vibe agent` tabs never carry the marker, so
    # they are never touched. Only detaches clients: agents keep running.
    tmux list-clients -F '#{client_pid} #{client_tty}' 2>/dev/null |
      while read -r cpid ctty; do
        [ -n "$cpid" ] || continue
        if tr '\0' '\n' <"/proc/$cpid/environ" 2>/dev/null | grep -qx 'VIBE_NESTED=1'; then
          tmux detach-client -t "$ctty" 2>/dev/null || true
        fi
      done
    exit 0
    ;;
  *)
    echo "Usage: agent-entry.sh agent|attach [ARGUMENT ...]" >&2
    exit 2
    ;;
esac

# --cold starts the agent without repo instruction files (CLAUDE.md/AGENTS.md
# and .claude customizations) for an unbiased session. -a/--agent overrides
# DEV_AGENT_CMD for this invocation (e.g. vibe agent -a codex). -s/--session
# NAME runs a parallel instance in its own session (agent-NAME): without it,
# -A reattaches the one default session — persistence by design, but under
# `vibe tui` "another agent" usually means another AGENT. Every variant gets
# its own tmux session, own identity, own state dot, own `vibe ps` row.
cold=0
agent_override=""
session_suffix=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --cold)
      cold=1
      shift
      ;;
    -a | --agent)
      if [ "$#" -lt 2 ]; then
        echo "Usage: vibe agent [--cold] [-a|--agent COMMAND] [-s|--session NAME] [ARGUMENT ...]" >&2
        exit 2
      fi
      agent_override="$2"
      shift 2
      ;;
    -s | --session)
      if [ "$#" -lt 2 ] || [ -z "$2" ]; then
        echo "Usage: vibe agent [--cold] [-a|--agent COMMAND] [-s|--session NAME] [ARGUMENT ...]" >&2
        exit 2
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

[ -n "$session_suffix" ] && session="$session-$session_suffix"

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

# Identity for the agent-state hook (BACKLOG "agent state at a glance"):
# SESSION is the stable logical name, INSTANCE is unique per run so a
# restarted agent can never inherit a previous run's state records. The
# `env` prefix lives inside the ONE cmd array, so the tmux %q path and the
# direct-exec path below cannot disagree; on -A reattach the fresh mint is
# discarded with the unused command string and the pane's original run
# keeps its identity — that run is what the state records describe.
#
# CARRIER tells the hook whether this identity's run lives inside the inner
# tmux session named $session — computed here from the SAME condition that
# picks the tmux branch below. The hook needs it because background/daemon
# forks of an agent (Claude fork-session jobs) inherit the identity env but
# not $TMUX: with carrier=tmux their events may still drive the title
# channel (the state dot), while DEV_AGENT_TMUX=0 runs stay carrier=none
# and can never stomp the title of an unrelated same-named tmux session.
carrier=none
if [ "${DEV_AGENT_TMUX:-0}" = "1" ] && [ -z "${TMUX:-}" ]; then
  carrier=tmux
fi
cmd=(env "VIBE_AGENT_SESSION=$session" "VIBE_AGENT_INSTANCE=$$.$(date +%s)" "VIBE_AGENT_CARRIER=$carrier")

# With DEV_AGENT_TMUX=1 the agent runs inside a tmux session: it survives a
# dropped terminal, and a second `vibe agent` reattaches (-A) instead of
# double-launching. `.env` still loads only inside the pane process via
# env-run.sh — never into the tmux server or an interactive shell. tmux takes
# the pane command as a shell string, hence the one remaining %q re-quote.
# The two %q sites below plus svc.sh's are the sanctioned tmux shell-string
# boundary — do not add another. (`new-session -e` was considered and
# rejected: session env would leak the identity vars into every future pane,
# letting a manually launched agent there impersonate this run's instance.)
cmd+=("$script_dir/env-run.sh" "${agent_cmd[@]}" "$@")
if [ "$carrier" = "tmux" ]; then
  # Under `vibe tui` (VIBE_NESTED=1, forwarded by cexec) an outer host tmux
  # already draws tabs and chrome, and THIS session is an engine, not a UI
  # (persistence + scrollback) — so drop its status bar AND its prefix.
  # With a live prefix + status off, C-b c created windows no chrome
  # anywhere could show (live report, 2026-07-22); prefix None makes C-b
  # pass through to the agent, while wheel scrollback (mouse copy-mode)
  # is untouched. Chained after new-session so it also applies when -A
  # reattaches a session created the other way; the non-nested branch
  # symmetrically resets both (-u = back to server defaults), so a
  # session born nested is fully drivable from a plain terminal.
  # Name the window after the agent binary (-n also turns off
  # automatic-rename for it): nothing nested shows inner names, but
  # `tmux list-windows -a` reading "claude | preview | codex" beats three
  # windows all named after the env-run wrapper ("bash"). Ignored on -A
  # reattach, like every other creation flag.
  win_name="${agent_cmd[0]##*/}"
  if [ "${VIBE_NESTED:-0}" = "1" ]; then
    # Explicit -t: without it the chained command binds to whatever session
    # tmux considers current, not necessarily the one just created. Plain
    # name, no "=" — set-option's -t is a pane target, which rejects the
    # exact-match prefix (verified on 3.7b); exact names win over prefix
    # matches, and these session names are harness-controlled anyway.
    exec tmux new-session -A -s "$session" -n "$win_name" "$(printf "%q " "${cmd[@]}")" \; \
      set-option -t "$session" status off \; \
      set-option -t "$session" prefix None
  fi
  exec tmux new-session -A -s "$session" -n "$win_name" "$(printf "%q " "${cmd[@]}")" \; \
    set-option -t "$session" -u status \; \
    set-option -t "$session" -u prefix
fi
exec "${cmd[@]}"

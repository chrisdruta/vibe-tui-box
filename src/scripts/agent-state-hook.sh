#!/usr/bin/env bash
#
# Claude Code hook: agent state for the vibe tui status line and `vibe ps`
# (BACKLOG "agent state at a glance"). Wired via templates/claude-settings.json
# with the EVENT NAME AS ARGV — every registration knows its own event, so the
# hot path (this fires on every tool use) never spawns jq. env-run.sh also
# calls it directly with the pseudo-event `__exit` from its exit trap: process
# death is the one transition no Claude hook can report, and it must dominate
# whatever semantic state was last written.
#
# Two outputs per event, both best-effort and both container-side:
#   1. A state record in ${XDG_RUNTIME_DIR:-/tmp}/vibe-agent-state-<uid>/,
#      one file per agent session, read by `vibe ps` (runtime tmpfs only —
#      never the workspace, never the agent-state volume).
#   2. The title channel: `set-titles-string` on this agent's inner tmux
#      session. The inner server re-emits it as an OSC title through the
#      docker-exec TTY, the host `vibe tui` server sees its pane title
#      change, and its pane-title-changed hook renders the dot — the
#      validated no-polling bridge (see BACKLOG). Encoding:
#      vibe1|<project>|<session>|<instance>|<state>
#
# States are deliberately conservative (sol review): working / attention /
# idle / exited. Notification means "wants a human" (permission prompt,
# question), NOT blocked-for-sure; Stop means the turn ended, NOT done.
#
# Hook contract: stdout stays EMPTY (UserPromptSubmit stdout is injected
# into model context); always exit 0 — state is cosmetic, the agent is not.
set -uo pipefail

event="${1:-}"
[ -t 0 ] || cat >/dev/null 2>&1 || true # drain the unused JSON payload

# No identity = not a harness-launched agent run (or a pre-identity pin):
# nothing to key records by, so stay a silent no-op.
session="${VIBE_AGENT_SESSION:-}"
instance="${VIBE_AGENT_INSTANCE:-}"
[ -n "$session" ] && [ -n "$instance" ] || exit 0

case "$event" in
  SessionStart) state=idle ;;
  UserPromptSubmit) state=working ;;
  PostToolUse) state=working ;; # clears `attention` once a prompt is approved
  Notification) state=attention ;;
  Stop) state=idle ;;
  SessionEnd) state=exited ;;
  __exit) state=exited ;;
  *) exit 0 ;;
esac

state_dir="${XDG_RUNTIME_DIR:-/tmp}/vibe-agent-state-$(id -u)"
state_file="$state_dir/$session"

# Straggler guard: instances are <pid>.<epoch> (agent-entry.sh); if the
# record already belongs to a LATER mint, this event is from a superseded
# run (e.g. the old run's exit trap firing after a restart) — drop it.
mint="${instance##*.}"
case "$mint" in '' | *[!0-9]*) mint=0 ;; esac
if [ -r "$state_file" ]; then
  read -r _ cur_instance _ <"$state_file" 2>/dev/null || cur_instance=""
  cur_mint="${cur_instance##*.}"
  case "$cur_mint" in '' | *[!0-9]*) cur_mint=0 ;; esac
  [ "$cur_mint" -gt "$mint" ] && exit 0
fi

# Atomic write; VIBE_AGENT_EXIT rides in from the env-run.sh trap.
mkdir -p "$state_dir" 2>/dev/null || exit 0
record="$state $instance $(date +%s) ${VIBE_AGENT_EXIT:-}"
{ printf '%s\n' "$record" >"$state_file.tmp.$$" &&
  mv -f "$state_file.tmp.$$" "$state_file"; } 2>/dev/null || true

# Title channel — only when this identity's run lives in the inner tmux:
# either $TMUX is inherited from the pane process, or agent-entry.sh minted
# VIBE_AGENT_CARRIER=tmux alongside the identity. The carrier covers
# background/daemon fork-sessions (they inherit the identity env but not
# $TMUX — without it their events reached the state file but never the
# dot). Guarding on carrier (not just has-session) keeps a
# DEV_AGENT_TMUX=0 run from stomping the title of an unrelated tmux
# session that happens to share the name.
[ -n "${TMUX:-}" ] || [ "${VIBE_AGENT_CARRIER:-}" = "tmux" ] || exit 0
command -v tmux >/dev/null 2>&1 || exit 0

# Project field from the per-checkout identity, sanitized: the title string
# transits terminals as an OSC payload and is parsed host-side — keep it to
# a safe charset and bounded length. session/instance are harness-minted.
# lib-core anchors VIBE_DIR on this script's location, not the cwd — and
# stays hook-safe (no git, no config.env; lib.sh never belongs in hooks).
# shellcheck source=lib-core.sh disable=SC1091
. "$(dirname -- "${BASH_SOURCE[0]}")/lib-core.sh"
proj="$({ tr -cd 'A-Za-z0-9._-' <"$VIBE_DIR/.project-id" | head -c 48; } 2>/dev/null)"
[ -n "$proj" ] || proj="$(basename "${CLAUDE_PROJECT_DIR:-$PWD}" | tr -cd 'A-Za-z0-9._-' | head -c 48)"

# Plain -t name, no "=" prefix: 3.7b set-option -t takes a pane-style
# target and rejects exact-match syntax; exact names beat prefixes anyway.
tmux set-option -t "$session" set-titles on \; \
  set-option -t "$session" set-titles-string "vibe1|$proj|$session|$instance|$state" \
  >/dev/null 2>&1 || true
exit 0

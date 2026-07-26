#!/usr/bin/env bash
#
# Claude Code hook: agent state for the vibe tui dots and `vibe ps`
# (docs/architecture.md (agent sessions); the v2 port of v1's hook). Wired via
# claude-settings.json with the EVENT NAME AS ARGV — every registration
# knows its own event, so the hot path (this fires on every tool use)
# never spawns jq. agent-session.sh's run mode also calls it directly
# with the pseudo-event `__exit` from its exit trap: process death is
# the one transition no Claude hook can report, and it must dominate
# whatever semantic state was last written.
#
# Two outputs per event, both best-effort and both container-side:
#   1. A state record in the runtime records dir (state-dir.sh owns the
#      derivation), one file per agent session, read by `vibe ps`
#      (runtime tmpfs only — never the workspace, never the agent-state
#      volume).
#   2. The title channel: `set-titles-string` on this agent's inner tmux
#      session. The inner server re-emits it as an OSC title through the
#      docker-exec TTY, the host `vibe tui` server sees its pane title
#      change, and its pane-title-changed hook renders the dot — the
#      validated no-polling bridge. Encoding:
#      vibe1|<project>|<session>|<instance>|<state>|<display>|<model>
#      (display: the truth label agent-session.sh mints beside the
#      session address (VIBE_AGENT_DISPLAY, e.g. "claude:review") — the
#      host renames the viewer window to it, so tabs and roster say
#      "claude", never "agent"; model: statusline-fed sidecar file,
#      empty for CLIs without a statusline hook. Both may be empty —
#      field POSITIONS are fixed, and pre-display artifacts simply
#      emitted five fields.)
#
# States are deliberately conservative: working / attention / idle /
# exited. Notification means "wants a human" (permission prompt,
# question), NOT blocked-for-sure; Stop means the turn ended, NOT done.
#
# Hook contract: stdout stays EMPTY (UserPromptSubmit stdout is injected
# into model context); always exit 0 — state is cosmetic, the agent is
# not.
set -uo pipefail

event="${1:-}"
[ -t 0 ] || cat >/dev/null 2>&1 || true # drain the unused JSON payload

# No identity = not a harness-launched agent run: nothing to key records
# by, so stay a silent no-op.
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

# The records dir contract lives in state-dir.sh (BASH_SOURCE, not $0
# — the agent harness invokes this hook, so $0 is its to set).
case "${BASH_SOURCE[0]}" in */*) here="${BASH_SOURCE[0]%/*}" ;; *) here=. ;; esac
# shellcheck source=state-dir.sh disable=SC1091
. "$here/state-dir.sh"
state_dir="$VIBE_STATE_DIR"
state_file="$state_dir/$session"

# Straggler guard: instances are <pid>.<epoch> (agent-session.sh); if the
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

# Atomic write; VIBE_AGENT_EXIT rides in from the run-mode trap.
mkdir -p "$state_dir" 2>/dev/null || exit 0
printf -v now '%(%s)T' -1
record="$state $instance $now ${VIBE_AGENT_EXIT:-}"
{ printf '%s\n' "$record" >"$state_file.tmp.$$" &&
  mv -f "$state_file.tmp.$$" "$state_file"; } 2>/dev/null || true

# Title channel — only when this identity's run lives in the inner tmux:
# either $TMUX is inherited from the pane process, or agent-session.sh
# minted VIBE_AGENT_CARRIER=tmux alongside the identity. The carrier
# covers background/daemon fork-sessions (they inherit the identity env
# but not $TMUX — without it their events reached the state file but
# never the dot). Guarding on carrier (not just has-session) keeps a
# direct-exec run from stomping the title of an unrelated tmux session
# that happens to share the name.
[ -n "${TMUX:-}" ] || [ "${VIBE_AGENT_CARRIER:-}" = "tmux" ] || exit 0
command -v tmux >/dev/null 2>&1 || exit 0

# Project field from the engine-injected identity (VIBE_PROJECT_NAME,
# frozen into the exec env — container-side scripts never parse
# workspace files for identity), sanitized: the title string transits
# terminals as an OSC payload and is parsed host-side — keep it to a
# safe charset and bounded length (pure-bash scrubs — this fires on
# every tool use). session/instance are harness-minted.
proj="${VIBE_PROJECT_NAME:-}"
proj="${proj//[^A-Za-z0-9._-]/}"
if [ -z "$proj" ]; then
  proj="${CLAUDE_PROJECT_DIR:-$PWD}"
  proj="${proj##*/}"
  proj="${proj//[^A-Za-z0-9._-]/}"
fi
proj="${proj:0:48}"

# Truth fields: the display label minted beside the session address
# (VIBE_AGENT_DISPLAY — agent-session.sh owns the grammar; identities
# minted before it fall back to the bare CLI name), and the model the
# statusline sidecar recorded (statusline.sh — the one place the CLI
# reports it). Same sanitize-and-bound rule as proj; model keeps
# spaces ("Fable 5"). Empty is fine — positions are fixed.
disp="${VIBE_AGENT_DISPLAY:-${VIBE_AGENT_CMD:-}}"
disp="${disp//[^A-Za-z0-9:._-]/}"
disp="${disp:0:32}"
model=""
if [ -r "$state_dir/$session.model" ]; then
  IFS= read -r model <"$state_dir/$session.model" || true
fi
model="${model//[^A-Za-z0-9 ._-]/}"
model="${model:0:32}"

# Exact-match targeting. 3.7b's set-option rejects "=" exact syntax,
# and a plain -t NAME PREFIX-matches once the exact session is gone —
# and stop/restart guarantee that: their kill fires this hook from the
# run-mode EXIT trap, and `-t agent` would then stamp a live sibling
# like agent-codex as exited. Resolve the unique session ID with an
# exact filter (no prefix fallback — verified on 3.7b) and stamp THAT;
# a gone session resolves to nothing and nothing is stamped.
sid="$(tmux list-sessions -f "#{==:#{session_name},$session}" -F '#{session_id}' 2>/dev/null)" || sid=""
sid="${sid%%$'\n'*}" # exact filter yields at most one; belt for a duplicate name
[ -n "$sid" ] || exit 0
tmux set-option -t "$sid" set-titles on \; \
  set-option -t "$sid" set-titles-string "vibe1|$proj|$session|$instance|$state|$disp|$model" \
  >/dev/null 2>&1 || true
exit 0

#!/usr/bin/env bash
#
# The attach-only agent viewer spawn — ONE definition serving both
# doors of the agent-surfaces contract (docs/tui-layout.md): the tray's
# ghost cells (MouseDown1Status range dispatch, `ghost-N`) and the
# sidebar's viewer-less rows (sidebar.sh click, target
# `SESSION:agent-NAME`). A container-side agent session that has no
# window on this server gets one, in the project's own tmux session.
#
#   agent-open.sh CLIENT SESSION NAME
#   agent-open.sh CLIENT SESSION -g INDEX
#
# ATTACH-ONLY by contract: the window runs `vibe attach NAME`, which
# reattaches the inner tmux session by its full address. It never
# starts an agent and never restarts one — `vibe agent` (palette,
# prefix+Space) is the door that launches, and the distinction is the
# whole reason a ghost is safe to click. NAME is the FULL inner session
# name the `vibe ps` join reported (agent(-cmd)(-name)(-cold)); the
# `vibe agent -s` suffix grammar cannot address the default `agent`
# session or an `-a` variant, so it is the wrong door here.
#
# The sidebar's click map hands the NAME whole (an option value, no
# length cliff). The tray's ghost cells hand -g INDEX instead: a tmux
# status range name lives in a 16-byte buffer, so a name-carrying
# range silently truncates — "agent-agent-codex" once arrived as
# "agent-cod" and attach minted a junk session by that name. The index
# resolves through the session's @vibe_ghost_map, published by
# sidebar.sh from the same frame render that drew the cells.
#
# The name reaches this script through an engine-rendered click map or
# the engine-published map option, already allowlisted — it is
# re-checked below anyway, since it becomes a shell word in the window
# command.
#
# Host-side: bash-3.2-safe (stock macOS). Runs under the vibe server via
# run-shell, which provides TMUX pointing at that server.
set -u

client="${1:-}"
sess="${2:-}"
name="${3:-}"
[ -n "$name" ] || exit 0

exe="$(tmux show-options -gqv @vibe_exe 2>/dev/null)"
{ [ -n "$exe" ] && [ -x "$exe" ]; } || exit 0

# The tray dispatch knows the client but not the session id; the
# sidebar click knows both. Resolve the hole from the client.
if [ -z "$sess" ]; then
  if [ -n "$client" ]; then
    sess="$(tmux display-message -p -t "$client" '#{session_id}' 2>/dev/null)"
  else
    sess="$(tmux display-message -p '#{session_id}' 2>/dev/null)"
  fi
fi
[ -n "$sess" ] || exit 0

# Ghost-cell form: resolve the range's index through the session's
# published name table. A stale index (the roster changed between the
# frame render and the click) falls off the end and does nothing —
# never a guessed name.
if [ "$name" = "-g" ]; then
  idx="${4:-}"
  case "$idx" in "" | *[!0-9]*) exit 0 ;; esac
  gmap="$(tmux show-options -t "$sess" -qv @vibe_ghost_map 2>/dev/null)"
  # shellcheck disable=SC2086  # word-splitting the map IS the parse
  set -- $gmap
  [ "$idx" -lt "$#" ] || exit 0
  shift "$idx"
  name="$1"
fi
case "$name" in
  "" | *[!A-Za-z0-9_-]*) exit 0 ;; # never a shell word we did not vet
esac

# The workspace is where `vibe attach` resolves the project from.
path="$(tmux display-message -p -t "$sess" '#{session_path}' 2>/dev/null)"
[ -n "$path" ] || exit 0

# Bring the client over first, then create: new-window makes the fresh
# window current in its session, so the viewer lands in front of the
# operator either way. Stamp @vibe_session at birth — the join key the
# ghost/chooser dedup reads. Waiting for the agent's own title events
# to stamp it leaves hookless CLIs (codex, a bare shell) invisible to
# the dedup forever: the ghost never clears and every click grows
# another viewer.
[ -n "$client" ] && tmux switch-client -c "$client" -t "$sess" 2>/dev/null
wid="$(tmux new-window -t "$sess" -c "$path" -n "$name" -P -F '#{window_id}' "exec '$exe' attach '$name'" 2>/dev/null)"
[ -n "$wid" ] && tmux set-option -w -t "$wid" @vibe_session "$name" 2>/dev/null
exit 0

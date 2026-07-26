#!/usr/bin/env bash
#
# The attach-only agent viewer spawn — ONE definition serving both
# doors of the agent-surfaces contract (docs/tui-layout.md): the tray's
# ghost cells (MouseDown1Status range dispatch, `agent-NAME`) and the
# sidebar's viewer-less rows (sidebar.sh click, target
# `SESSION:agent-NAME`). A container-side agent session that has no
# window on this server gets one, in the project's own tmux session.
#
#   agent-open.sh CLIENT SESSION NAME
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
# The name reaches this script through an engine-rendered mouse range or
# click map, already allowlisted — it is re-checked below anyway, since
# it becomes a shell word in the window command.
#
# Host-side: bash-3.2-safe (stock macOS). Runs under the vibe server via
# run-shell, which provides TMUX pointing at that server.
set -u

client="${1:-}"
sess="${2:-}"
name="${3:-}"
[ -n "$name" ] || exit 0
case "$name" in
  *[!A-Za-z0-9_-]*) exit 0 ;; # never a shell word we did not vet
esac

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

# The workspace is where `vibe attach` resolves the project from.
path="$(tmux display-message -p -t "$sess" '#{session_path}' 2>/dev/null)"
[ -n "$path" ] || exit 0

# Bring the client over first, then create: new-window makes the fresh
# window current in its session, so the viewer lands in front of the
# operator either way.
[ -n "$client" ] && tmux switch-client -c "$client" -t "$sess" 2>/dev/null
tmux new-window -t "$sess" -c "$path" -n "$name" "exec '$exe' attach '$name'" 2>/dev/null
exit 0

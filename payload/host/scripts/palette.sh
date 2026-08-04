#!/usr/bin/env bash
# The vibe palette — ONE definition serving both doors: prefix+Space
# and the tray's 🥡 cell (MouseDown1Status range dispatch in
# tmux-tui.conf; the + cell opens the agents chooser, chooser.sh).
# Extracted from the conf so the menu cannot drift between them;
# display-menu needs an explicit client when invoked from run-shell
# (no current client in that context), hence $1.
#
# Command strings keep their #{...} constructs LITERAL — display-menu
# expands them against the choosing client's context when an item is
# picked, exactly as the old inline conf menu did. The engine binary
# and payload dir resolve through the stamped globals, never argv.
set -euo pipefail

# Positional params, not an array: "$@" expands empty-safely under
# set -u on bash 3.2 (the host pledge), where an empty "${arr[@]}"
# does not.
client="${1:-}"
set --
[ -n "$client" ] && set -- -c "$client"

# -M -O is the only working flag pair for a script-opened menu
# (chooser.sh documents the matrix; tui-layout.md "Launch surfaces"
# records the mechanism): without -M the CLI-opened menu is NOMOUSE
# (clicks swallowed, any release closes it); -M alone dies on the
# first pointer motion outside the box (bare motion is release-coded);
# -M -O lets motion aim and a press fire. No position flags: the
# palette is the full command surface, and display-menu's default is
# centered both axes — the look it always had (a -y S here once
# bottom-anchored this tall menu and read as broken centering; the
# tray-button chooser is the menu that anchors above the tray). The
# tray door opens on MouseUp so the opening click's release is already
# spent. The bare "agent" item is retired for the agents chooser: its
# label promised "new" while `vibe agent`'s -A semantics delivered
# attach-or-launch — a second viewer on the same inner session when
# one was already running. "default" in the stop/restart labels is
# load-bearing (2026-07-28): those verbs address only the bare `agent`
# session — per-session stop lives on right-click (agent-menu.sh),
# launch variants in the chooser.
exec tmux display-menu "$@" -M -O -T " vibe " \
  "agents" a "run-shell -b \"exec bash '#{@vibe_payload_dir}/scripts/chooser.sh' '#{client_name}'\"" \
  "restart default agent" r "new-window -c \"#{session_path}\" -n agent \"'#{@vibe_exe}' agent --restart\"" \
  "stop default agent" x "run-shell -b \"exec bash '#{@vibe_payload_dir}/scripts/popup.sh' -w 70% -h 45% '#{client_name}' '#{@vibe_exe}' agent --stop\"" \
  "container shell" s "new-window -c \"#{session_path}\" -n shell \"'#{@vibe_exe}' shell\"" \
  "attach main proc" e "new-window -c \"#{session_path}\" -n attach \"'#{@vibe_exe}' attach\"" \
  "host shell" h "new-window -c \"#{session_path}\"" \
  "" \
  "project sidebar" b "run-shell -b \"bash '#{@vibe_payload_dir}/scripts/sidebar.sh' toggle '#{window_id}'\"" \
  "host dock" t "run-shell -b \"bash '#{@vibe_payload_dir}/scripts/dock.sh' '#{window_id}'\"" \
  "clip image → agent" v "run-shell -b \"bash '#{@vibe_payload_dir}/scripts/clip-to-pane.sh' '#{window_id}' '#{client_name}'\"" \
  "projects (live)" o "choose-tree -Zs" \
  "files (nvim)" f "run-shell -b \"exec bash '#{@vibe_payload_dir}/scripts/review.sh' '#{client_name}' '#{@vibe_exe}' '#{session_path}' files\"" \
  "git (lazygit)" g "run-shell -b \"exec bash '#{@vibe_payload_dir}/scripts/review.sh' '#{client_name}' '#{@vibe_exe}' '#{session_path}' git\"" \
  "requests" u "run-shell -b \"exec bash '#{@vibe_payload_dir}/scripts/popup.sh' '#{client_name}' '#{@vibe_exe}' request list\"" \
  "ps · all projects" p "run-shell -b \"exec bash '#{@vibe_payload_dir}/scripts/popup.sh' '#{client_name}' '#{@vibe_exe}' ps\"" \
  "network egress" E "run-shell -b \"exec bash '#{@vibe_payload_dir}/scripts/popup.sh' '#{client_name}' '#{@vibe_exe}' _egress\"" \
  "doctor" D "run-shell -b \"exec bash '#{@vibe_payload_dir}/scripts/popup.sh' '#{client_name}' '#{@vibe_exe}' doctor\"" \
  "" \
  "detach (keep running)" d "detach-client" \
  "park project (down + quit)" z "run-shell -b \"exec bash '#{@vibe_payload_dir}/scripts/popup.sh' -w 70% -h 45% '#{client_name}' '#{@vibe_exe}' down\"" \
  "quit ui" Q "vibe-quit-ui" \
  "kill ui server (ALL)" K "vibe-kill-server"

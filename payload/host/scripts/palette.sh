#!/usr/bin/env bash
# The vibe palette — ONE definition serving both doors: prefix+Space
# and the tray's 🥡 / + cells (MouseDown1Status range dispatch in
# tmux-tui.conf). Extracted from the conf so the menu cannot drift
# between them; display-menu needs an explicit client when invoked from
# run-shell (no current client in that context), hence $1.
#
# Command strings keep their #{...} constructs LITERAL — display-menu
# expands them against the choosing client's context when an item is
# picked, exactly as the old inline conf menu did. The engine binary
# and payload dir resolve through the stamped globals, never argv.
set -euo pipefail

client="${1:-}"
target=()
[ -n "$client" ] && target=(-c "$client")

exec tmux display-menu "${target[@]}" -T " vibe " \
  "agent" a "new-window -c \"#{session_path}\" -n agent \"'#{@vibe_exe}' agent\"" \
  "restart agent" r "new-window -c \"#{session_path}\" -n agent \"'#{@vibe_exe}' agent --restart\"" \
  "stop agent" x "run-shell -b \"exec bash '#{@vibe_payload_dir}/scripts/popup.sh' -w 70% -h 40% '#{client_name}' '#{@vibe_exe}' agent --stop\"" \
  "container shell" s "new-window -c \"#{session_path}\" -n shell \"'#{@vibe_exe}' shell\"" \
  "attach main proc" e "new-window -c \"#{session_path}\" -n attach \"'#{@vibe_exe}' attach\"" \
  "host shell" h "new-window -c \"#{session_path}\"" \
  "" \
  "project sidebar" b "run-shell -b \"bash '#{@vibe_payload_dir}/scripts/sidebar.sh' toggle '#{window_id}'\"" \
  "host dock" t "run-shell -b \"bash '#{@vibe_payload_dir}/scripts/dock.sh' '#{window_id}'\"" \
  "clip image → agent" v "run-shell -b \"bash '#{@vibe_payload_dir}/scripts/clip-to-pane.sh' '#{window_id}'\"" \
  "switch project" o "choose-tree -Zs" \
  "files (nvim)" f "run-shell -b \"exec bash '#{@vibe_payload_dir}/scripts/review.sh' '#{client_name}' '#{@vibe_exe}' '#{session_path}' files\"" \
  "git (lazygit)" g "run-shell -b \"exec bash '#{@vibe_payload_dir}/scripts/review.sh' '#{client_name}' '#{@vibe_exe}' '#{session_path}' git\"" \
  "review diff" G "run-shell -b \"exec bash '#{@vibe_payload_dir}/scripts/review.sh' '#{client_name}' '#{@vibe_exe}' '#{session_path}' review\"" \
  "requests" u "run-shell -b \"exec bash '#{@vibe_payload_dir}/scripts/popup.sh' '#{client_name}' '#{@vibe_exe}' request list\"" \
  "agents (vibe ps)" p "run-shell -b \"exec bash '#{@vibe_payload_dir}/scripts/popup.sh' '#{client_name}' '#{@vibe_exe}' ps\"" \
  "doctor" D "run-shell -b \"exec bash '#{@vibe_payload_dir}/scripts/popup.sh' '#{client_name}' '#{@vibe_exe}' doctor\"" \
  "" \
  "detach (keep running)" d "detach-client" \
  "park project (down + quit)" z "run-shell -b \"exec bash '#{@vibe_payload_dir}/scripts/popup.sh' -w 70% -h 45% '#{client_name}' '#{@vibe_exe}' down\"" \
  "quit ui" Q "vibe-quit-ui" \
  "kill ui server (ALL)" K "vibe-kill-server"

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
  "stop agent" x "display-popup -w 70% -h 40% -E \"'#{@vibe_exe}' agent --stop; printf '\\n[enter to close] '; read _\"" \
  "container shell" s "new-window -c \"#{session_path}\" -n shell \"'#{@vibe_exe}' shell\"" \
  "attach main proc" e "new-window -c \"#{session_path}\" -n attach \"'#{@vibe_exe}' attach\"" \
  "host shell" h "new-window -c \"#{session_path}\"" \
  "" \
  "project sidebar" b "run-shell -b \"bash '#{@vibe_payload_dir}/scripts/sidebar.sh' toggle '#{window_id}'\"" \
  "host dock" t "run-shell -b \"bash '#{@vibe_payload_dir}/scripts/dock.sh' '#{window_id}'\"" \
  "clip image → agent" v "run-shell -b \"bash '#{@vibe_payload_dir}/scripts/clip-to-pane.sh' '#{@vibe_payload_dir}' '#{window_id}'\"" \
  "switch project" o "choose-tree -Zs" \
  "git (lazygit)" g "display-popup -d \"#{session_path}\" -w 85% -h 85% -E \"lazygit || bash -l\"" \
  "requests" u "display-popup -w 85% -h 70% -E \"'#{@vibe_exe}' request list; printf '\\n[enter to close] '; read _\"" \
  "agents (vibe ps)" p "display-popup -w 85% -h 70% -E \"'#{@vibe_exe}' ps; printf '\\n[enter to close] '; read _\"" \
  "doctor" D "display-popup -w 85% -h 70% -E \"'#{@vibe_exe}' doctor; printf '\\n[enter to close] '; read _\"" \
  "" \
  "detach (keep running)" d "detach-client" \
  "park project (down + quit)" z "display-popup -w 70% -h 45% -E \"'#{@vibe_exe}' down; printf '\\n[enter to close] '; read _\"" \
  "quit ui" Q "confirm-before -p \"quit vibe tui? agents & containers keep running — palette: park project (y/n)\" kill-session" \
  "kill ui server (ALL)" K "confirm-before -p \"kill the whole vibe tui server? ALL projects' UI sessions end; agents keep running — 'vibe agent --stop' ends one (y/n)\" kill-server"

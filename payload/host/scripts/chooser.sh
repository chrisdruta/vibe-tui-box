#!/usr/bin/env bash
#
# The agents chooser — the tray `+` cell's menu and the palette's
# "agents" item (docs/tui-layout.md "Launch surfaces"): one entry per
# installed CLI, launch what's down, reach what's up, never a second
# viewer on a session that already has one. The reach-vs-launch
# verdicts live in the engine's `vibe _chooser` renderer (manifest
# truth + the `vibe ps` fetch cache — the same truth the tray's ghost
# cells read); this side only gathers the session's window porcelain,
# turns porcelain rows into display-menu items, and dispatches:
#
#   launch   new-window running `vibe agent` (the manifest default)
#   launcha  new-window running `vibe agent -a KIND`
#   attach   agent-open.sh (the attach-only viewer spawn, both doors')
#   jump     select-window to the existing viewer
#
# Item COMMAND strings keep their #{...} constructs LITERAL — like
# palette.sh, display-menu expands them against the choosing client's
# context when an item is picked. -M -O together are the ONLY working
# flag pair for a script-opened menu (tui-layout.md "Launch surfaces"
# records the mechanism, proven against the pinned tmux):
#   no -M   the CLI-opened menu is NOMOUSE — clicks swallowed,
#           any release closes it (keyboard-only).
#   -M      opens all-motion tracking, but a bare motion event is
#           release-coded (SGR 35 & MASK_BUTTONS == 3), so the first
#           pointer twitch outside the box closes the menu.
#   -M -O   stay-open: motion hovers and AIMS the choice, a press
#           fires the aimed item, a press outside closes. Correct —
#           motion always precedes a real pointer's press because the
#           menu itself turned 1003 on.
# -y S pins it above the tray. Porcelain fields become shell words and
# tmux targets, so every arg is charset-vetted here even though the
# engine already vetted them.
#
# Host-side: bash-3.2-safe (stock macOS). Runs under the vibe server
# via run-shell, which provides TMUX pointing at that server. Any
# missing prerequisite falls back to the palette — a dead chooser must
# never eat the tray's only launch door.
set -u

client="${1:-}"

pdir="$(tmux show-options -gqv @vibe_payload_dir 2>/dev/null)"
fallback() {
  [ -n "$pdir" ] || exit 0
  set --
  [ -n "$client" ] && set -- "$client"
  exec bash "$pdir/scripts/palette.sh" "$@"
}

exe="$(tmux show-options -gqv @vibe_exe 2>/dev/null)"
{ [ -n "$exe" ] && [ -x "$exe" ]; } || fallback

if [ -n "$client" ]; then
  sid="$(tmux display-message -p -t "$client" '#{session_id}' 2>/dev/null)"
else
  sid="$(tmux display-message -p '#{session_id}' 2>/dev/null)"
fi
[ -n "$sid" ] || fallback
proj="$(tmux display-message -p -t "$sid" '#{@vibe_project}' 2>/dev/null)"
[ -n "$proj" ] || fallback

# The fetch cache beside the socket (sidebar.sh's derivation). Missing
# is fine — the engine then renders launch verdicts, and `vibe agent`'s
# -A semantics keep them honest.
cache_dir=""
sock="$(tmux display-message -p '#{socket_path}' 2>/dev/null)"
[ -n "$sock" ] && cache_dir="${sock%/*}/vibe-tui-cache"

us=$'\x1f'
rows="$(tmux list-windows -t "$sid" -F "W${us}#{window_id}${us}#{@vibe_session}" 2>/dev/null |
  "$exe" _chooser --cache "$cache_dir" --project "$proj" 2>/dev/null)" || fallback
[ -n "$rows" ] || fallback

args=()
[ -n "$client" ] && args+=(-c "$client")
args+=(-M -O -y S -T " agents ")
while IFS="$us" read -r ver label key verb arg; do
  [ "$ver" = "1" ] || continue
  case "$verb" in
    launch | launcha)
      case "$arg" in "" | *[!A-Za-z0-9_-]*) continue ;; esac
      flag="" addr="agent"
      [ "$verb" = "launcha" ] && { flag=" -a $arg"; addr="agent-$arg"; }
      # Stamp @vibe_session at birth: the ghost/chooser dedup join key
      # must not wait on title events a hookless CLI never sends.
      cmd="new-window -c \"#{session_path}\" -n $arg \"'#{@vibe_exe}' agent$flag\" ; set-option -w @vibe_session $addr"
      ;;
    attach)
      case "$arg" in "" | *[!A-Za-z0-9_-]*) continue ;; esac
      cmd="run-shell -b \"bash '#{@vibe_payload_dir}/scripts/agent-open.sh' '#{client_name}' '#{session_id}' '$arg'\""
      ;;
    jump)
      case "$arg" in "@"*) ;; *) continue ;; esac
      case "${arg#@}" in "" | *[!0-9]*) continue ;; esac
      cmd="select-window -t '$arg'"
      ;;
    *) continue ;;
  esac
  args+=("$label" "$key" "$cmd")
done <<EOF
$rows
EOF
args+=("")
args+=("container shell" s "new-window -c \"#{session_path}\" -n shell \"'#{@vibe_exe}' shell\"")
args+=("host shell" h "new-window -c \"#{session_path}\"")

exec tmux display-menu "${args[@]}"

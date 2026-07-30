#!/usr/bin/env bash
#
# vibe tui working-dot animator (docs/tui-layout.md "The working
# spinner"). One per server: while ANY window's recorded @vibe_state is
# `working`, rotate the global @vibe_spin option through the theme's
# spinner frames at 4Hz; tmux repaints the status line on every option
# write by itself (measured on the pinned 3.7b: a bare `set -g` redraws
# the status, ~350 bytes, and ONLY the status — which is also why the
# pane-border dot deliberately never references @vibe_spin: borders
# don't repaint on option writes, and a frozen braille frame would read
# as broken). The tab dots and working ghost cells therefore animate
# with zero new #() splices and no status-interval change; the
# sidebar's rows animate separately (its own sub-tick overlay — the
# render loop owns that pane and never needs this option).
#
# Lifecycle: exits, restoring the static working dot, as soon as a
# check finds nothing working or the server is gone; the liveness check
# runs every 8th tick (~2s) so the idle cost of a finished agent is
# bounded and the hot loop stays one `set-option` per tick. Spawned by
# state-render.sh on every `working` event (the instant door) and by
# the sidebar loop whenever its frame carries spin cells (the healer —
# a viewer-less working agent has no title events on this server).
# Both doors fire blind: the noclobber lock beside the tmux socket
# makes redundant spawns exit in milliseconds, and a stale lock (dead
# pid) is reclaimed rather than wedging the animation forever.
#
# Host-side: bash-3.2-safe (stock macOS — no flock(1) here, hence the
# noclobber lock; the race it leaves open costs a duplicate animator
# writing the same frames, cosmetic by construction). Runs under the
# vibe server via run-shell or the sidebar pane, so plain `tmux` is
# always the right binary/socket.
set -u

case "$0" in */*) here="${0%/*}" ;; *) here="." ;; esac
# shellcheck source=theme.sh disable=SC1091
. "$here/theme.sh"

sock="$(tmux display-message -p '#{socket_path}' 2>/dev/null)" || exit 0
[ -n "$sock" ] || exit 0
lock="${sock%/*}/vibe-tui-spin.lock"

take_lock() {
  if (set -C; printf '%s\n' "$$" >"$lock") 2>/dev/null; then
    return 0
  fi
  pid=""
  IFS= read -r pid <"$lock" 2>/dev/null || :
  case "$pid" in
    '' | *[!0-9]*) ;;
    *) kill -0 "$pid" 2>/dev/null && return 1 ;;
  esac
  rm -f "$lock" 2>/dev/null
  (set -C; printf '%s\n' "$$" >"$lock") 2>/dev/null
}
take_lock || exit 0
trap 'rm -f "$lock" 2>/dev/null' EXIT

# The static working dot — the frame every surface falls back to when
# no animator runs, restored on the way out. From the theme's state
# map, not a literal: theme.go owns every glyph.
vibe_state_style working || exit 0
# shellcheck disable=SC2154  # vibe_glyph: set by vibe_state_style above
restore="$vibe_glyph"

# shellcheck disable=SC2206  # word-split ON PURPOSE: frames are space-separated
frames=($VIBE_SPIN_FRAMES)
n=${#frames[@]}
[ "$n" -gt 0 ] || exit 0
i=0
while :; do
  if [ $((i % 8)) -eq 0 ]; then
    states="$(tmux list-windows -a -F '#{@vibe_state}' 2>/dev/null)" || break
    case "$states" in *working*) ;; *) break ;; esac
  fi
  tmux set-option -g @vibe_spin "${frames[i % n]}" 2>/dev/null || break
  i=$((i + 1))
  sleep 0.25
done
tmux set-option -g @vibe_spin "$restore" 2>/dev/null
exit 0

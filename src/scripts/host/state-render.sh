#!/usr/bin/env bash
#
# vibe tui host renderer for the agent-state title channel (BACKLOG "agent
# state at a glance"). Invoked by the vibe server's hooks as:
#   state-render.sh PANE_ID            (pane-title-changed)
#   state-render.sh PANE_ID frontend-dead   (pane-died: mark the corpse)
# SECURITY: only the server-controlled pane id and a conf-supplied literal
# state ever reach argv. The pane title is container-controlled text, so it
# is fetched out-of-band here and never becomes host shell words (the
# injection rule the title-channel spike baked into the design; see BACKLOG).
#
# Input title encoding (written by the container-side agent-state-hook.sh):
#   vibe1|<project>|<session>|<instance>|<state>
# Output: data-only tmux user options; presentation lives in tmux-tui.conf.
#   pane   @vibe_state  raw state, @vibe_title (only if unset — keeps the
#                       raw encoding out of the pane border)
#   pane+window @vibe_glyph / @vibe_dot_fg  the pre-chosen dot + its color
#   window @vibe_attn   1 while the agent wants a human (tab flash)
#
# Host-side: must stay bash-3.2-safe (stock macOS). Runs under the vibe
# server via run-shell, which provides TMUX pointing at that server.
# shellcheck disable=SC2154  # vibe_glyph/vibe_state_hex: set by vibe_state_style
set -u

pane="${1:-}"
[ -n "$pane" ] || exit 0
forced="${2:-}"

# Palette + state map from theme.sh — no per-event tmux show-options round
# trip: scripts render from theme.sh, the conf renders from its @thm twin;
# same checkout, same values. ($0 is subprocess-free on this hot path.)
case "$0" in */*) here="${0%/*}" ;; *) here="." ;; esac
# shellcheck source=../../config/theme.sh disable=SC1091
. "$here/../../config/theme.sh"

dead="$(tmux display-message -p -t "$pane" '#{pane_dead}' 2>/dev/null)" || exit 0

if [ -n "$forced" ]; then
  # pane-died path: an agent pane's death here means the FRONTEND is gone
  # (the docker-exec client), not necessarily the agent — the inner tmux
  # session may well be alive. Mark it distinctly instead of trusting the
  # last state. Guards are the INVERSE of the title path: only a dead pane
  # takes a forced state, and only over an existing agent state (so host
  # shell panes never grow a dot).
  [ "$forced" = "frontend-dead" ] || exit 0
  [ "$dead" = "1" ] || exit 0
  [ -n "$(tmux show-options -pqv -t "$pane" @vibe_state 2>/dev/null)" ] || exit 0
  vibe_state_style "$forced" || exit 0
  tmux set-option -p -t "$pane" @vibe_state "$forced" \; \
    set-option -p -t "$pane" @vibe_glyph "$vibe_glyph" \; \
    set-option -p -t "$pane" @vibe_dot_fg "$vibe_state_hex" \; \
    set-option -w -t "$pane" @vibe_glyph "$vibe_glyph" \; \
    set-option -w -t "$pane" @vibe_dot_fg "$vibe_state_hex" \; \
    set-option -w -t "$pane" @vibe_attn 0 2>/dev/null
  exit 0
fi

# Liveness dominates semantic state (the layered-liveness rule): hook
# run-shell is async, so a queued title event can execute AFTER the pane
# died — never let it overwrite the pane-died hook's frontend-dead mark.
[ "$dead" = "1" ] && exit 0

title="$(tmux display-message -p -t "$pane" '#{pane_title}' 2>/dev/null)" || exit 0
case "$title" in
  "vibe1|"*) ;;
  *) exit 0 ;; # not an agent-state title — nothing to render
esac

IFS='|' read -r _ _proj session _instance state <<EOF
$title
EOF

# The title channel carries exactly these four states — anything else is a
# newer/older pin talking: render nothing rather than guess.
case "$state" in
  working | attention | idle | exited) ;;
  *) exit 0 ;;
esac
vibe_state_style "$state" || exit 0
attn=0
dot_fg="$vibe_state_hex"
if [ "$state" = "attention" ]; then
  # Tabs-presentation override, not a theme fact: the whole tab flashes
  # coral (conf), so the dot blends into that background.
  dot_fg="$VIBE_THM_BG"
  attn=1
fi

tmux set-option -p -t "$pane" @vibe_state "$state" \; \
  set-option -p -t "$pane" @vibe_glyph "$vibe_glyph" \; \
  set-option -p -t "$pane" @vibe_dot_fg "$dot_fg" \; \
  set-option -w -t "$pane" @vibe_glyph "$vibe_glyph" \; \
  set-option -w -t "$pane" @vibe_dot_fg "$dot_fg" \; \
  set-option -w -t "$pane" @vibe_attn "$attn" 2>/dev/null || exit 0

# Human label for the pane border: the border format prefers @vibe_title,
# so stamping the session name here keeps the raw vibe1|… encoding from
# ever showing. Never overwrite a label tui.sh/the palette already chose.
cur_title="$(tmux show-options -pqv -t "$pane" @vibe_title 2>/dev/null)"
[ -n "$cur_title" ] || tmux set-option -p -t "$pane" @vibe_title "$session" 2>/dev/null

exit 0

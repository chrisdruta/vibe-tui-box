#!/usr/bin/env bash
#
# vibe tui host renderer for the agent-state title channel
# (docs/architecture.md (agent sessions)). Invoked by the vibe server's hooks as:
#   state-render.sh PANE_ID                 (pane-title-changed)
#   state-render.sh PANE_ID frontend-dead   (pane-died: mark the corpse)
# SECURITY: only the server-controlled pane id and a conf-supplied
# literal state ever reach argv. The pane title is container-controlled
# text, so it is fetched out-of-band here and never becomes host shell
# words (the injection rule the title-channel spike baked into the
# design).
#
# Input title encoding (written by the container-side
# agent-state-hook.sh); display/model are absent from pre-truth
# artifacts and both may be empty — positions are fixed:
#   vibe1|<project>|<session>|<instance>|<state>|<display>|<model>
# Output: data-only tmux user options; presentation lives in
# tmux-tui.conf.
#   pane   @vibe_state  raw state, @vibe_title (the display name — the
#                       CLI actually running, never the raw encoding)
#   pane+window @vibe_glyph / @vibe_dot_fg  the pre-chosen dot + color
#   window @vibe_attn   1 while the agent wants a human (tab flash)
#   window @vibe_model  the statusline-fed model, roster's dim suffix
#   window NAME         renamed to the display — session names are the
#                       ADDRESS (stop/-s/-a target them), the window
#                       shows TRUTH ("claude", "codex:review"), so tabs
#                       and roster never read "agent"
#   server @vibe_state_serial  bumped on every write, riding the same
#                       server command — the sidebar's cheap change
#                       signal
#
# Host-side: must stay bash-3.2-safe (stock macOS). Runs under the vibe
# server via run-shell, which provides TMUX pointing at that server.
# shellcheck disable=SC2154  # vibe_glyph/vibe_state_hex: set by vibe_state_style
set -u

pane="${1:-}"
[ -n "$pane" ] || exit 0
forced="${2:-}"

# Palette + state map from theme.sh — no per-event tmux show-options
# round trip: scripts render from theme.sh, the conf renders from its
# @thm twin; same artifact, same values. ($0 is subprocess-free on this
# hot path.)
case "$0" in */*) here="${0%/*}" ;; *) here="." ;; esac
# shellcheck source=theme.sh disable=SC1091
. "$here/theme.sh"

info="$(tmux display-message -p -t "$pane" '#{pane_dead}|#{@vibe_title}' 2>/dev/null)" || exit 0
dead="${info%%|*}"
prev_title="${info#*|}"

if [ -n "$forced" ]; then
  # pane-died path: an agent pane's death here means the FRONTEND is
  # gone (the docker-exec client), not necessarily the agent — the
  # inner tmux session may well be alive. Mark it distinctly instead of
  # trusting the last state. Guards are the INVERSE of the title path:
  # only a dead pane takes a forced state, and only over an existing
  # agent state (so host shell panes never grow a dot).
  [ "$forced" = "frontend-dead" ] || exit 0
  [ "$dead" = "1" ] || exit 0
  [ -n "$(tmux show-options -pqv -t "$pane" @vibe_state 2>/dev/null)" ] || exit 0
  vibe_state_style "$forced" || exit 0
  tmux set-option -p -t "$pane" @vibe_state "$forced" \; \
    set-option -p -t "$pane" @vibe_glyph "$vibe_glyph" \; \
    set-option -p -t "$pane" @vibe_dot_fg "$vibe_state_hex" \; \
    set-option -w -t "$pane" @vibe_glyph "$vibe_glyph" \; \
    set-option -w -t "$pane" @vibe_dot_fg "$vibe_state_hex" \; \
    set-option -w -t "$pane" @vibe_attn 0 \; \
    set-option -g @vibe_state_serial "$$$RANDOM" 2>/dev/null
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

IFS='|' read -r _ _proj session _instance state display model _rest <<EOF
$title
EOF

# EVERY field here is container-controlled bytes: allowlist them ALL
# before any becomes a window name or option value (argv-only below,
# never shell words — but display surfaces still deserve a closed
# charset, and the roster does byte-width math on them). Pure bash —
# this runs per state event. The display itself is minted beside the
# session address in agent-session.sh and travels the channel whole;
# this script renders, it never re-derives the grammar.
display="${display//[^a-zA-Z0-9:._-]/}"
display="${display:0:32}"
model="${model//[^a-zA-Z0-9 ._-]/}"
model="${model:0:32}"
session="${session//[^a-zA-Z0-9_-]/}"
session="${session:0:40}"

# The title channel carries exactly these four states — anything else is
# a newer/older artifact talking: render nothing rather than guess.
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
  set-option -w -t "$pane" @vibe_attn "$attn" \; \
  set-option -w -t "$pane" @vibe_model "$model" \; \
  set-option -g @vibe_state_serial "$$$RANDOM" 2>/dev/null || exit 0

# Human labels: the window and border track the channel's display.
# Guard on the PANE's own settled label (@vibe_title, fetched with the
# dead check above), never the window name — two agent panes sharing a
# window would otherwise fight over it on every event from either
# side. Pre-display titles keep the old behavior: session name, only
# if nothing chose a label yet.
if [ -n "$display" ]; then
  [ "$prev_title" = "$display" ] || tmux rename-window -t "$pane" "$display" \; \
    set-option -p -t "$pane" @vibe_title "$display" 2>/dev/null
else
  [ -n "$prev_title" ] || tmux set-option -p -t "$pane" @vibe_title "$session" 2>/dev/null
fi

exit 0

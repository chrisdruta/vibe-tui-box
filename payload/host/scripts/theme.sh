# shellcheck shell=bash
#
# GENERATED from internal/tmuxui/theme.go — do not edit. Change the Go
# source and run `go generate ./internal/payload`; CI fails on
# a stale rendering (payload manifest drift). The @thm_* block in
# tmux-tui.conf renders from the same source.
#
# vibe theme — the ONE palette + state map for every script renderer:
# sidebar.sh (fleet + agent roster), state-render.sh (dot + state
# colors), spin.sh (the working-dot spinner).
#
# Sourced, never executed. Pure definitions: no subprocesses, no output,
# no set-option mutation; bash-3.2-safe (host + container). Callers may
# run under set -e.

# shellcheck disable=SC2034  # consumed by sourcing scripts
VIBE_THM_BG="#0e1421"
VIBE_THM_SURFACE="#1a2440"
VIBE_THM_BORDER="#2a3554"
VIBE_THM_FG="#a9b6d8"
VIBE_THM_BRIGHT="#ffffff"
VIBE_THM_DIM="#5c6b96"
VIBE_THM_BLUE="#7aa2f7"
VIBE_THM_ACCENT="#3d59a1"
VIBE_THM_CORAL="#e8735a"
VIBE_THM_GREEN="#9ece6a"
VIBE_THM_YELLOW="#e0af68"
VIBE_THM_RED="#f7768e"

# The working dot's animation frames (docs/tui-layout.md "The working
# spinner") — presentation of the working state, never a state glyph;
# space-separated so bash-3.2 callers word-split into an array.
VIBE_SPIN_FRAMES="⠋ ⠙ ⠹ ⠸ ⠼ ⠴ ⠦ ⠧ ⠇ ⠏"

# hex (#rrggbb) -> truecolor foreground escape, on stdout.
vibe_fg() {
  local h="${1#\#}"
  printf '\033[38;2;%d;%d;%dm' \
    "0x$(printf '%.2s' "$h")" "0x$(printf '%.2s' "${h#??}")" "0x$(printf '%.2s' "${h#????}")"
}

# The one state -> glyph + color map. Sets vibe_glyph and vibe_state_hex;
# returns 1 on an unknown state (callers pick their own fallback: the host
# renderer drops the event, ps.sh renders a dim dot). The full vocabulary —
# which channel carries which state is the caller's contract:
#   working        agent is doing something          (title channel + records)
#   attention      agent wants a human               (title channel + records)
#   idle           agent alive, nothing pending      (title channel + records)
#   exited*        recorded exit; ps.sh suffixes the code
#   running        alive is all we know (hookless/pre-identity runs; ps.sh)
#   gone           no live carrier, no recorded exit — killed too hard (ps.sh)
#   frontend-dead  the docker-exec viewer died; the run may live (host UI)
vibe_state_style() {
  case "$1" in
    working) vibe_glyph="●" vibe_state_hex="$VIBE_THM_GREEN" ;;
    attention) vibe_glyph="●" vibe_state_hex="$VIBE_THM_CORAL" ;;
    idle) vibe_glyph="●" vibe_state_hex="$VIBE_THM_DIM" ;;
    running) vibe_glyph="●" vibe_state_hex="$VIBE_THM_BLUE" ;;
    exited*) vibe_glyph="✗" vibe_state_hex="$VIBE_THM_RED" ;;
    gone | frontend-dead) vibe_glyph="◌" vibe_state_hex="$VIBE_THM_DIM" ;;
    *) return 1 ;;
  esac
}

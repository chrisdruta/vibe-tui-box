#!/usr/bin/env bash
#
# vibe tui project sidebar (spike, 2026-07-22): a vertical take on the
# cross-project strip. One narrow pane on the far left listing every
# project session on the vibe socket — state dots (the same @vibe_glyph /
# @vibe_dot_fg / @vibe_attn data state-render.sh maintains for the tabs
# and the strip), the workspace name in bold (bright = the session this
# sidebar lives in, dim = the others), and the checkout's git branch
# underneath. Render-only, per-window, toggled with prefix+b / palette b.
#
# Modes:
#   toggle WINDOW_ID   create/kill the sidebar pane in that window (bind)
#   render             the draw loop that runs inside the sidebar pane
#   render --once      draw a single frame and exit (testing)
#
# Refresh is spike-grade: a 2s poll INSIDE this one pane. The status line
# stays event-driven (status-interval 0 untouched) and nothing here wakes
# other clients; the branch line has no tmux event to hook anyway. If the
# sidebar graduates, the loop should block on `tmux wait-for` signals
# nudged from state-render.sh and the session-created/closed hooks, with
# the poll kept only as the branch-change fallback.
#
# Host-side: bash-3.2-safe (stock macOS). Runs under the vibe server
# (run-shell provides TMUX for `toggle`; the pane's environment provides
# it for `render`), so plain `tmux` is always the right binary/socket.
set -u

mode="${1:-render}"
tab="$(printf '\t')"

# ── toggle: the prefix+b / palette entry point ───────────────────────────
if [ "$mode" = "toggle" ]; then
  win="${2:-}"
  [ -n "$win" ] || exit 0
  existing="$(tmux list-panes -t "$win" -F "#{pane_id}$tab#{@vibe_role}" 2>/dev/null |
    awk -F "$tab" '$2 == "sidebar" { print $1; exit }')"
  if [ -n "$existing" ]; then
    tmux kill-pane -t "$existing"
    exit 0
  fi
  self="$(cd "$(dirname "$0")" && pwd)/$(basename "$0")"
  # Full-height split BEFORE the leftmost pane; input off so stray clicks
  # can't type into the render loop; focus returns to where the user was.
  pane="$(tmux split-window -fhb -l 26 -t "$win" -P -F '#{pane_id}' \
    "exec bash '$self' render")"
  tmux set-option -p -t "$pane" @vibe_role "sidebar" \; \
    set-option -p -t "$pane" @vibe_title "projects" \; \
    select-pane -d -t "$pane" \; \
    select-pane -l
  exit 0
fi

# ── render ───────────────────────────────────────────────────────────────
once=0
[ "${2:-}" = "--once" ] && once=1

# One palette: read the theme off the server, conf defaults as fallback
# (same rule as state-render.sh). Read once at launch — a palette change
# lands on the next toggle, which is fine for a look-see.
thm() { v="$(tmux show-options -gv "$1" 2>/dev/null)"; [ -n "$v" ] && printf '%s' "$v" || printf '%s' "$2"; }
fg() { # hex -> truecolor foreground escape
  h="${1#\#}"
  printf '\033[38;2;%d;%d;%dm' "0x$(printf '%.2s' "$h")" "0x$(printf '%.2s' "${h#??}")" "0x$(printf '%.2s' "${h#????}")"
}
c_fg="$(fg "$(thm @thm_fg '#a9b6d8')")"
c_dim="$(fg "$(thm @thm_dim '#5c6b96')")"
c_coral="$(fg "$(thm @thm_coral '#e8735a')")"
bold="$(printf '\033[1m')"
reset="$(printf '\033[0m')"
eol="$(printf '\033[K')"

# .git/HEAD read directly — no git subprocess per tick. Handles the .git
# FILE indirection (worktrees, submodule checkouts); detached HEAD shows
# the short sha.
branch_of() {
  p="$1"
  g="$p/.git"
  if [ -f "$g" ]; then
    gd="$(sed -n 's/^gitdir: //p' "$g" 2>/dev/null)"
    case "$gd" in
      '') return 0 ;;
      /*) g="$gd" ;;
      *) g="$p/$gd" ;;
    esac
  fi
  [ -r "$g/HEAD" ] || return 0
  IFS= read -r line <"$g/HEAD" || [ -n "$line" ] || return 0
  case "$line" in
    "ref: refs/heads/"*) printf '%s' "${line#ref: refs/heads/}" ;;
    *) printf '%.7s' "$line" ;; # detached: the short sha is the label
  esac
}

frame() {
  width="$(tmux display-message -p -t "${TMUX_PANE:-}" '#{pane_width}' 2>/dev/null)"
  case "$width" in '' | *[!0-9]*) width=26 ;; esac
  here="$(tmux display-message -p -t "${TMUX_PANE:-}" '#{session_name}' 2>/dev/null)" || here=""
  # Text budget: 2-col left gutter, keep 1 clear on the right.
  max=$((width - 3))
  [ "$max" -lt 8 ] && max=8

  buf="$(printf '\033[H')"
  buf="$buf
$eol"
  while IFS="$tab" read -r sid name path; do
    [ -n "$sid" ] || continue
    # Dots: window order, same semantics as the strip — attention renders
    # coral (the tab-blend @vibe_dot_fg would vanish here), plain windows
    # (host shells, popups) emit nothing.
    dots=""
    while IFS="$tab" read -r glyph dfg attn; do
      [ -n "$glyph" ] || continue
      if [ "$attn" = "1" ]; then
        dots="$dots ${c_coral}●"
      else
        dots="$dots $(fg "${dfg:-#5c6b96}")$glyph"
      fi
    done <<EOF2
$(tmux list-windows -t "$sid" -F "#{@vibe_glyph}$tab#{@vibe_dot_fg}$tab#{@vibe_attn}" 2>/dev/null)
EOF2
    if [ "$name" = "$here" ]; then
      mark="${c_coral}▍" name_c="$c_fg"
    else
      mark=" " name_c="$c_dim"
    fi
    shown="$name"
    [ "${#shown}" -gt "$max" ] && shown="$(printf '%.*s' $((max - 1)) "$shown")…"
    buf="$buf
${mark}${reset} ${bold}${name_c}${shown}${reset}${dots}${reset}${eol}"
    br="$(branch_of "$path")"
    if [ -n "$br" ]; then
      [ "${#br}" -gt $((max - 2)) ] && br="$(printf '%.*s' $((max - 3)) "$br")…"
      buf="$buf
   ${c_dim}⎇ ${br}${reset}${eol}"
    fi
    buf="$buf
$eol"
  done <<EOF
$(tmux list-sessions -F "#{session_id}$tab#{session_name}$tab#{session_path}" 2>/dev/null | sort -t "$tab" -k2)
EOF
  printf '%s\033[J' "$buf"
}

printf '\033[?25l'
trap 'printf "\033[?25h"' EXIT
frame
[ "$once" = "1" ] && exit 0
while :; do
  sleep 2
  frame
done

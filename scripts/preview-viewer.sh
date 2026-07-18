#!/usr/bin/env bash
#
# Image review viewer: the pane process of a dedicated tmux window named
# "preview". Flip through images arriving in a watched directory (Gemini
# output, Blender render batches, `vibe clip` captures), record approve/reject
# verdicts to a JSONL file an agent or pipeline can consume, and jump to
# whatever a Claude Code hook just surfaced (preview-image-hook.sh feeds the
# queue file below).
#
# Why a dedicated window and not a split: sixel under tmux 3.5a survives
# only in a calm window. Native ingestion (no passthrough) is a dead letter
# on this build — tmux stores the image but never re-emits it to the
# client, so even fresh renders appear as "+" placeholders (see also
# tmux/tmux#4499, #4639, #5126). Passthrough renders for real but paints at
# the client cursor, so any window shared with a busy agent TUI smears it
# into ghosts (cursor drag, scroll optimizations). A dedicated window the
# viewer fully owns removes every disturber: the cursor is ours, nothing
# scrolls, tmux repaints only the active window. The viewer still
# re-renders on window re-entry, on SIGWINCH, on demand (`r`), and once
# more a tick after each render (entry/resize redraw storms can eat the
# first pass).
#
# Modes:
#   (no args)          run the UI — must be a tmux pane's own process
#   --ensure SESSION   create the window detached if absent (hooks; silent)
#   --focus  SESSION   jump to the window, creating it if needed (prefix+i)
set -uo pipefail

WINDOW_NAME=preview
QUEUE=/tmp/.vibe-preview-queue
LOCK=/tmp/.vibe-preview-viewer.lock

# Canonicalize our own path (readlink loop, not GNU-only `readlink -f`) so
# --ensure/--focus relaunch the SAME copy: the baked /usr/local/bin/vibe-preview
# spawns the baked copy, a harness checkout spawns the harness copy.
self_path="${BASH_SOURCE[0]}"
while [ -L "$self_path" ]; do
  link_target="$(readlink "$self_path")"
  case "$link_target" in
    /*) self_path="$link_target" ;;
    *) self_path="$(dirname -- "$self_path")/$link_target" ;;
  esac
done
script_dir="$(cd -- "$(dirname -- "$self_path")" && pwd)"
self="$script_dir/$(basename -- "$self_path")"

case "${1:-}" in
  --ensure | --focus)
    session="${2:-}"
    [ -n "$session" ] || exit 0
    if [ "$1" = "--focus" ]; then
      tmux select-window -t "${session}:=${WINDOW_NAME}" 2>/dev/null && exit 0
      tmux new-window -t "$session" -n "$WINDOW_NAME" "exec bash '$self'" >/dev/null 2>&1
    else
      tmux list-windows -t "$session" -F '#{window_name}' 2>/dev/null |
        grep -qx "$WINDOW_NAME" && exit 0
      tmux new-window -d -t "$session" -n "$WINDOW_NAME" "exec bash '$self'" >/dev/null 2>&1
    fi
    exit 0
    ;;
  -h | --help)
    sed -n '2,25p' "$self" | sed 's/^# \{0,1\}//'
    exit 0
    ;;
esac

# Two homes: a tmux window (prefix+i — best effort, see header), or a plain
# host terminal via `vibe review` (devcontainer exec with a pty) — the
# RELIABLE one: chafa probes the real terminal and emits sixel with no tmux
# between the pixels and the screen.
in_tmux=""
[ -n "${TMUX:-}" ] && in_tmux=1
[ -t 0 ] || { echo "preview-viewer needs an interactive terminal" >&2; exit 1; }
command -v chafa >/dev/null 2>&1 || { echo "chafa not installed" >&2; exit 1; }
command -v jq >/dev/null 2>&1 || { echo "jq not installed" >&2; exit 1; }

# Singleton: a second viewer (hook race, double prefix+i) fails this lock,
# exits, and its window self-closes without ever taking focus.
exec 9>"$LOCK"
flock -n 9 || exit 0

# Project config, best effort: harness layout puts config.env two levels up
# (.devcontainer/harness/scripts -> .devcontainer/config.env); the baked copy
# falls back to the session start directory (the workspace root under
# `vibe agent`). Defaults applied after so missing keys or files are fine.
for cfg in "$script_dir/../../config.env" "$PWD/.devcontainer/config.env"; do
  # shellcheck disable=SC1090  # runtime project config, path known only here
  if [ -f "$cfg" ]; then . "$cfg"; break; fi
done
watch_dir="${VIBE_PREVIEW_DIR:-/tmp}"
watch_glob="${VIBE_PREVIEW_GLOB:-*.png *.jpg *.jpeg *.webp}"
decisions="${VIBE_PREVIEW_DECISIONS:-${watch_dir%/}/vibe-decisions.jsonl}"

# Light the window name in the status bar when we print while unfocused.
[ -n "$in_tmux" ] && tmux set-option -w -t "${TMUX_PANE:-}" monitor-activity on 2>/dev/null

name_args=()
for g in $watch_glob; do # deliberate word split: glob list is space-separated
  if [ "${#name_args[@]}" -gt 0 ]; then name_args+=(-o); fi
  name_args+=(-name "$g")
done

images=()
current=""
declare -A extras=() # hook-fed paths outside the watch dir, live while file exists
need_render=1
winch=""
last_active=""
last_sig=""
heal_left=0
last_dcs="" # cached bare sixel DCS + anchor of the last render, for emit_last
last_row=1
last_col=1
trap 'winch=1' WINCH
trap 'printf "\033[2J\033[H"' EXIT

scan() {
  local entries=() line path
  local -A seen=()
  for path in "${!extras[@]}"; do # prune vanished hook-fed files
    [ -f "$path" ] || unset 'extras[$path]'
  done
  # Newest first; NUL-safe so paths with spaces survive (the hook's prompt
  # grep can't match those, but queue/watch-dir arrivals can).
  mapfile -d '' -t entries < <(
    {
      find "$watch_dir" -maxdepth 1 -type f \( "${name_args[@]}" \) -printf '%T@\t%p\0' 2>/dev/null
      for path in "${!extras[@]}"; do
        printf '%s\t%s\0' "$(stat -c %Y -- "$path" 2>/dev/null || echo 0)" "$path"
      done
    } | sort -rzn
  )
  images=()
  for line in "${entries[@]}"; do
    path="${line#*$'\t'}"
    [ -n "${seen[$path]:-}" ] && continue
    seen[$path]=1
    images+=("$path")
  done
}

drain_queue() { # sets $jump to the newest valid queued path, if any
  jump=""
  [ -s "$QUEUE" ] || return 0
  local lines p
  # shellcheck disable=SC2094  # sequential read-then-truncate, serialized by the flock
  lines="$( { flock -x 8; cat -- "$QUEUE" 2>/dev/null; : >"$QUEUE"; } 8>>"$QUEUE" )"
  while IFS= read -r p; do
    if [ -z "$p" ] || [ ! -f "$p" ]; then continue; fi
    extras["$p"]=1
    jump="$p"
  done <<<"$lines"
}

idx_of_current() {
  local i
  for i in "${!images[@]}"; do
    [ "${images[$i]}" = "$current" ] && { printf '%s' "$i"; return; }
  done
  printf -- '-1'
}

verdict_of() {
  [ -f "$decisions" ] || { printf 'undecided'; return; }
  local v
  v="$(jq -r --arg p "$1" 'select(.path==$p).verdict' "$decisions" 2>/dev/null | tail -1)"
  printf '%s' "${v:-undecided}"
}

decide() {
  [ -n "$current" ] && [ -f "$current" ] || return 0
  mkdir -p -- "$(dirname -- "$decisions")" 2>/dev/null
  jq -nc --arg ts "$(date -u +%FT%TZ)" --arg path "$current" --arg verdict "$1" \
    '{ts: $ts, path: $path, verdict: $verdict}' >>"$decisions" 2>/dev/null
  move older # reviewing runs newest -> older through the batch
  need_render=1
}

move() { # older|newer|newest — selection is by path; index recomputed per scan
  [ "${#images[@]}" -gt 0 ] || return 0
  local idx
  idx="$(idx_of_current)"
  case "$1" in
    newest) idx=0 ;;
    newer) idx=$((idx > 0 ? idx - 1 : 0)) ;;
    older) idx=$((idx + 1 < ${#images[@]} ? idx + 1 : ${#images[@]} - 1)) ;;
  esac
  [ "$idx" -lt 0 ] && idx=0
  if [ "${images[$idx]}" != "$current" ]; then
    current="${images[$idx]}"
    need_render=1
  fi
}

emit_last() {
  # Flicker-free heal: re-emit only the cached sixel envelope, over itself.
  # A render can land mid client-redraw (window switch, resize settling) and
  # get wiped; the text survives in tmux's grid, so repainting just the
  # pixels one tick later repairs the image without a clear — invisible when
  # the first pass survived.
  heal_left=0
  [ -n "$last_dcs" ] || return 0
  local esc
  esc="$(printf '\033')"
  printf '\033Ptmux;'
  printf '\0337\033[%d;%dH%s\0338' "$last_row" "$last_col" "$last_dcs" | sed "s/$esc/$esc$esc/g"
  # shellcheck disable=SC1003  # literal backslash: the ST terminator, not a quote escape
  printf '\033\\'
}

render() {
  need_render=""
  last_dcs=""
  if [ -z "$in_tmux" ]; then heal_left=0; else heal_left=1; fi
  local cols rows idx
  cols="$(tput cols 2>/dev/null || echo 80)"
  rows="$(tput lines 2>/dev/null || echo 24)"
  printf '\033[2J\033[H'
  if [ -z "$current" ]; then
    printf 'No images yet — watching %s (%s)\n' "$watch_dir" "$watch_glob"
    printf 'q quit  r rescan\n'
    return
  fi
  idx="$(idx_of_current)"
  printf '[%d/%d] %s  (%s)\n' "$((idx + 1))" "${#images[@]}" \
    "$(basename -- "$current")" "$(verdict_of "$current")"
  printf 'h/< newer  l/> older  g newest  y approve  n/x reject  r redraw  q quit\n\n'
  # Image box: side and bottom margins, centered via chafa (it composes the
  # padding into the canvas, so the anchored sixel below stays one block).
  local iw ih
  iw=$((cols - 4))
  ih=$((rows - 5))
  if [ "$iw" -lt 4 ]; then iw=4; fi
  if [ "$ih" -lt 3 ]; then ih=3; fi
  if [ -z "$in_tmux" ]; then
    # Plain terminal (`vibe review` from the host): chafa probes it directly
    # and picks sixel or unicode blocks itself — no tmux anywhere. This is
    # the zero-caveat path.
    chafa --align mid,mid -s "${iw}x${ih}" -- "$current" 2>/dev/null ||
      printf '(render failed — press r to retry)\n'
  elif tmux display-message -p '#{client_termfeatures}' 2>/dev/null | grep -q sixel; then
    # In tmux, a hand-anchored passthrough envelope — the only variant that
    # rendered deterministically on 3.5a. Native ingestion redraws as "+"
    # placeholders, and BARE passthrough races: tmux batches pane-text
    # drawing but forwards passthrough bytes immediately, so the image can
    # reach the client before the header text that positioned the cursor.
    # Self-positioning inside the envelope (save cursor, absolute jump,
    # draw, restore) is immune to both, and this window never scrolls, so
    # the shared-window ghost problem can't occur either.
    # Sizing is measured, not predicted: chafa's cell→pixel mapping when its
    # output is captured bears no relation to the real terminal's cell size
    # (observed: images rendered beyond the whole screen). So render, read
    # the true pixel size from the sixel raster header ("Pan;Pad;Ph;Pv), and
    # if it busts a conservative pixel budget for the box (10x20 px/cell —
    # real cells are almost always bigger, so output errs SMALL and fits),
    # rescale the request proportionally and render once more.
    local raw img esc row col voff hoff pxw pxh budw budh num den iw2 ih2 cw ch
    budw=$((iw * 10))
    budh=$((ih * 20))
    raw="$(chafa -f sixel --passthrough none -s "${iw}x${ih}" -- "$current" 2>/dev/null)"
    read -r pxw pxh <<<"$(printf '%s' "$raw" | head -c 200 |
      sed -n 's/.*q"[0-9][0-9]*;[0-9][0-9]*;\([0-9][0-9]*\);\([0-9][0-9]*\).*/\1 \2/p')"
    if [ -n "${pxw:-}" ] && [ -n "${pxh:-}" ] && [ "$pxw" -gt 0 ] && [ "$pxh" -gt 0 ]; then
      if [ "$pxw" -gt "$budw" ] || [ "$pxh" -gt "$budh" ]; then
        if [ $((pxw * budh)) -gt $((pxh * budw)) ]; then num=$budw den=$pxw; else num=$budh den=$pxh; fi
        iw2=$((iw * num / den))
        ih2=$((ih * num / den))
        if [ "$iw2" -lt 1 ]; then iw2=1; fi
        if [ "$ih2" -lt 1 ]; then ih2=1; fi
        raw="$(chafa -f sixel --passthrough none -s "${iw2}x${ih2}" -- "$current" 2>/dev/null)"
        read -r pxw pxh <<<"$(printf '%s' "$raw" | head -c 200 |
          sed -n 's/.*q"[0-9][0-9]*;[0-9][0-9]*;\([0-9][0-9]*\);\([0-9][0-9]*\).*/\1 \2/p')"
      fi
    fi
    case "$raw" in
      *$'\x1bP'*) : ;;
      *)
        printf '(render failed — press r to retry)\n'
        return
        ;;
    esac
    # Bare DCS only — any text decoration executed inside the envelope acts
    # at CLIENT level (spaces blank cells, a bottom-row linefeed scrolls the
    # whole screen).
    img="${raw#*$'\x1bP'}"
    img="${img%$'\x1b\\'*}"
    img=$'\x1bP'"$img"$'\x1b\\'
    # Center using the same conservative cell estimate the budget used.
    cw=$(((${pxw:-0} + 9) / 10))
    ch=$(((${pxh:-0} + 19) / 20))
    hoff=$(((iw - cw) / 2))
    voff=$(((ih - ch) / 2))
    if [ "$hoff" -lt 0 ]; then hoff=0; fi
    if [ "$voff" -lt 0 ]; then voff=0; fi
    row=$((4 + voff)) # under 2 header lines + 1 blank; window origin is client row 1
    if [ "$(tmux show -gv status-position 2>/dev/null)" = "top" ]; then row=$((row + 1)); fi
    col=$((3 + hoff))
    last_dcs="$img" # cache for the flicker-free heal pass
    last_row=$row
    last_col=$col
    esc="$(printf '\033')"
    printf '\033Ptmux;'
    printf '\0337\033[%d;%dH%s\0338' "$row" "$col" "$img" | sed "s/$esc/$esc$esc/g"
    # shellcheck disable=SC1003  # literal backslash: the ST terminator, not a quote escape
    printf '\033\\'
  else
    chafa -f symbols --align mid,mid -s "${iw}x${ih}" -- "$current" 2>/dev/null ||
      printf '(render failed — press r to retry)\n'
  fi
}

while :; do
  drain_queue
  scan
  if [ -n "$jump" ]; then
    current="$jump"
    need_render=1
  fi
  if [ -z "$current" ] || [ "$(idx_of_current)" -lt 0 ]; then # gone or never set
    current="${images[0]:-}"
    need_render=1
  fi
  sig="${#images[@]}:${images[0]:-}"
  active="$(tmux display-message -p -t "${TMUX_PANE:-}" '#{window_active}' 2>/dev/null)"
  if [ "$active" = "1" ]; then
    # Re-entry and resizes both invalidate whatever sixel was on screen; a
    # changed image list refreshes the [n/total] header.
    [ "$last_active" != "1" ] && need_render=1
    [ "$sig" != "$last_sig" ] && need_render=1
    [ -n "$winch" ] && winch="" && need_render=1
    if [ -n "$need_render" ]; then
      render
    elif [ "${heal_left:-0}" -gt 0 ]; then
      emit_last
    fi
  elif [ "$sig" != "$last_sig" ] && [ -n "${images[0]:-}" ]; then
    # Unfocused: one short line trips monitor-activity; render waits for entry.
    printf 'new: %s (%d total)\n' "$(basename -- "${images[0]}")" "${#images[@]}"
  fi
  last_active="$active"
  last_sig="$sig"

  key=""
  IFS= read -rsn1 -t 2 key || continue
  if [ "$key" = $'\x1b' ]; then # arrow keys arrive as ESC [ C/D
    rest=""
    IFS= read -rsn2 -t 0.05 rest || rest=""
    key="ESC$rest"
  fi
  case "$key" in
    h | 'ESC[D') move newer ;;
    l | 'ESC[C') move older ;;
    g) move newest ;;
    y) decide approve ;;
    n | x) decide reject ;;
    r) need_render=1 ;;
    q) exit 0 ;;
  esac
done

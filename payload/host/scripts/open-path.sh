#!/usr/bin/env bash
# The ctrl+click path-follow dispatcher — ONE definition behind the
# conf's C-MouseDown1Pane bind (docs/tui-layout.md "Editor surfaces").
# Reads the clicked line back via capture-pane instead of trusting
# #{mouse_word} (the default word-separators split on ./:- so a path
# arrives as fragments) or #{q:mouse_line} (q-escaping inserts
# backslashes that shift the column arithmetic), extracts the path
# around the mouse column, resolves it INSIDE the project's container
# over `vibe exec` (host-workspace paths map by the bind-mount prefix),
# and routes: text → review.sh's nvim popup (`file` mode), images →
# the reusable preview window (show-image.sh, sixel or loud low-fi).
# Anything that is not path-shaped, or does not resolve, exits silently
# so the gesture is free over prose.
#
#   open-path.sh CLIENT SESSION_PATH EXE PANE X Y
#
# Known limits, recorded on purpose: a path wrapped across two screen
# lines truncates at the wrap; wide glyphs left of the pointer shift
# the column by their extra cells; a copy-mode-scrolled pane extracts
# from the live screen, not the scrolled view. All three fail toward
# the silent no-op.
#
# The extracted word is rebuilt character-by-character from the path
# alphabet below, so it cannot smuggle quotes or metacharacters — it
# still only ever travels as a quoted argv word, never through eval.
#
# Host-side: bash-3.2-safe (stock macOS). Runs under the vibe server
# via run-shell.
set -u

client="${1:-}"
spath="${2:-}"
exe="${3:-}"
pane="${4:-}"
x="${5:-}"
y="${6:-}"

[ -n "$pane" ] && [ -n "$spath" ] && [ -n "$exe" ] && [ -x "$exe" ] || exit 0
case "$x" in '' | *[!0-9]*) exit 0 ;; esac
case "$y" in '' | *[!0-9]*) exit 0 ;; esac

line="$(tmux capture-pane -p -t "$pane" 2>/dev/null | sed -n "$((y + 1))p")"
[ -n "$line" ] || exit 0
len=${#line}
[ "$x" -lt "$len" ] || exit 0

# The path alphabet. ':' is in so file.go:12 arrives whole (split off
# below); '=' '?' '&' are out so URLs and flags stop early and fail
# resolution silently.
path_char() {
  case "$1" in
  [A-Za-z0-9._/~+@%:-]) return 0 ;;
  *) return 1 ;;
  esac
}

path_char "${line:$x:1}" || exit 0
start=$x
while [ "$start" -gt 0 ] && path_char "${line:$((start - 1)):1}"; do
  start=$((start - 1))
done
end=$x
while [ $((end + 1)) -lt "$len" ] && path_char "${line:$((end + 1)):1}"; do
  end=$((end + 1))
done
word="${line:$start:$((end - start + 1))}"
[ ${#word} -le 512 ] || exit 0

# Split a :LINE (or :LINE:COL) suffix off — compiler/grep spelling.
strip_num() {
  case "$1" in
  *:*)
    g_num="${1##*:}"
    g_rest="${1%:*}"
    case "$g_num" in '' | *[!0-9]*) return 1 ;; esac
    [ -n "$g_rest" ]
    ;;
  *) return 1 ;;
  esac
}
word="${word%:}"
lineno=""
if strip_num "$word"; then
  lineno="$g_num" word="$g_rest"
  if strip_num "$word"; then
    # two numeric tails — that was file:LINE:COL; the line is the inner one
    lineno="$g_num" word="$g_rest"
  fi
fi

# Sentence punctuation clings to paths in prose ("see tui.go.").
while :; do
  case "$word" in
  *. | *,) word="${word%?}" ;;
  *) break ;;
  esac
done

# Path-shaped or nothing: a slash, or a dotted extension. Bare words
# stay prose.
case "$word" in
*/*) ;;
*.*) ;;
*) exit 0 ;;
esac

# '~' means a HOST home the container cannot see — not this door.
case "$word" in
'~' | '~/'*) exit 0 ;;
esac

# Resolve to the container view: /workspace paths pass through, host
# paths under the project map by the bind-mount prefix, anything
# relative is meant from the workspace root, and any other absolute
# path is tried AS a container path first — `vibe clip` drops its
# PNGs at the container's /tmp, and agents talk about container
# paths, not host ones. Only when the container denies it does the
# host get a look (message, never an open — the tools live container-
# side).
host_fallback=""
case "$word" in
/workspace | /workspace/*) cpath="$word" ;;
"$spath") cpath="/workspace" ;;
"$spath"/*) cpath="/workspace${word#"$spath"}" ;;
/*)
  cpath="$word"
  host_fallback=1
  ;;
*) cpath="/workspace/$word" ;;
esac
case "$lineno" in *[!0-9]*) lineno="" ;; esac

# The engine resolves the project from the working directory — same
# contract review.sh leans on via its popup's -d.
cd "$spath" 2>/dev/null || exit 0
if ! "$exe" exec -- test -e "$cpath" >/dev/null 2>&1; then
  [ -n "$host_fallback" ] && [ -e "$word" ] &&
    tmux display-message -c "$client" "vibe open: outside the workspace — $word"
  exit 0
fi

# Image extensions get the preview window (docs/tui-layout.md
# "Preview window"): ONE reusable window per project session, marked
# @vibe_view (found by the marker, not the name — the name carries the
# filename), running show-image.sh over `vibe exec`. The format is
# gated HERE, loudly: sixel only when this server is >=3.7 AND the
# client negotiated sixel (verdict-A conditions); anything less gets
# chafa symbols plus show-image.sh's low-fi header — degradation the
# operator can see, never silent (the 2026-07-27 decision).
case "$cpath" in
*.*)
  ext="$(printf '%s' "${cpath##*.}" | tr 'A-Z' 'a-z')"
  case "$ext" in
  png | jpg | jpeg | gif | webp | bmp)
    fmt=symbols
    v="$(tmux display-message -p '#{version}' 2>/dev/null)"
    maj="${v%%.*}"
    min="${v#*.}"
    min="${min%%[!0-9]*}"
    case "$maj" in '' | *[!0-9]*) maj="" ;; esac
    case "$min" in '' | *[!0-9]*) min="" ;; esac
    feats="$(tmux display-message -p -c "$client" '#{client_termfeatures}' 2>/dev/null)"
    if [ -n "$maj" ] && [ -n "$min" ] &&
      { [ "$maj" -gt 3 ] || { [ "$maj" -eq 3 ] && [ "$min" -ge 7 ]; }; }; then
      case ",$feats," in *,sixel,*) fmt=sixel ;; esac
    fi

    # Argv → one /bin/sh string — popup.sh's quoting rule — plus
    # review.sh's hold-open tail for a dead container.
    cmd=""
    for word in "$exe" exec -- bash /vibe/payload/container/show-image.sh "$fmt" "$cpath"; do
      cmd="$cmd '${word//\'/\'\\\'\'}'"
    done
    cmd="$cmd || { printf '\\n[vibe] container unavailable? vibe up starts it · enter to close '; read -r _; }"

    sess="$(tmux display-message -p -c "$client" '#{session_id}')"
    [ -n "$sess" ] || exit 0
    name="⌗ ${cpath##*/}"
    win="$(tmux list-windows -t "$sess" -F '#{window_id} #{@vibe_view}' 2>/dev/null | awk '$2==1{print $1; exit}')"
    if [ -n "$win" ]; then
      tmux respawn-window -k -c "$spath" -t "$win" "$cmd"
      tmux rename-window -t "$win" "$name"
      tmux select-window -t "$win"
    else
      win="$(tmux new-window -c "$spath" -t "$sess" -n "$name" -P -F '#{window_id}' "$cmd")"
      [ -n "$win" ] && tmux set-option -w -t "$win" @vibe_view 1
    fi
    exit 0
    ;;
  esac
  ;;
esac

dir="$(cd "$(dirname "$0")" && pwd)"
exec bash "$dir/review.sh" "$client" "$exe" "$spath" file "$cpath" "$lineno"

#!/usr/bin/env bash
# vibe show-image.sh — the container half of the tui's preview window
# (docs/tui-layout.md "Preview window"), reached over `vibe exec` by
# open-path.sh when a ctrl+clicked path has an image extension. chafa
# (image-baked, internal/builder/install.go) encodes; the HOST tmux
# ingests the emitted sixel natively and re-emits it on redraw — the
# v1 lesson (b2819b1): passthrough dies under nesting, native ingest
# survives it, and this window is a host pane so only one tmux layer
# is even involved.
#
#   show-image.sh sixel|symbols PATH
#
# FORMAT is decided host-side (open-path.sh probes the host tmux; a
# pre-3.7 or sixel-less host gets `symbols` and the loud low-fi
# header — degradation is visible, never silent). A resize clears
# sixel rasters on every tmux (upstream reflow), so SIGWINCH re-runs
# chafa: sidebar/dock toggles heal themselves instead of leaving a
# blank pane. Any key closes; the window dies with this process.
#
# The wait is a POLLED read, not a blocking one: bash installs its
# SIGWINCH handler with SA_RESTART (it watches WINCH itself for
# checkwinsize), so a blocking `read` is never interrupted and a
# WINCH trap sits pending until the builtin returns — verified
# in-container 2026-07-27, including `kill -WINCH` directly at the
# pid. Each timeout tick is the safe point where the pending trap
# actually runs; the flag then repaints within one tick.
set -u

fmt="${1:-}"
path="${2:-}"
case "$fmt" in
sixel | symbols) ;;
*)
  echo "show-image.sh: format must be sixel|symbols" >&2
  exit 2
  ;;
esac
[ -n "$path" ] || { echo "show-image.sh: no path" >&2; exit 2; }

if ! [ -r "$path" ]; then
  printf '[vibe] cannot read %s · any key closes ' "$path"
  read -r -n1 _
  exit 1
fi

render() {
  # Home + clear, then size the image UNDER the chrome: left to its
  # own devices chafa fills the full terminal height and the header
  # scrolls straight off the top (first repro, 2026-07-27).
  printf '\033[H\033[2J'
  rows=0 cols=0
  if sz="$(stty size 2>/dev/null)" && [ -n "$sz" ]; then
    rows="${sz%% *}"
    cols="${sz##* }"
  fi
  case "$rows$cols" in '' | *[!0-9]*) rows=0 cols=0 ;; esac
  reserve=2
  if [ "$fmt" = symbols ]; then
    reserve=3
    printf '\033[7m low-fi preview — host tmux lacks sixel >=3.7 (vibe doctor) \033[0m\n'
  fi
  # A degenerate size means the pty was born 0x0 (the exec
  # lost-first-resize race, fixed at ExecCreate but never trusted
  # again here): chafa's own 80x25 fallback beats a -s hard error —
  # that accidental fallback is what made the pre-`-s` script work.
  ok=1
  if [ "$rows" -ge 5 ] && [ "$cols" -ge 10 ]; then
    w="$cols"
    h=$((rows - reserve))
    if [ "$fmt" = sixel ]; then
      # The host pipe eats big rasters WHOLE: a full-pane render on a
      # wide window came out over 1MB and simply never appeared —
      # the size ladder bisected the cliff between 827KB (shows) and
      # 1.27MB (vanishes), 2026-07-27, which is tmux's DCS input
      # buffer limit (1MB; an over-long sixel is discarded, not
      # clipped). Encode to scratch, shrink until it fits with
      # headroom, and only then let it touch the terminal.
      tmpimg="${TMPDIR:-/tmp}/.vibe-show.$$"
      while :; do
        if ! chafa -f sixel --animate off -s "${w}x${h}" "$path" >"$tmpimg" 2>/dev/null; then
          ok=""
          break
        fi
        if [ "$(wc -c <"$tmpimg")" -le 900000 ]; then
          cat "$tmpimg"
          break
        fi
        w=$((w * 2 / 3))
        h=$((h * 2 / 3))
        if [ "$w" -lt 20 ] || [ "$h" -lt 6 ]; then
          printf '[vibe] image too dense for the sixel pipe at any size\n'
          break
        fi
      done
      rm -f "$tmpimg"
    else
      chafa -f "$fmt" --animate off -s "${w}x${h}" "$path" || ok=""
    fi
  else
    chafa -f "$fmt" --animate off "$path" || ok=""
  fi
  [ -n "$ok" ] || printf '[vibe] chafa failed on %s\n' "$path"
  printf '%s · any key closes' "${path##*/}"
}

resized=""
on_winch() { resized=1; }
trap on_winch WINCH
render
# rc 0 is a keypress (close), >128 is the poll timeout or a signal
# tick (loop; the trap has run by now and set the flag), anything
# else is a dead TTY (close — treating EOF like a tick would
# busy-spin forever).
while :; do
  read -r -n1 -t 0.3 _
  rc=$?
  [ "$rc" -eq 0 ] && break
  if [ -n "$resized" ]; then
    resized=""
    render
    continue
  fi
  [ "$rc" -gt 128 ] || break
done

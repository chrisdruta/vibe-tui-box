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
  # Home + clear; chafa sizes to the current terminal on each run.
  printf '\033[H\033[2J'
  if [ "$fmt" = symbols ]; then
    printf '\033[7m low-fi preview — host tmux lacks sixel >=3.7 (vibe doctor) \033[0m\n'
  fi
  chafa -f "$fmt" --animate off "$path" || printf '[vibe] chafa failed on %s\n' "$path"
  printf '%s · any key closes · re-renders on resize' "${path##*/}"
}

trap render WINCH
render
# read returns >128 when a signal (WINCH) interrupts it — wait again.
# Anything else — a keypress (0) or a dead TTY (1) — ends the window;
# treating EOF like a signal would busy-spin here forever.
while :; do
  read -r -n1 _ && break
  [ "$?" -gt 128 ] || break
done

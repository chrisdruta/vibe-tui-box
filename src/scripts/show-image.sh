#!/usr/bin/env bash
#
# Preview an image in the terminal: crisp img2sixel nearest-neighbor pixels
# for small png/jpeg/gif/bmp (real format sniffed from magic bytes, never
# the extension), chafa for everything else — sixel where the terminal
# supports it, unicode blocks otherwise. The companion to `vibe clip`: with
# no argument it shows the newest /tmp/clip-*.png so you can eyeball what an
# agent is about to see — the Claude Code TUI itself can't render images
# inline.
#
#   vibe show [PATH]          render it
#   vibe show --diag [PATH]   render nothing: report sniffed format vs
#                             extension, native size, renderer choice, exit
#                             code and stderr — for "why is it blank" moments
#
# Runs container-side, either directly in a pane or via `vibe show` from the
# host (vibe exec).
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

# Shared render/sniff/diagnostics helpers — same two homes as this script:
# a harness checkout (beside us) or the baked copy from the Dockerfile.
lib_ok=""
for _lib in "$script_dir/preview-lib.sh" /usr/local/lib/vibe/preview-lib.sh; do
  # shellcheck source=preview-lib.sh disable=SC1091
  if [ -f "$_lib" ]; then . "$_lib" && lib_ok=1; break; fi
done
if [ -z "$lib_ok" ]; then
  echo "preview-lib.sh not found (harness checkout incomplete, or old baked image — vibe rebuild)" >&2
  exit 1
fi
trap 'rm -f -- "$VIBE_RENDER_ERR"' EXIT
# Set by the lib's vibe_render_sixel; initialized here for the linter, which
# cannot follow the two-home source above.
raw="" pxw="" pxh="" last_fmt="" last_renderer="" last_scale="" last_rc=0 last_err=""

diag=""
if [ "${1:-}" = "--diag" ]; then
  diag=1
  shift
fi

path="${1:-}"
if [ -z "$path" ]; then
  # Newest image by mtime across /tmp clips AND VIBE_PREVIEW_DIR/GLOB
  # from config.env (where captures and generated images land).
  for cfg in "$script_dir/../../../config.env" "$PWD/.vibe/config.env" "$PWD/.devcontainer/config.env"; do
    # shellcheck disable=SC1090  # runtime project config, path known only here
    if [ -f "$cfg" ]; then . "$cfg"; break; fi
  done
  watch_dir="${VIBE_PREVIEW_DIR:-/tmp}"
  name_args=()
  for g in ${VIBE_PREVIEW_GLOB:-$(vibe_default_glob)}; do # deliberate split
    if [ "${#name_args[@]}" -gt 0 ]; then name_args+=(-o); fi
    name_args+=(-iname "$g")
  done
  path="$({
    find /tmp -maxdepth 1 -name 'clip-*.png' -printf '%T@ %p\n' 2>/dev/null
    find "$watch_dir" -maxdepth 1 -type f \( "${name_args[@]}" \) -printf '%T@ %p\n' 2>/dev/null
  } | sort -rn | head -1 | cut -d' ' -f2-)"
  if [ -z "$path" ]; then
    echo "No images in /tmp or $watch_dir — run \`vibe clip\` on the host first, or pass a path." >&2
    exit 1
  fi
fi
if [ ! -f "$path" ]; then
  echo "Not a readable image file: $path" >&2
  exit 1
fi

# Box: the whole terminal minus a small margin; fixed default when there is
# no tty to measure (exec without a pty, e.g. from a hook).
cols="$(tput cols 2>/dev/null)" || cols=""
rows="$(tput lines 2>/dev/null)" || rows=""
case "${cols:-}" in '' | *[!0-9]*) cols=102 ;; esac
case "${rows:-}" in '' | *[!0-9]*) rows=43 ;; esac
iw=$((cols - 2))
ih=$((rows - 3))
[ "$iw" -lt 4 ] && iw=4
[ "$ih" -lt 3 ] && ih=3

if [ -n "$diag" ]; then
  echo "vibe show --diag (dry run, no image emitted)"
  echo
  vibe_render_sixel "$path" "$iw" "$ih" $((iw * 10)) $((ih * 20)) || :
  vibe_diag_report "$path"
  if [ -n "${TMUX:-}" ]; then
    printf 'tmux:        client_termfeatures=[%s]\n' \
      "$(tmux display-message -p '#{client_termfeatures}' 2>/dev/null)"
  else
    printf 'terminal:    DA1 sixel=%s cell=%spx (10x20 assumed when unanswered)\n' \
      "$(term_has_sixel && echo yes || echo no)" "$(term_cell_px | tr ' ' 'x')"
  fi
  exit 0
fi

# The path echo is a human courtesy; keep piped stdout pure sixel.
if [ -t 1 ]; then echo "$path"; else echo "$path" >&2; fi

render_fail() {
  printf '(render failed via %s: %s%s — try: vibe show --diag %s)\n' \
    "${last_renderer:-?}" "$last_err" "$(vibe_format_mismatch)" "$path" >&2
  dlog "FAIL(show) $path fmt=$last_fmt renderer=$last_renderer rc=$last_rc err=$last_err"
  exit 1
}

if [ -n "${TMUX:-}" ]; then
  # Inside tmux, sixel in a passthrough envelope (explicit — chafa's "auto"
  # default already means this, but leave no room for drift): this tmux
  # build ingests raw sixel yet never re-emits it to the client, so native
  # compositing shows only "+" placeholders. Passthrough paints at the
  # client cursor — correct when this pane is focused in a calm window
  # (manual use), garbage next to a busy agent
  # TUI, which is why hooks feed the yazi preview window instead of
  # rendering into shared windows. Kept on chafa deliberately: chafa pairs its sizing
  # assumption with matching text-level cursor advancement OUTSIDE the
  # envelope, which a hand-wrapped img2sixel raster cannot replicate
  # (either CSI leaks into the envelope or the prompt overlaps the image).
  # Crisp nearest-neighbor pixels inside tmux live in the preview window
  # (prefix+i), which anchors and heals the raster properly.
  dlog "show(tmux pane) $path fmt=$(sniff_format "$path") via chafa --passthrough tmux"
  exec chafa -f sixel --animate off --passthrough tmux "$path"
fi

if [ -t 1 ] && ! term_has_sixel; then
  # Terminal didn't claim sixel in its DA1 reply: chafa probes it directly
  # and picks sixel or unicode blocks itself — the zero-caveat path.
  exec chafa --animate off "$path"
fi

if [ ! -t 1 ] && [ "${VIBE_SHOW_FORMAT:-}" = "symbols" ]; then
  # Opt-out for piping to non-sixel consumers; the default non-tty contract
  # stays "emit sixel for the host terminal to render".
  exec chafa -f symbols --animate off -s "${iw}x${ih}" "$path"
fi

cellw=10
cellh=20
if [ -t 1 ]; then read -r cellw cellh <<<"$(term_cell_px)" || :; fi
vibe_render_sixel "$path" "$iw" "$ih" $((iw * cellw)) $((ih * cellh)) || render_fail
dlog "OK(show) $path fmt=$last_fmt renderer=$last_renderer scale=$last_scale out=${pxw}x${pxh}"
printf '%s\n' "$raw"

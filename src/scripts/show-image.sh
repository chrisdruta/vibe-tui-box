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
  # from config.env (where captures and generated images land). Nearest
  # project config above $PWD — the same walk shape as svc.sh/review.sh
  # (idiom 3, AGENTS.md "Path discovery"): panes live inside the project
  # and `vibe show` cexecs at the repo root, while a script-relative
  # guess breaks under the baked copy.
  dir="$PWD"
  while [ "$dir" != "/" ]; do
    for cfg in "$dir/.vibe/config.env" "$dir/.devcontainer/config.env"; do
      # shellcheck disable=SC1090  # runtime project config
      if [ -f "$cfg" ]; then . "$cfg"; break 2; fi
    done
    dir="$(dirname -- "$dir")"
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
  # Native ingest vs passthrough (host-validated under vibe tui,
  # 2026-07-21): when this tmux's CLIENT is declared sixel-capable
  # (terminal-features — shipped in tmux.conf; the client under vibe tui
  # is the host tmux 3.7b), emit RAW sixel and let this server ingest it.
  # It then re-emits the image on every redraw, which is the only path
  # that survives nesting — passthrough forwards one transient copy that
  # the outer tmux composites onto cells this tmux repaints as blanks
  # moments later (prompt print/scroll), wiping it. Resize still clears
  # (upstream reflow); rerun to repaint. Feature-less clients keep the
  # passthrough envelope (historically the only working in-tmux path: an
  # undeclared client renders native ingest as "+" placeholders). Both
  # stay on chafa deliberately: it pairs its sizing assumption with
  # matching text-level cursor advancement, which a hand-wrapped
  # img2sixel raster cannot replicate (either CSI leaks into the envelope
  # or the prompt overlaps the image). VIBE_SHOW_NATIVE=1/0 forces
  # either way.
  client_features="$(tmux display-message -p '#{client_termfeatures}' 2>/dev/null || true)"
  native=0
  case ",$client_features," in *,sixel,*) native=1 ;; esac
  case "${VIBE_SHOW_NATIVE:-}" in 1) native=1 ;; 0) native=0 ;; esac
  if [ "$native" = 1 ]; then
    dlog "show(tmux pane) $path fmt=$(sniff_format "$path") via chafa native ingest (client sixel)"
    exec chafa -f sixel --animate off "$path"
  fi
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

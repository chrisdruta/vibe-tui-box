#!/usr/bin/env bash
# shellcheck shell=bash
#
# Shared helpers for `vibe show` (show-image.sh sources this;
# preview-image-hook.sh stays standalone, and yazi does its own decoding). Pure
# bash + od/stat on purpose: the image has no `file`, ImageMagick, or python,
# and the preview path must not grow dependencies.
#
# Renderer contract (vibe_render_sixel): chafa decodes everything but only
# smooth-scales (no resampling-filter option through 1.14.x), which blends
# pixels — exactly wrong for small textures and icons. img2sixel has true
# nearest-neighbor (-r nearest) and pixel-exact sizing (-w Npx), but its
# built-in stb decoder only speaks png/jpeg/gif/bmp. So: sniff the REAL
# format from magic bytes (generated assets are often webp bytes named
# .jpg — extension routing was a source of silent blanks), send the stb
# formats to img2sixel — integer nearest upscale when the image fits the
# pixel budget, lanczos3 fit when it doesn't — and leave everything else
# (webp/avif/svg/tiff/unknown) to chafa: smooth, but correct.
#
# Two homes, like the scripts that source it: a harness checkout (found
# beside the script) or the baked copy at /usr/local/lib/vibe/preview-lib.sh
# (Dockerfile). Written to survive `set -e` callers: no bare failing
# commands, every read/expansion guarded.

# Canonical image extension list. preview-image-hook.sh reads this exact
# assignment line with sed (it must not source executable code — hook stdout
# leaks into model context), so keep it a plain one-line double-quoted
# literal. config.env templates and docs/ still sync by hand.
VIBE_IMAGE_EXTS="png jpg jpeg gif bmp webp avif"

# One line per render attempt, self-truncating — silent blank images were
# the disease this stack got treated for, so the log is always on.
VIBE_PREVIEW_DEBUG_LOG="${XDG_RUNTIME_DIR:-/tmp}/.vibe-preview-debug.log"

# Per-process stderr capture for renderer calls; callers rm it on exit.
VIBE_RENDER_ERR="${XDG_RUNTIME_DIR:-/tmp}/.vibe-preview-err.$$"

vibe_has_img2sixel=""
command -v img2sixel >/dev/null 2>&1 && vibe_has_img2sixel=1

# Diagnostics of the most recent vibe_render_sixel call (the viewer's `d`
# key and `vibe show --diag` read these).
last_fmt=unknown
last_ext=unknown
last_native=""
last_renderer=""
last_scale=""
last_rc=0
last_err=""
last_pxw=0
last_pxh=0
last_budw=0
last_budh=0

vibe_default_glob() { # derive "*.png *.jpg ..." from the canonical list
  local e out=""
  for e in $VIBE_IMAGE_EXTS; do out="$out${out:+ }*.$e"; done
  printf '%s' "$out"
}

dlog() {
  local f="$VIBE_PREVIEW_DEBUG_LOG" size
  size="$(stat -c %s -- "$f" 2>/dev/null)" || size=0
  if [ "${size:-0}" -gt 65536 ]; then
    tail -c 32768 -- "$f" >"$f.tmp" 2>/dev/null && mv -f -- "$f.tmp" "$f" 2>/dev/null
  fi
  printf '%s %s\n' "$(date -u +%FT%TZ)" "$*" >>"$f" 2>/dev/null || :
}

sniff_format() { # PATH -> png|jpeg|gif|bmp|webp|avif|tiff|svg|unknown
  local hex
  hex="$(od -An -tx1 -N 32 -- "$1" 2>/dev/null | tr -d ' \n')" || hex=""
  case "$hex" in
    89504e47*) printf 'png' ;;
    ffd8ff*) printf 'jpeg' ;;
    47494638*) printf 'gif' ;;
    424d*) printf 'bmp' ;;
    52494646????????57454250*) printf 'webp' ;; # RIFF....WEBP
    ????????6674797061766966* | ????????6674797061766973*) printf 'avif' ;; # ftyp avif|avis
    49492a00* | 4d4d002a*) printf 'tiff' ;;
    *)
      if head -c 256 -- "$1" 2>/dev/null | grep -qiE '<svg|<\?xml'; then
        printf 'svg'
      else
        printf 'unknown'
      fi
      ;;
  esac
}

ext_format() { # PATH -> the format its extension claims, or unknown
  local e="${1##*.}"
  e="${e,,}"
  case "$e" in
    jpg | jpeg) printf 'jpeg' ;;
    tif | tiff) printf 'tiff' ;;
    png | gif | bmp | webp | avif | svg) printf '%s' "$e" ;;
    *) printf 'unknown' ;;
  esac
}

_vibe_jpeg_dims() { # PATH -> "W H"; walk segments to the first SOF marker
  local path="$1" size off=2 b m len w="" h="" guard=0
  size="$(stat -c %s -- "$path" 2>/dev/null)" || return 1
  while [ $((off + 9)) -le "$size" ] && [ "$guard" -lt 64 ]; do
    guard=$((guard + 1))
    b="" m=""
    read -r b m <<<"$(od -An -tu1 -j "$off" -N 2 -- "$path" 2>/dev/null)" || :
    [ "${b:-0}" -eq 255 ] || return 1
    if [ "${m:-0}" -eq 255 ]; then # fill byte before a marker
      off=$((off + 1))
      continue
    fi
    case "$m" in
      1 | 208 | 209 | 210 | 211 | 212 | 213 | 214 | 215 | 216) # standalone, no length
        off=$((off + 2))
        continue
        ;;
      217 | 218) return 1 ;; # EOI / SOS before any SOF: give up
    esac
    if [ "$m" -ge 192 ] && [ "$m" -le 207 ] && [ "$m" -ne 196 ] && [ "$m" -ne 200 ] && [ "$m" -ne 204 ]; then
      # SOF payload: len(2) precision(1) height(2) width(2)
      read -r h w <<<"$(od -An -tu2 -j $((off + 5)) -N 4 --endian=big -- "$path" 2>/dev/null)" || :
      [ -n "${w:-}" ] && [ -n "${h:-}" ] || return 1
      printf '%s %s' "$w" "$h"
      return 0
    fi
    len=""
    read -r len <<<"$(od -An -tu2 -j $((off + 2)) -N 2 --endian=big -- "$path" 2>/dev/null)" || :
    [ "${len:-0}" -ge 2 ] || return 1
    off=$((off + 2 + len))
  done
  return 1
}

image_dims() { # FMT PATH -> "W H" for the formats img2sixel can decode
  local fmt="$1" path="$2" w="" h=""
  case "$fmt" in
    png) # IHDR: width/height, 32-bit big-endian at offset 16
      read -r w h <<<"$(od -An -tu4 -j 16 -N 8 --endian=big -- "$path" 2>/dev/null)" || :
      ;;
    gif) # logical screen size, 16-bit little-endian at offset 6
      read -r w h <<<"$(od -An -tu2 -j 6 -N 4 --endian=little -- "$path" 2>/dev/null)" || :
      ;;
    bmp) # BITMAPINFOHEADER int32s at 18/22; height negative when top-down
      read -r w h <<<"$(od -An -td4 -j 18 -N 8 --endian=little -- "$path" 2>/dev/null)" || :
      [ "${h:-0}" -lt 0 ] && h=$((-h))
      ;;
    jpeg)
      read -r w h <<<"$(_vibe_jpeg_dims "$path")" || :
      ;;
    *) return 1 ;;
  esac
  # Sanity gate: a parse gone wrong must degrade to chafa, not misrender.
  [ -n "${w:-}" ] && [ -n "${h:-}" ] || return 1
  [ "$w" -ge 1 ] && [ "$w" -le 100000 ] && [ "$h" -ge 1 ] && [ "$h" -le 100000 ] 2>/dev/null || return 1
  printf '%s %s' "$w" "$h"
}

sixel_raster_dims() { # stdin: sixel stream -> "W H" from the raster header
  # Works for chafa ("Pan;Pad;Ph;Pv after params) and img2sixel (q"1;1;W;H).
  head -c 200 | sed -n 's/.*q"[0-9][0-9]*;[0-9][0-9]*;\([0-9][0-9]*\);\([0-9][0-9]*\).*/\1 \2/p'
}

vibe_da1_cache=""
term_has_sixel() { # DA1-probe the directly attached terminal (never in tmux)
  if [ -z "$vibe_da1_cache" ]; then
    vibe_da1_cache=no
    if [ -t 0 ] && [ -t 1 ] && [ -z "${TMUX:-}" ]; then
      local reply="" ch
      printf '\033[c' >/dev/tty
      while IFS= read -rsn1 -t 0.3 ch </dev/tty; do
        reply="$reply$ch"
        [ "$ch" = "c" ] && break
      done
      # Attribute 4 = sixel graphics, delimited: ESC[?64;1;...;4;...c
      printf '%s' "$reply" | grep -qE '[;?]4[;c]' && vibe_da1_cache=yes
    fi
  fi
  [ "$vibe_da1_cache" = yes ]
}

vibe_cell_px_cache=""
term_cell_px() { # -> "CELLW CELLH": XTWINOPS 16 when answered, else 10 20
  if [ -z "$vibe_cell_px_cache" ]; then
    vibe_cell_px_cache="10 20" # the conservative estimate the budgets assume
    if [ -t 0 ] && [ -t 1 ] && [ -z "${TMUX:-}" ]; then
      local reply="" ch cw chh
      printf '\033[16t' >/dev/tty
      while IFS= read -rsn1 -t 0.3 ch </dev/tty; do
        reply="$reply$ch"
        [ "$ch" = "t" ] && break
      done
      read -r chh cw <<<"$(printf '%s' "$reply" |
        sed -n 's/.*\[6;\([0-9][0-9]*\);\([0-9][0-9]*\)t.*/\1 \2/p')" || :
      if [ "${cw:-0}" -ge 4 ] && [ "${cw:-0}" -le 64 ] &&
        [ "${chh:-0}" -ge 6 ] && [ "${chh:-0}" -le 128 ] 2>/dev/null; then
        vibe_cell_px_cache="$cw $chh"
      fi
    fi
  fi
  printf '%s' "$vibe_cell_px_cache"
}

# vibe_render_sixel PATH IW IH BUDW BUDH
# Produce a sixel stream for PATH into $raw (empty on failure, with the
# reason in $last_err), plus $pxw/$pxh measured from the raster header.
# IW/IH are the box in cells (chafa sizing), BUDW/BUDH the pixel budget.
# Selection: stb formats with parsable dimensions go to img2sixel — integer
# nearest-neighbor upscale when native fits the budget, lanczos3 fit when
# not — anything else (or any img2sixel surprise) falls back to chafa, whose
# output is measured and re-rendered once if it busts the budget (its
# captured-output cell->pixel mapping is unrelated to the real terminal's;
# never trust it unmeasured).
vibe_render_sixel() {
  local path="$1" iw="$2" ih="$3" budw="$4" budh="$5"
  local dims nw="" nh="" k rc=0 num den iw2 ih2
  raw="" pxw="" pxh=""
  case "$path" in /*) : ;; *) path="./$path" ;; esac # img2sixel has no --
  last_fmt="$(sniff_format "$path")"
  last_ext="$(ext_format "$path")"
  last_native="" last_renderer="" last_scale="" last_rc=0 last_err=""
  last_budw=$budw last_budh=$budh
  : >"$VIBE_RENDER_ERR" 2>/dev/null || :
  if [ -n "$vibe_has_img2sixel" ]; then
    case "$last_fmt" in
      png | jpeg | gif | bmp)
        dims="$(image_dims "$last_fmt" "$path")" || dims=""
        read -r nw nh <<<"$dims" || :
        if [ -n "${nw:-}" ] && [ -n "${nh:-}" ]; then
          last_native="${nw}x${nh}"
          last_renderer=img2sixel
          if [ "$nw" -le "$budw" ] && [ "$nh" -le "$budh" ]; then
            # Crisp pixels: largest integer multiple that fits the budget.
            k=$((budw / nw))
            [ $((budh / nh)) -lt "$k" ] && k=$((budh / nh))
            [ "$k" -lt 1 ] && k=1
            last_scale="nearest x${k} (${nw}x${nh} -> $((nw * k))x$((nh * k)))"
            raw="$(img2sixel -S -w "$((nw * k))px" -r nearest "$path" 2>"$VIBE_RENDER_ERR")" || rc=$?
          else
            # Native dims are known, so fit the binding axis directly.
            last_scale="lanczos3 fit"
            if [ $((nw * budh)) -gt $((nh * budw)) ]; then
              raw="$(img2sixel -S -w "${budw}px" -r lanczos3 "$path" 2>"$VIBE_RENDER_ERR")" || rc=$?
            else
              raw="$(img2sixel -S -h "${budh}px" -r lanczos3 "$path" 2>"$VIBE_RENDER_ERR")" || rc=$?
            fi
          fi
          last_rc=$rc
          read -r pxw pxh <<<"$(printf '%s' "$raw" | sixel_raster_dims)" || :
          case "$raw" in *$'\x1bP'*) : ;; *) raw="" ;; esac
          # img2sixel exits 0 with empty output on some failures, and a lying
          # file header could oversize the raster — both mean: try chafa.
          if [ -n "$raw" ] && { [ "${pxw:-0}" -gt "$budw" ] || [ "${pxh:-0}" -gt "$budh" ]; }; then
            raw=""
          fi
          if [ -z "$raw" ]; then
            last_err="$(head -n 1 -- "$VIBE_RENDER_ERR" 2>/dev/null)" || last_err=""
            dlog "img2sixel->chafa fallback: $path fmt=$last_fmt rc=$rc${last_err:+ err=$last_err}"
          fi
        fi
        ;;
    esac
  fi
  if [ -z "$raw" ]; then
    last_renderer=chafa
    last_scale="smooth"
    rc=0
    # --animate off is load-bearing: without it chafa PLAYS animated GIFs
    # (multi-frame stream into the capture, blocking for the animation's
    # duration) — the img2sixel path's -S is the same guard.
    raw="$(chafa -f sixel --animate off --passthrough none -s "${iw}x${ih}" -- "$path" 2>"$VIBE_RENDER_ERR")" || rc=$?
    last_rc=$rc
    read -r pxw pxh <<<"$(printf '%s' "$raw" | sixel_raster_dims)" || :
    if [ "${pxw:-0}" -gt 0 ] && [ "${pxh:-0}" -gt 0 ] 2>/dev/null; then
      if [ "$pxw" -gt "$budw" ] || [ "$pxh" -gt "$budh" ]; then
        if [ $((pxw * budh)) -gt $((pxh * budw)) ]; then num=$budw den=$pxw; else num=$budh den=$pxh; fi
        iw2=$((iw * num / den))
        ih2=$((ih * num / den))
        [ "$iw2" -lt 1 ] && iw2=1
        [ "$ih2" -lt 1 ] && ih2=1
        raw="$(chafa -f sixel --animate off --passthrough none -s "${iw2}x${ih2}" -- "$path" 2>"$VIBE_RENDER_ERR")" || rc=$?
        last_rc=$rc
        read -r pxw pxh <<<"$(printf '%s' "$raw" | sixel_raster_dims)" || :
      fi
    fi
  fi
  case "$raw" in *$'\x1bP'*) : ;; *) raw="" ;; esac
  last_pxw="${pxw:-0}"
  last_pxh="${pxh:-0}"
  if [ -z "$raw" ]; then
    last_err="$(head -n 1 -- "$VIBE_RENDER_ERR" 2>/dev/null)" || last_err=""
    [ -n "$last_err" ] || last_err="renderer produced no sixel output (rc=$last_rc)"
    return 1
  fi
  return 0
}

vibe_format_mismatch() { # -> " [file is X but named .Y]" or "" (last render)
  if [ "$last_fmt" != unknown ] && [ "$last_ext" != unknown ] &&
    [ "$last_fmt" != "$last_ext" ]; then
    printf ' [file is %s but named %s]' "$last_fmt" "$last_ext"
  fi
}

vibe_diag_report() { # PATH — the standard block after a vibe_render_sixel
  local path="$1" size
  size="$(stat -c %s -- "$path" 2>/dev/null)" || size="?"
  printf 'path:        %s\n' "$path"
  printf 'size:        %s bytes\n' "$size"
  printf 'format:      %s (extension says: %s)%s\n' "$last_fmt" "$last_ext" \
    "$(vibe_format_mismatch)"
  printf 'native size: %s\n' "${last_native:-unknown (dims not parsed -> chafa)}"
  printf 'renderer:    %s (%s)\n' "${last_renderer:-none}" "${last_scale:-n/a}"
  printf 'exit code:   %s\n' "$last_rc"
  printf 'stderr:      %s\n' "${last_err:-<empty>}"
  printf 'raster px:   %sx%s (budget %sx%s)\n' "$last_pxw" "$last_pxh" "$last_budw" "$last_budh"
  printf 'debug log:   %s\n' "$VIBE_PREVIEW_DEBUG_LOG"
}

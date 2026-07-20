#!/usr/bin/env bash
#
# Image review, powered by yazi (baked into the image, pinned by checksum).
#   [DIR]                browse DIR (default: cwd) in the invoking terminal
#   --window SESSION     internal: the preview window's pane process
#   --ensure SESSION     create the preview window detached if absent (used
#                        by the Claude Code hook); prints "created" when it
#                        did, nothing when it already existed
#   --focus SESSION      jump to the window, creating it if needed (prefix+i)
#   --client-id SESSION  print the numeric DDS id the window's yazi answers
#                        on (`ya emit-to <id> reveal PATH` — the hook's path)
#
# Config resolution: the harness config (vibe.yazi plugin, A/R verdict
# keybindings -> vibe-verdict, badge linemode) is the base; a project-owned
# <vibe-dir>/yazi/ (.vibe/, or legacy .devcontainer/; seeded from
# templates/yazi) layers on top — its
# yazi.toml/theme.toml replace wholesale, its keymap entries merge in front.
#
# Baked as /usr/local/bin/vibe-preview (tmux prefix+i can't know the
# per-project harness path); also runs from a harness checkout.
set -uo pipefail

WINDOW_NAME=preview

# Canonicalize our own path (readlink loop, not GNU-only `readlink -f`) so
# --ensure/--focus relaunch the SAME copy: baked spawns baked, checkout
# spawns checkout.
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

# tmux session ids look like "$3"; yazi client ids must be numeric (u64).
# Offset well away from 0 (tmux's FIRST session is "$0", and 0 risks being a
# DDS broadcast/reserved id) and hash non-numeric input instead of collapsing
# it to one shared value. Yazi's auto ids are timestamps — no overlap.
client_id_for() {
  local id="${1#\$}"
  case "$id" in
    '' | *[!0-9]*) id="$(printf '%s' "$1" | cksum | cut -d' ' -f1)" ;;
  esac
  printf '%s\n' "$((10000 + id % 100000))"
}

# Assemble the runtime config home: harness machinery layered under
# project-owned preferences. The harness base (checkout beside us, or the
# baked /usr/local copy) carries the vibe.yazi plugin, the A/R verdict
# keybindings, and the badge linemode; a project's .vibe/yazi/
# overrides yazi.toml/theme.toml wholesale, appends its keymap entries
# (project entries first, so they win), and its init.lua runs after the
# harness's. Also exports the project's config.env (VIBE_REVIEW_DECISIONS
# etc.) into yazi's environment.
resolve_config_home() {
  local dir="$PWD" proj="" base="" c vd
  while [ "$dir" != "/" ]; do
    for vd in .vibe .devcontainer; do
      if [ -d "$dir/$vd/yazi" ] || [ -f "$dir/$vd/compose.yaml" ] || [ -f "$dir/$vd/devcontainer.json" ]; then
        [ -d "$dir/$vd/yazi" ] && proj="$dir/$vd/yazi"
        if [ -f "$dir/$vd/config.env" ]; then
          set -a
          # shellcheck disable=SC1090  # runtime project config
          . "$dir/$vd/config.env"
          set +a
        fi
        break 2
      fi
    done
    dir="$(dirname -- "$dir")"
  done
  for c in "$script_dir/../config/yazi" /usr/local/lib/vibe/yazi; do
    if [ -d "$c" ]; then
      base="$(cd -- "$c" && pwd)"
      break
    fi
  done
  [ -n "$base" ] || return 0

  # Fresh per launch ($$): a running yazi instance keeps lazily requiring
  # plugin files from its config home, so rebuilding a shared path in place
  # would yank files out from under a concurrent instance mid-review.
  # /tmp is container-local; the small dirs vanish with the container.
  local out
  out="/tmp/.vibe-yazi-cfg-$(id -u)-$$"
  rm -rf "$out" && mkdir -p "$out"
  cp -a "$base/." "$out/"
  if [ -n "$proj" ]; then
    local f
    for f in yazi.toml theme.toml; do
      [ -f "$proj/$f" ] && cp -- "$proj/$f" "$out/$f"
    done
    if [ -f "$proj/keymap.toml" ]; then
      # prepend_keymap entries only, both sides; earlier entries win, so the
      # project's come first and can rebind the harness's A/R.
      cat -- "$proj/keymap.toml" "$base/keymap.toml" >"$out/keymap.toml"
    fi
    [ -d "$proj/plugins" ] && cp -a -- "$proj/plugins/." "$out/plugins/"
    if [ -f "$proj/init.lua" ]; then
      printf '\npcall(dofile, "%s")\n' "$proj/init.lua" >>"$out/init.lua"
    fi
  fi
  export YAZI_CONFIG_HOME="$out"
}

case "${1:-}" in
  --ensure | --focus)
    session="${2:-}"
    [ -n "$session" ] || exit 0
    window_cmd="exec bash '$self' --window '$session'"
    if [ "$1" = "--focus" ]; then
      tmux select-window -t "${session}:=${WINDOW_NAME}" 2>/dev/null && exit 0
      tmux new-window -t "$session" -n "$WINDOW_NAME" "$window_cmd" >/dev/null 2>&1
    else
      tmux list-windows -t "$session" -F '#{window_name}' 2>/dev/null |
        grep -qx "$WINDOW_NAME" && exit 0
      tmux new-window -d -t "$session" -n "$WINDOW_NAME" "$window_cmd" >/dev/null 2>&1 &&
        echo created
    fi
    exit 0
    ;;
  --client-id)
    client_id_for "${2:-}"
    exit 0
    ;;
  --window)
    session="${2:-}"
    resolve_config_home
    exec yazi --client-id "$(client_id_for "$session")"
    ;;
  -h | --help)
    awk 'NR > 1 && !/^#/ { exit } NR > 1 { sub(/^# ?/, ""); print }' "$self"
    exit 0
    ;;
esac

# Interactive: vibe review [DIR] — yazi straight in the invoking terminal.
resolve_config_home
exec yazi "$@"

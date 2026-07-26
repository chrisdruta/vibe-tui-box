#!/usr/bin/env bash
# vibe edit.sh — the container-side review-stack launcher: ONE door
# for the tui's f/g/G popups (docs/tui-layout.md "Editor surfaces"),
# reached over `vibe exec` by the host router (scripts/review.sh).
# XDG_CONFIG_HOME lands on the read-only payload, so nvim finds
# nvim/init.lua and lazygit finds lazygit/config.yml under one root;
# data/state/cache go to scratch — nothing durable, no plugin bytes
# outside the image (the binaries and packpath are image-baked,
# internal/builder/install.go).
#
# Usage: edit.sh files|git
set -euo pipefail

mode="${1:-files}"

export XDG_CONFIG_HOME=/vibe/payload/container
scratch="${TMPDIR:-/tmp}/vibe-edit"
export XDG_DATA_HOME="$scratch/data" XDG_STATE_HOME="$scratch/state" XDG_CACHE_HOME="$scratch/cache"
mkdir -p "$XDG_DATA_HOME" "$XDG_STATE_HOME" "$XDG_CACHE_HOME"

case "$mode" in
files)
  # oil is the default_file_explorer: the directory fills the window.
  exec nvim .
  ;;
git)
  exec lazygit
  ;;
*)
  echo "edit.sh: unknown mode '$mode' (files|git)" >&2
  exit 2
  ;;
esac

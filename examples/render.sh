#!/usr/bin/env bash
#
# Regenerate examples/<preset>/ from src/templates/ with the same rendering
# install.sh uses. The examples are committed artifacts so they read well on
# GitHub, and verify.sh diffs them against a real install of every preset —
# edit src/templates/ (or install.sh's preset table), rerun this, commit
# both. Host-side: bash-3.2 compatible.
set -euo pipefail

examples_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "$examples_dir/.." && pwd)"

render_preset() {
  local preset="$1" preset_name="$2" base_image="$3" install_bun="$4" \
    install_rokit="$5" extra_commands="$6" out f
  out="$examples_dir/$preset"
  mkdir -p "$out"
  for f in compose.yaml config.env; do
    sed \
      -e "s|@PRESET_NAME@|$preset_name|" \
      -e "s|@BASE_IMAGE@|$base_image|" \
      -e "s|@INSTALL_BUN@|$install_bun|" \
      -e "s|@INSTALL_ROKIT@|$install_rokit|" \
      -e "s|@EXTRA_COMMANDS@|$extra_commands|" \
      "$repo_root/src/templates/$f" >"$out/$f"
  done
}

# Keep this table in sync with install.sh's preset case (verify.sh catches
# drift by diffing against a real install).
render_preset minimal "Agent Dev" "mcr.microsoft.com/devcontainers/base:debian" false false ""
render_preset python "Python Agent Dev" "mcr.microsoft.com/devcontainers/python:3.14" false false ""
render_preset bun "Bun Agent Dev" "mcr.microsoft.com/devcontainers/base:debian" true false " bun"
render_preset roblox "Roblox Agent Dev" "mcr.microsoft.com/devcontainers/python:3.14" false true " rokit"

echo "Rendered examples for: minimal python bun roblox"

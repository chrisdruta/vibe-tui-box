#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

# These scripts live at <vibe-dir>/harness/src/scripts/ inside a consuming
# project, where <vibe-dir> is .vibe (current layout) or .devcontainer
# (legacy) — the discovery below is positional, so both work unchanged.
HARNESS_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
VIBE_DIR="$(cd -- "$HARNESS_DIR/.." && pwd)"
# shellcheck disable=SC2034  # consumed by sourcing scripts
VIBE_DIR_NAME="$(basename -- "$VIBE_DIR")"

find_repo_root() {
  # Anchor on the project's vibe dir, not the harness: inside the submodule,
  # git rev-parse would report the submodule's own toplevel. Deliberately NOT the
  # $PWD ancestor walk in repo-root.sh: lifecycle scripts belong to a fixed
  # project (where they live); host tools resolve whichever project you're in.
  if root="$(git -C "$VIBE_DIR" rev-parse --show-toplevel 2>/dev/null)"; then
    printf '%s\n' "$root"
  else
    cd -- "$VIBE_DIR/.." && pwd
  fi
}

# shellcheck disable=SC2034  # consumed by the scripts that source this file
REPO_ROOT="$(find_repo_root)"
CONFIG_FILE="$VIBE_DIR/config.env"

if [[ -f "$CONFIG_FILE" ]]; then
  # shellcheck disable=SC1090
  source "$CONFIG_FILE"
fi

DEV_AGENT_CMD="${DEV_AGENT_CMD:-claude}"
DEV_BOOTSTRAP_STRICT="${DEV_BOOTSTRAP_STRICT:-1}"
DEV_AUTO_INSTALL="${DEV_AUTO_INSTALL:-1}"
DEV_AUTO_GIT_HOOKS="${DEV_AUTO_GIT_HOOKS:-1}"
DEV_AUTO_GIT_LFS="${DEV_AUTO_GIT_LFS:-1}"
DEV_ENV_FILE="${DEV_ENV_FILE:-.env}"
DEV_REQUIRED_COMMANDS="${DEV_REQUIRED_COMMANDS:-git gh jq rg uv claude}"

log() {
  printf '[dev] %s\n' "$*"
}

warn() {
  printf '[dev] WARN: %s\n' "$*" >&2
}

fail() {
  printf '[dev] ERROR: %s\n' "$*" >&2
  return 1
}

require_command() {
  local command_name="$1"
  command -v "$command_name" >/dev/null 2>&1 || fail "Required command not found: $command_name"
}

run_step() {
  local description="$1"
  shift
  log "$description"
  if "$@"; then
    return 0
  fi

  if [[ "$DEV_BOOTSTRAP_STRICT" == "1" ]]; then
    fail "$description failed"
  else
    warn "$description failed; continuing because DEV_BOOTSTRAP_STRICT=0"
  fi
}

#!/usr/bin/env bash
#
# Subprocess-light core for container-side scripts: path anchors + log
# helpers. Safe to source from hot-path hooks — never runs git, never
# sources config.env, and sourcing produces no output. Lifecycle scripts
# that also need REPO_ROOT/config.env source lib.sh (which layers on this).
# Deliberately sets no shell options: callers own their tier (AGENTS.md
# "Shell conventions").
#
# These scripts live at <vibe-dir>/harness/src/scripts/ inside a consuming
# project — the discovery is positional, anchored on this file's location,
# never on the caller's cwd.

HARNESS_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
VIBE_DIR="$(cd -- "$HARNESS_DIR/.." && pwd)"
# shellcheck disable=SC2034  # consumed by sourcing scripts
VIBE_DIR_NAME="$(basename -- "$VIBE_DIR")"

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

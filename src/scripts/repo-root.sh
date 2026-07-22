#!/usr/bin/env bash
#
# Sourced helpers shared by the host launcher (`vibe`) and update.sh so the
# project-root walk can't drift between them. Host-side callers may be
# macOS: bash-3.2 only.
#
# find_repo_root_from_pwd prints the nearest ancestor of $PWD that is a
# harness project root, or returns 1. A project root is a directory with
# .vibe/compose.yaml (current layout) or .devcontainer/devcontainer.json
# (legacy devcontainer-engine layout — still recognized so `vibe update`
# and the migration docs can reach an unmigrated project).
#
# vibe_dir_name_for_root ROOT prints ".vibe" or ".devcontainer" for a
# project root (empty + status 1 when neither matches).
#
# Deliberately distinct from lib.sh's find_repo_root: container lifecycle
# scripts anchor on the harness directory they live under (their project is
# fixed by their location), while these host tools anchor on $PWD (a
# PATH-installed `vibe` must resolve whichever project you're standing in).

vibe_dir_name_for_root() {
  if [ -f "$1/.vibe/compose.yaml" ]; then
    printf '.vibe\n'
  elif [ -f "$1/.devcontainer/devcontainer.json" ]; then
    printf '.devcontainer\n'
  else
    return 1
  fi
}

# Project-identity helpers (used by the identity block in `vibe`).
#
# vibe_project_slug ROOT prints the sanitized basename-derived project
# name ("vibe-<basename>"): lowercase, [a-z0-9_-] only. This is the
# HUMAN-READABLE PREFIX of the project identity — alone it collides
# across same-named checkouts, which is why `vibe` appends a
# per-checkout suffix (see vibe_checkout_suffix).
vibe_project_slug() {
  printf 'vibe-%s' "$(basename -- "$1" | tr '[:upper:]' '[:lower:]' | sed 's/[^a-z0-9_-]/-/g')"
}

# vibe_checkout_suffix ROOT prints a short stable per-checkout token:
# the first 8 hex chars of a digest of the canonical (symlink-resolved)
# checkout path. Deterministic on purpose — a deleted project-id file
# regenerates the same identity for an unmoved checkout, so its
# containers are re-adopted rather than orphaned. sha256sum is coreutils
# (Linux); shasum ships with stock macOS; cksum is the POSIX last resort.
vibe_checkout_suffix() {
  local canonical digest
  canonical="$(cd -- "$1" && pwd -P)" || return 1
  if command -v sha256sum >/dev/null 2>&1; then
    digest="$(printf '%s' "$canonical" | sha256sum)"
  elif command -v shasum >/dev/null 2>&1; then
    digest="$(printf '%s' "$canonical" | shasum -a 256)"
  else
    digest="$(printf '%s' "$canonical" | cksum)"
  fi
  printf '%s' "$digest" | tr -dc '0-9a-f' | cut -c1-8
}

# NOTE: the former vibe_resolve_project_name lived here — it wrote the
# per-checkout identity into a container-writable .vibe/.project-id (via a
# symlink-followable .tmp) and ran raw git against the workspace repo. Under the
# host root-of-trust that identity moved into the host trust record
# (~/.vibe/state/projects/<digest>, injected as VIBE_PROJECT_NAME), so the
# function is gone. The identity itself is still vibe_project_slug ROOT +
# "-" + vibe_checkout_suffix ROOT (used by the launcher and store).

find_repo_root_from_pwd() {
  local dir="$PWD"
  while [ "$dir" != "/" ]; do
    if vibe_dir_name_for_root "$dir" >/dev/null; then
      printf '%s\n' "$dir"
      return 0
    fi
    dir="$(dirname -- "$dir")"
  done
  return 1
}

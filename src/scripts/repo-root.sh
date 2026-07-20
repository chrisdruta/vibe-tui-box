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

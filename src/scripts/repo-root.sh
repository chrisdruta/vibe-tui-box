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

# vibe_resolve_project_name ROOT VIBE_DIR_NAME prints the per-checkout
# compose project identity (top finding of the 2026-07 external review):
# a basename-only project name collides across same-named checkouts, and
# the collision is the whole compose namespace — containers, sidecars,
# network, image tags — not just the documented agent-state volume. The
# identity is therefore
#   vibe-<sanitized basename>-<8-hex canonical-path digest>
# persisted in <vibe-dir>/.project-id on first use (per checkout, never
# committed; git worktrees each get their own). The FILE is the identity:
# moving a checkout keeps its containers; deleting the file regenerates
# the same suffix for an unmoved checkout. Checkouts that already ran
# under the unsuffixed name adopt it instead — probed via compose's own
# project + working_dir labels (docker ANDs repeated label filters, which
# is exactly right here), so another same-named repo's containers can
# never trigger adoption. The agent-state volume name stays derived from
# the bare basename: separate, documented ABI (AGENTS.md).
vibe_resolve_project_name() {
  local root="$1" vibe_dir="$2"
  local project_slug project_id_file project_name legacy_probe_ok legacy_ids exclude_file
  project_slug="$(vibe_project_slug "$root")"
  project_id_file="$root/$vibe_dir/.project-id"
  project_name=""
  if [ -f "$project_id_file" ]; then
    project_name="$(tr -d '[:space:]' <"$project_id_file")"
    case "$project_name" in
      "" | *[!a-z0-9_-]*) project_name="" ;; # corrupt/foreign content: reseed
    esac
  fi
  if [ -z "$project_name" ]; then
    legacy_probe_ok=1
    legacy_ids="$(docker ps -aq \
      --filter "label=com.docker.compose.project=$project_slug" \
      --filter "label=com.docker.compose.project.working_dir=$root" \
      2>/dev/null)" || legacy_probe_ok=0
    if [ -n "$legacy_ids" ]; then
      project_name="$project_slug"
    else
      project_name="$project_slug-$(vibe_checkout_suffix "$root")"
    fi
    if [ "$legacy_probe_ok" = 1 ]; then
      # Persist only when the daemon answered — a down daemon could
      # misclassify a pre-suffix checkout as fresh and strand its containers.
      printf '%s\n' "$project_name" >"$project_id_file.tmp" \
        && mv "$project_id_file.tmp" "$project_id_file"
      # Per-checkout and never committed: park the ignore entry in
      # .git/info/exclude (worktree-shared) instead of churning the
      # consumer's .gitignore. Best-effort — a read-only or non-git tree
      # just skips it.
      exclude_file="$(git -C "$root" rev-parse --git-path info/exclude 2>/dev/null || true)"
      if [ -n "$exclude_file" ]; then
        case "$exclude_file" in /*) ;; *) exclude_file="$root/$exclude_file" ;; esac
        if mkdir -p "$(dirname -- "$exclude_file")" 2>/dev/null; then
          grep -qxF "$vibe_dir/.project-id" "$exclude_file" 2>/dev/null \
            || printf '%s\n' "$vibe_dir/.project-id" >>"$exclude_file" 2>/dev/null \
            || true
        fi
      fi
    fi
  fi
  printf '%s\n' "$project_name"
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

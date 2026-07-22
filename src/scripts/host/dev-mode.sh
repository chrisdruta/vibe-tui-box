#!/usr/bin/env bash
#
# vibe dev mode — harness development against the trust store.
#
# The honest model for developing the harness inside its own project (dogfood):
# host execution STILL runs from a materialized, immutable snapshot — never the
# live workspace bind — so a compromised (or just half-edited) working tree can
# never reach host execution automatically (sol C-3). `dev sync` promotes the
# CURRENT working tree of .vibe/harness into a fresh immutable snapshot and
# points the project's trust record at it; the shim then runs that snapshot.
#
#   vibe dev on      snapshot the working tree + switch this project to dev mode
#   vibe dev sync    re-snapshot the working tree (after you edit harness code)
#   vibe dev off     back to normal mode (the committed, trusted pin)
#   vibe dev status  show the current mode + dev snapshot
#
# Host-side: bash-3.2 (stock macOS) only.
set -euo pipefail

repo_root="${1:-}"
harness_dir="${2:-}"
sub="${3:-status}"
if [ -z "$repo_root" ] || [ -z "$harness_dir" ]; then
  echo "usage: dev-mode.sh REPO_ROOT HARNESS_DIR {on|sync|off|status}" >&2; exit 2
fi

# shellcheck source=store.sh disable=SC1091
. "$harness_dir/src/scripts/host/store.sh"
vibe_sanitize_env

home="$(vibe_store_init)" || exit 1
digest="$(vibe_checkout_digest "$repo_root")" || exit 1
record="$(vibe_record_path "$digest")"
work_sub="$repo_root/.vibe/harness"

record_field() { vibe_record_get "$record" "$1" 2>/dev/null || true; }

# Where the harness source being edited actually lives. Normally it is the
# .vibe/harness submodule. In the dogfood repo the harness IS the superproject
# (and .vibe/harness is a stale self-pin), so edit-and-snapshot the superproject.
dev_source() {
  if [ -f "$repo_root/src/scripts/host/store.sh" ] && [ -f "$repo_root/install.sh" ]; then
    printf '%s\n' "$repo_root"
  else
    printf '%s\n' "$work_sub"
  fi
}

# Snapshot the WORKING TREE (incl. uncommitted edits) of the dev source into an
# immutable version dir. Builds a tree object from a throwaway index so the real
# index is untouched, commits it (orphan; identity forced so a sanitized env
# with no user.name still works), then materializes that commit from the source's
# own object store — same hardening as any other version (fsck, symlink
# rejection, manifest, freeze).
snapshot_worktree() {
  local src; src="$(dev_source)"
  [ -e "$src/.git" ] || { echo "vibe dev: no git checkout at $src" >&2; return 1; }
  local tmpidx tree commit
  tmpidx="$(vibe_mktemp "$home/state/lock")" || return 1
  rm -f "$tmpidx"   # git wants to create it fresh
  if ! GIT_INDEX_FILE="$tmpidx" git -C "$src" add -A 2>/dev/null; then
    rm -f "$tmpidx" "$tmpidx.lock"; return 1; fi
  tree="$(GIT_INDEX_FILE="$tmpidx" git -C "$src" write-tree 2>/dev/null)" || { rm -f "$tmpidx" "$tmpidx.lock"; return 1; }
  rm -f "$tmpidx" "$tmpidx.lock"
  commit="$(git -C "$src" \
    -c user.name='vibe dev' -c user.email='dev@vibe.local' \
    commit-tree "$tree" -m 'vibe dev snapshot' 2>/dev/null)" || return 1
  vibe_materialize "$commit" "$src/.git"
}

case "$sub" in
  on | sync)
    dest="$(snapshot_worktree)" || { echo "vibe dev: snapshot failed" >&2; exit 1; }
    devver="$(basename -- "$dest")"
    pname="$(record_field project_name)"
    [ -n "$pname" ] || pname="$(vibe_project_slug "$repo_root")-$(vibe_checkout_suffix "$repo_root")"
    keep_sha="$(record_field sha)"
    [ -n "$keep_sha" ] || keep_sha="$(vibe_read_pin "$repo_root" 2>/dev/null || echo '')"
    vibe_record_write "$record" \
      "sha=$keep_sha" \
      "project_name=$pname" \
      "ws_base=$(basename -- "$repo_root")" \
      "mode=dev" \
      "dev_version=$devver" \
      "root=$(cd -- "$repo_root" && pwd -P)"
    echo "vibe dev: ${sub} — snapshot $devver is now this project's host code."
    echo "  Edit harness code, then: vibe dev sync   (re-snapshots the working tree)"
    echo "  Back to the committed pin: vibe dev off"
    ;;
  off)
    keep_sha="$(record_field sha)"
    [ -n "$keep_sha" ] || keep_sha="$(vibe_read_pin "$repo_root" 2>/dev/null || echo '')"
    [ -n "$keep_sha" ] || { echo "vibe dev: no committed pin to return to — commit a pin first" >&2; exit 1; }
    pname="$(record_field project_name)"
    [ -n "$pname" ] || pname="$(vibe_project_slug "$repo_root")-$(vibe_checkout_suffix "$repo_root")"
    vibe_record_write "$record" \
      "sha=$keep_sha" "project_name=$pname" "ws_base=$(basename -- "$repo_root")" \
      "mode=normal" "root=$(cd -- "$repo_root" && pwd -P)"
    echo "vibe dev: off — back to the committed, trusted pin ($keep_sha)."
    ;;
  status)
    mode="$(record_field mode)"; [ -n "$mode" ] || mode="(no record)"
    echo "project: $repo_root"
    echo "mode:    $mode"
    [ "$mode" = "dev" ] && echo "dev snapshot: $(record_field dev_version)"
    echo "committed pin: $(vibe_read_pin "$repo_root" 2>/dev/null || echo '?')"
    ;;
  *)
    echo "vibe dev: unknown subcommand: $sub (on|sync|off|status)" >&2; exit 2 ;;
esac

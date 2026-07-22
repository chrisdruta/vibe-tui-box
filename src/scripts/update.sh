#!/usr/bin/env bash
#
# vibe update [TAG] — move the harness pin, through the HOST-OWNED MIRROR only.
#
# Host root-of-trust (sol C-2): the workspace submodule .git is container-
# writable, so this NEVER fetches, checks out, or runs porcelain against it —
# a container-planted post-checkout / credential-helper / filter would execute
# on the host. Instead it operates entirely on ~/.vibe/repo.git (the canonical
# mirror), shows the diff from there, stages the superproject gitlink with a
# narrow `update-index --cacheinfo` (no submodule checkout, no hook surface),
# and materializes + trusts the new version. Review-the-diff, stage-only: the
# human still commits and rebuilds.
#
# Runs on the host (`vibe update`) and, staging-only, inside the container.
# Host-side: bash-3.2 (stock macOS) only.
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=repo-root.sh disable=SC1091
. "$script_dir/repo-root.sh"
# shellcheck source=host/store.sh disable=SC1091
. "$script_dir/host/store.sh"

in_container=""
[ -e /.dockerenv ] && in_container=1
[ -n "$in_container" ] || vibe_sanitize_env

repo_root="${VIBE_REPO_ROOT:-}"
[ -n "$repo_root" ] || repo_root="$(find_repo_root_from_pwd || true)"
if [ -z "$repo_root" ]; then
  echo "No .vibe/compose.yaml (or legacy .devcontainer/) found above: $PWD" >&2
  exit 1
fi
vd="$(vibe_dir_name_for_root "$repo_root")"

# The currently-committed pin (as DATA — ls-tree/ls-files, never porcelain).
old_sha="$(vibe_read_pin "$repo_root" 2>/dev/null || true)"

# ── in-container: staging-only, no store, no mirror ────────────────────────
# The overmounted harness tree has no .git and may not be a submodule checkout,
# so we can't run submodule git here. Stage the gitlink the human names; trust +
# materialize land on their next host-side vibe command.
if [ -n "$in_container" ]; then
  target="${1:-}"
  if [ -z "$target" ]; then
    echo "In-container update needs an explicit TAG or SHA (no mirror in here)." >&2
    echo "  vibe update <tag-or-sha>   # stages the gitlink; host trusts it next run" >&2
    exit 2
  fi
  # Resolve a sha from the workspace submodule objects as DATA only (rev-parse
  # reads objects; it does not check out or run hooks).
  sub="$repo_root/$vd/harness"
  new_sha="$(vibe_git -C "$sub" rev-parse -q --verify "$target^{commit}" 2>/dev/null || true)"
  [ -n "$new_sha" ] || { echo "Not a known tag/ref in the harness objects: $target" >&2; exit 1; }
  vibe_git -C "$repo_root" update-index --cacheinfo "160000,$new_sha,$vd/harness" 2>/dev/null \
    || vibe_git -C "$repo_root" update-index --add --cacheinfo "160000,$new_sha,$vd/harness"
  echo "Staged gitlink $vd/harness -> $new_sha (in-container)."
  echo "On the host, run 'vibe update' or just 'vibe' here to review, trust, and rebuild."
  exit 0
fi

# ── host: operate on the canonical mirror ──────────────────────────────────
home="$(vibe_store_init)" || exit 1
if ! vibe_canonical_remote >/dev/null 2>&1; then
  echo "No canonical harness remote recorded. Bootstrap the store first:" >&2
  echo "  $repo_root/$vd/harness/install.sh --self" >&2
  exit 1
fi
mirror="$home/repo.git"
if ! vibe_mirror_refresh; then
  echo "warning: mirror refresh failed (offline?) — using already-fetched objects" >&2
fi

target="${1:-}"
if [ -z "$target" ]; then
  target="$(git -C "$mirror" tag --list 'v*' --sort=-v:refname 2>/dev/null | head -n 1)"
  [ -n "$target" ] || { echo "No version tags in the canonical mirror." >&2; exit 1; }
fi
new_sha="$(git -C "$mirror" rev-parse -q --verify "$target^{commit}" 2>/dev/null || true)"
[ -n "$new_sha" ] || {
  echo "Not a known tag or ref in the canonical mirror: $target" >&2
  echo "Available: git -C $mirror tag --list --sort=-v:refname" >&2
  exit 1
}

if [ "$new_sha" = "$old_sha" ]; then
  echo "Already at $target ($old_sha) — nothing to do."
  exit 0
fi

# Publisher authentication: the target must be release-reachable in the mirror.
if desc="$(vibe_sha_is_release "$new_sha" 2>/dev/null)"; then
  verified="verified — $desc"
else
  verified="UNVERIFIED — not reachable from a release ref in the canonical mirror"
fi

echo
echo "harness: ${old_sha:-<none>} -> $new_sha ($target)"
echo "publisher: $verified"
echo

# CHANGELOG delta + diff, ALL from the mirror (never the workspace).
if [ -n "$old_sha" ] && git -C "$mirror" cat-file -e "$old_sha^{commit}" 2>/dev/null; then
  echo "Changes ($old_sha..$new_sha):"
  git -C "$mirror" log --oneline "$old_sha..$new_sha" 2>/dev/null | head -40 | sed 's/^/  /'
  echo
  echo "Files changed:"
  git -C "$mirror" diff --stat "$old_sha" "$new_sha" -- 2>/dev/null | tail -n 40
else
  echo "(no local base object for $old_sha — showing the target's changelog head)"
  git -C "$mirror" show "$new_sha:CHANGELOG.md" 2>/dev/null | head -60
fi

case "$verified" in
  UNVERIFIED*)
    echo
    echo "!! $target is not reachable from a known release ref. Only continue if you"
    echo "   trust this exact commit."
    ;;
esac

if [ -t 0 ] && [ -t 1 ]; then
  printf '\nStage this pin move and trust it? [y/N]: '
  read -r ans
  case "$ans" in y|Y|yes|YES) ;; *) echo "Aborted (nothing staged)."; exit 1 ;; esac
else
  echo
  echo "Non-interactive: not staging. Use 'vibe provision --sha $new_sha' to trust exactly." >&2
  exit 1
fi

# Stage the gitlink directly — no submodule checkout, no hook surface.
vibe_git -C "$repo_root" update-index --cacheinfo "160000,$new_sha,$vd/harness" 2>/dev/null \
  || vibe_git -C "$repo_root" update-index --add --cacheinfo "160000,$new_sha,$vd/harness"

# Materialize + trust the new version now (review already happened above).
dest="$(vibe_materialize "$new_sha" "$mirror" 2>/dev/null || true)"
[ -n "$dest" ] || { echo "warning: could not materialize $new_sha from the mirror" >&2; }
digest="$(vibe_checkout_digest "$repo_root")"
record="$(vibe_record_path "$digest")"
pname="$(vibe_record_get "$record" project_name 2>/dev/null || true)"
[ -n "$pname" ] || pname="$(vibe_project_slug "$repo_root")-$(vibe_checkout_suffix "$repo_root")"
vibe_record_write "$record" \
  "sha=$new_sha" "project_name=$pname" "ws_base=$(basename -- "$repo_root")" \
  "mode=normal" "root=$(cd -- "$repo_root" && pwd -P)"

# What a rebuild picks up — decide from the mirror diff, not the workspace.
changed="$(git -C "$mirror" diff --name-only "$old_sha" "$new_sha" -- 2>/dev/null || true)"
rebuild=""
if printf '%s\n' "$changed" | grep -qE '^(src/)?Dockerfile$'; then
  rebuild=required
elif printf '%s\n' "$changed" | grep -qE '^(src/)?(scripts/|config/|compose/)'; then
  rebuild=recommended
fi

echo
echo "Staged the pin move and trusted $new_sha. Next:"
echo "  git -C '$repo_root' diff --cached --submodule    # review the move"
echo "  git -C '$repo_root' commit -m \"Update vibe harness to $target\""
echo "  vibe doctor"
if [ "$rebuild" = "required" ]; then
  echo
  echo "Dockerfile changed: rebuild REQUIRED:  vibe rebuild"
elif [ "$rebuild" = "recommended" ]; then
  echo
  echo "Harness scripts/config changed: rebuild to refresh baked copies:  vibe rebuild"
fi

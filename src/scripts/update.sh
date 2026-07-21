#!/usr/bin/env bash
#
# vibe update [TAG] — docs/updating.md's recommended flow as one command:
# fetch tags in the harness submodule, show what the move changes (CHANGELOG
# delta, diff stat), check out the target tag, and STAGE the pin move in the
# consuming repo. Deliberately never commits and never rebuilds: the pin model
# is review-the-diff, and install.sh set the precedent (stage only, the human
# commits). Runs identically on the host (`vibe update`) and inside the
# container (agents: `bash .vibe/harness/scripts/update.sh`) — only
# the rebuild handoff differs, because rebuilds need the host's docker.
# Host-side callers may be macOS: keep this bash-3.2 compatible.
set -euo pipefail

# Repo root: handed over by the vibe launcher, or (direct invocation) the
# shared $PWD ancestor walk — one implementation for both callers.
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=repo-root.sh disable=SC1091
. "$script_dir/repo-root.sh"
repo_root="${VIBE_REPO_ROOT:-}"
if [ -z "$repo_root" ]; then
  repo_root="$(find_repo_root_from_pwd || true)"
fi
if [ -z "$repo_root" ]; then
  echo "No .vibe/compose.yaml (or legacy .devcontainer/) found above: $PWD" >&2
  exit 1
fi

# Works on both layouts: the pin lives at .vibe/harness (current) or
# .devcontainer/harness (legacy — updating across the engine swap is
# exactly when this script must still run there).
vd="$(vibe_dir_name_for_root "$repo_root")"
sub="$repo_root/$vd/harness"
if ! git -C "$sub" rev-parse --git-dir >/dev/null 2>&1; then
  echo "No harness submodule at $sub" >&2
  echo "Initialize it first: git submodule update --init $vd/harness" >&2
  exit 1
fi

old_sha="$(git -C "$sub" rev-parse HEAD)"
old_desc="$(git -C "$sub" describe --tags 2>/dev/null || git -C "$sub" rev-parse --short HEAD)"

# Offline is a degraded mode, not a failure: continue with already-fetched
# tags (matching doctor's offline-only staleness check), but say so.
if ! git -C "$sub" fetch --tags --quiet origin 2>/dev/null; then
  echo "warning: tag fetch failed (offline?) — using already-fetched tags" >&2
fi

target="${1:-}"
if [ -z "$target" ]; then
  target="$(git -C "$sub" tag --list 'v*' --sort=-v:refname | head -n 1)"
  if [ -z "$target" ]; then
    echo "No version tags known in the harness submodule." >&2
    exit 1
  fi
fi
if ! new_sha="$(git -C "$sub" rev-parse -q --verify "$target^{commit}")"; then
  echo "Not a known tag or ref: $target" >&2
  echo "Available: git -C $vd/harness tag --list --sort=-v:refname" >&2
  exit 1
fi

if [ "$new_sha" = "$old_sha" ]; then
  echo "Already at $target ($old_desc) — nothing to do."
  exit 0
fi

echo
echo "harness: $old_desc -> $target"

rollback=""
if git -C "$sub" merge-base --is-ancestor "$new_sha" "$old_sha" 2>/dev/null; then
  rollback=1
fi

# CHANGELOG delta: the newer side's sections down to (excluding) the older
# pin's release heading — on a rollback that is what you are LEAVING.
if [ -n "$rollback" ]; then
  echo "(rollback — the CHANGELOG sections below are what you are leaving)"
  show_ref="$old_sha"
  boundary="$(git -C "$sub" describe --tags --abbrev=0 "$new_sha" 2>/dev/null || echo '')"
else
  show_ref="$target"
  boundary="$(git -C "$sub" describe --tags --abbrev=0 2>/dev/null || echo '')"
fi
echo
git -C "$sub" show "$show_ref:CHANGELOG.md" 2>/dev/null | awk -v tag="$boundary" '
  /^## / { started = 1 }
  started && tag != "" && substr($0, 1, length("## " tag " ")) == "## " tag " " { exit }
  started {
    print
    if (++printed >= 120) { print "[... truncated — full history in CHANGELOG.md]"; exit }
  }
'

echo "Files changed:"
git -C "$sub" diff --stat "$old_sha" "$new_sha" -- | tail -n 40

git -C "$sub" checkout --quiet "$target"
git -C "$repo_root" add "$vd/harness"

changed="$(git -C "$sub" diff --name-only "$old_sha" "$new_sha" --)"
rebuild=""
if printf '%s\n' "$changed" | grep -qE '^(src/)?Dockerfile$'; then
  rebuild=required
elif printf '%s\n' "$changed" | grep -qE '^(src/)?(scripts/|config/|compose/)'; then
  rebuild=recommended
fi
templates_changed=""
if printf '%s\n' "$changed" | grep -qE '^(src/)?templates/'; then
  templates_changed=1
fi
in_container=""
if [ -e /.dockerenv ]; then
  in_container=1
fi

echo
echo "Staged (not committed). Next:"
echo "  git diff --cached --submodule    # review the move"
echo "  git commit -m \"Update vibe harness to $target\""
echo "  ./$vd/vibe doctor"
if [ -n "$templates_changed" ]; then
  echo
  echo "templates/ changed between these pins — review the project-owned files"
  echo "against them (docs/updating.md -> \"Agent-driven update\" covers the merge)."
  echo "(.claude/settings.json hook registrations merge themselves on the next"
  echo " rebuild/bootstrap — additive only; review that diff like this one.)"
fi
if [ "$rebuild" = "required" ]; then
  echo
  if [ -n "$in_container" ]; then
    echo "Dockerfile changed: rebuild REQUIRED — on the HOST (it cannot run in here):"
  else
    echo "Dockerfile changed: rebuild REQUIRED:"
  fi
  echo "  ./$vd/vibe rebuild"
elif [ "$rebuild" = "recommended" ]; then
  echo
  echo "Harness scripts/config changed: checkout copies apply immediately, but"
  echo "the copies baked into the image (tmux conf, prefix+i viewer) refresh"
  if [ -n "$in_container" ]; then
    echo "only on rebuild — on the HOST (it cannot run in here):"
  else
    echo "only on rebuild:"
  fi
  echo "  ./$vd/vibe rebuild"
fi

#!/usr/bin/env bash
#
# vibe store bootstrap — the host root-of-trust "self install" step.
#
# Establishes ~/.vibe: writes the shim onto the host PATH surface, records the
# canonical harness remote (release authenticity anchor), refreshes the host
# mirror, materializes the harness version this checkout is pinned to, and (when
# run from inside a project) writes that project's trust record. This is the one
# necessary bootstrap ceremony: it runs workspace code (this file) exactly once,
# from a checkout the human deliberately invoked — after this, the shim on PATH
# is the only host entry point and never reads workspace code again.
#
# Invoked by `install.sh --self` and at the end of a normal `install.sh`.
# Host-side: bash-3.2 (stock macOS) only.
set -euo pipefail

# Resolve this script's harness dir, then source the store library from it.
self_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
harness_dir="$(cd -- "$self_dir/../../.." && pwd)"
# shellcheck source=store.sh disable=SC1091
. "$self_dir/store.sh"

remote=""
project_root=""
project_name=""
ws_base=""
quiet=0
while [ $# -gt 0 ]; do
  case "$1" in
    --remote) remote="$2"; shift 2 ;;
    --project-root) project_root="$2"; shift 2 ;;
    --project-name) project_name="$2"; shift 2 ;;
    --ws-base) ws_base="$2"; shift 2 ;;
    --quiet) quiet=1; shift ;;
    *) printf 'self-install: unknown arg: %s\n' "$1" >&2; exit 2 ;;
  esac
done

say() { [ "$quiet" = 1 ] || printf '%s\n' "$*"; }

vibe_sanitize_env
home="$(vibe_store_init)" || { printf 'self-install: could not create a secure store\n' >&2; exit 1; }

# 1. Shim onto the PATH surface (~/.vibe/bin/vibe).
if [ -f "$harness_dir/src/templates/shim" ]; then
  cp -f "$harness_dir/src/templates/shim" "$home/bin/vibe"
  chmod 755 "$home/bin/vibe"
  say "Installed shim: $home/bin/vibe"
else
  printf 'self-install: shim template missing at %s/src/templates/shim\n' "$harness_dir" >&2
  exit 1
fi

# 2. Canonical remote (release-authenticity anchor). Prefer an explicit
#    --remote; else the harness checkout's own origin. NEVER derived from a
#    project's .gitmodules.
if [ -z "$remote" ]; then
  remote="$(vibe_git -C "$harness_dir" remote get-url origin 2>/dev/null || true)"
fi
if [ -n "$remote" ]; then
  vibe_set_canonical_remote "$remote" || say "self-install: keeping existing canonical remote"
  say "Canonical remote: $(vibe_canonical_remote 2>/dev/null || echo '(unset)')"
  vibe_mirror_refresh >/dev/null 2>&1 || say "self-install: mirror refresh skipped (offline?)"
else
  say "self-install: no canonical remote (no origin on the harness checkout)."
  say "  Set it later: printf '%s\\n' <URL> > $home/canonical-remote"
fi

# 3. Materialize the SHA this checkout is at (the version the shim will run).
sha="$(vibe_git -C "$harness_dir" rev-parse HEAD 2>/dev/null || true)"
if [ -z "$sha" ]; then
  printf 'self-install: could not read the harness HEAD sha\n' >&2
  exit 1
fi
dest=""
if [ -d "$home/repo.git" ]; then
  dest="$(vibe_materialize "$sha" "$home/repo.git" 2>/dev/null || true)"
fi
if [ -z "$dest" ]; then
  # Bootstrap ceremony: materialize from the checkout the human is running.
  dest="$(vibe_materialize "$sha" "$harness_dir/.git" 2>/dev/null || true)"
fi
if [ -z "$dest" ]; then
  printf 'self-install: could not materialize harness %s\n' "$sha" >&2
  exit 1
fi
say "Materialized harness version: ${sha}"

# 4. Project trust record (when self-installing from within a project).
if [ -n "$project_root" ]; then
  digest="$(vibe_checkout_digest "$project_root")" || exit 1
  record="$home/state/projects/$digest"
  [ -n "$project_name" ] || project_name="$(vibe_project_slug "$project_root")-$(vibe_checkout_suffix "$project_root")"
  [ -n "$ws_base" ] || ws_base="$(basename -- "$project_root")"
  vibe_record_write "$record" \
    "sha=$sha" "project_name=$project_name" "ws_base=$ws_base" \
    "mode=normal" "root=$(cd -- "$project_root" && pwd -P)"
  say "Recorded trust for project: $project_root"
fi

# 5. PATH guidance.
case ":$PATH:" in
  *":$home/bin:"*) ;;
  *)
    say ""
    say "Add the vibe shim to your PATH (then restart your shell):"
    say "  echo 'export PATH=\"$home/bin:\$PATH\"' >> ~/.bashrc   # or ~/.zshrc"
    ;;
esac

#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh disable=SC1091
source "$script_dir/lib.sh"

cd -- "$REPO_ROOT"

# Self-heal execution bits on the project-owned launchers: checkouts done with
# core.fileMode=false (Windows-side clones, some VS Code git operations) restore
# these files without the +x recorded at install time. (`dev` is the pre-rename
# wrapper name still present in older installs.)
chmod +x "$DEVCONTAINER_DIR/vibe" "$DEVCONTAINER_DIR/dev" \
  "$DEVCONTAINER_DIR/project/"*.sh 2>/dev/null || true

# Complete GitHub git wiring when — and only when — the user has logged into gh:
# the login is the opt-in; without it this block never runs. gh becomes git's
# credential helper, and git@github.com: remotes rewrite to HTTPS (the container
# has no SSH keys, so a repo cloned over SSH on the host is otherwise push-dead
# in here). Both settings land in the container-local ~/.gitconfig, which is why
# this reruns on every start: it restores them after a rebuild.
if command -v gh >/dev/null 2>&1 && gh auth token >/dev/null 2>&1; then
  gh auth setup-git 2>/dev/null || warn "gh is logged in but credential-helper setup failed"
  git config --global url."https://github.com/".insteadOf "git@github.com:" \
    || warn "could not set the GitHub SSH->HTTPS rewrite"
fi

# pipefail makes the doctor exit status win over tee's.
if ! bash "$script_dir/doctor.sh" 2>&1 | tee /tmp/dev-doctor.log; then
  warn "Environment checks reported problems; see /tmp/dev-doctor.log"
fi

project_hook="$DEVCONTAINER_DIR/project/post-start.sh"
if [[ -f "$project_hook" ]]; then
  if ! bash "$project_hook"; then
    warn "Project post-start hook failed"
  fi
fi

exit 0

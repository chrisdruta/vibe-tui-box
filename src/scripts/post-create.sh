#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh disable=SC1091
source "$script_dir/lib.sh"

cd -- "$REPO_ROOT"
log "Bootstrapping $REPO_ROOT"

# In-container `vibe` on PATH. The host root `./vibe` symlink is gone (host
# root-of-trust: no host-executable workspace entry point); inside the
# container the workspace is within the boundary, so provide the everyday
# spelling here. It execs the RO-overmounted trusted launcher directly — no
# workspace-relative dispatch. ~/.local/bin is on PATH and lives in the
# agent-state volume (persists across rebuilds).
mkdir -p -- "$HOME/.local/bin"
cat >"$HOME/.local/bin/vibe" <<VIBE_SHIM
#!/usr/bin/env bash
exec bash "$REPO_ROOT/.vibe/harness/vibe" "\$@"
VIBE_SHIM
chmod +x "$HOME/.local/bin/vibe"

# One subdir per agent CLI inside the shared state volume.
mkdir -p -- \
  "${CLAUDE_CONFIG_DIR:-$HOME/.agents/claude}" \
  "${CODEX_HOME:-$HOME/.agents/codex}" \
  "$HOME/.agents/grok"

# Keep the persisted Claude state internally consistent with the native installation.
if command -v claude >/dev/null 2>&1; then
  claude_dir="${CLAUDE_CONFIG_DIR:-$HOME/.claude}"
  claude_cfg="$claude_dir/.claude.json"
  mkdir -p -- "$claude_dir"
  if [[ ! -f "$claude_cfg" ]]; then
    printf '{"installMethod":"native","autoUpdates":false}\n' >"$claude_cfg"
    chmod 600 "$claude_cfg"
  elif command -v jq >/dev/null 2>&1; then
    tmp="$(mktemp)"
    jq '.installMethod = "native" | .autoUpdates = false' "$claude_cfg" >"$tmp"
    mv -- "$tmp" "$claude_cfg"
    chmod 600 "$claude_cfg"
  fi
fi

# Codex's own sandbox (bwrap) cannot start in this container — cap_drop ALL
# plus no-new-privileges leaves no unprivileged namespaces, so every mode
# except danger-full-access dies at its first shell command
# (docs/security.md "Inner agent sandboxes"). The container IS the isolation
# boundary; default the config accordingly. Key-absent only — a user-set
# sandbox_mode always wins. Top-level TOML keys must precede any [table],
# so a missing key is PREPENDED, never appended.
if command -v codex >/dev/null 2>&1; then
  codex_cfg="${CODEX_HOME:-$HOME/.agents/codex}/config.toml"
  if [[ ! -f "$codex_cfg" ]]; then
    printf 'sandbox_mode = "danger-full-access"\n' >"$codex_cfg"
    chmod 600 "$codex_cfg"
  elif ! grep -Eq '^sandbox_mode[[:space:]]*=' "$codex_cfg"; then
    tmp="$(mktemp)"
    { printf 'sandbox_mode = "danger-full-access"\n'; cat "$codex_cfg"; } >"$tmp"
    mv -- "$tmp" "$codex_cfg"
    chmod 600 "$codex_cfg"
  fi
fi

# A pin bump can introduce new harness hook registrations, but the
# project-owned .claude/settings.json is seeded exactly once — merge in
# whatever is missing (additive only, command-string identity, never
# touching user entries; the change lands in the git diff for review
# like the pin move itself). Rerunnable via `vibe bootstrap`.
bash "$script_dir/settings-merge.sh" || warn "settings hook merge failed; rerun with: vibe bootstrap"

# Codex plugin for Claude Code (/codex:review etc.) rides on the opt-in Codex CLI.
# User scope keeps plugin state in the persistent ~/.agents volume, not the project.
# The install needs the network, so a failure warns instead of failing bootstrap.
if command -v claude >/dev/null 2>&1 && command -v codex >/dev/null 2>&1; then
  if ! claude plugin list --json 2>/dev/null | jq -e 'any(.[]; .id == "codex@openai-codex")' >/dev/null; then
    log "Installing Codex plugin for Claude Code"
    if ! { claude plugin marketplace add openai/codex-plugin-cc \
        && claude plugin install codex@openai-codex --scope user; }; then
      warn "Codex plugin install failed; retry later with: claude plugin install codex@openai-codex"
    fi
  fi
fi

if [[ "$DEV_AUTO_INSTALL" == "1" ]]; then
  if [[ -f pyproject.toml && -f uv.lock ]]; then
    run_step "Syncing locked Python environment" uv sync --frozen
  fi

  if [[ -f bun.lock || -f bun.lockb ]]; then
    run_step "Installing locked Bun dependencies" bun install --frozen-lockfile
  elif [[ -f pnpm-lock.yaml ]]; then
    run_step "Installing locked pnpm dependencies" pnpm install --frozen-lockfile
  elif [[ -f package-lock.json ]]; then
    run_step "Installing locked npm dependencies" npm ci
  elif [[ -f yarn.lock ]]; then
    run_step "Installing locked Yarn dependencies" yarn install --immutable
  fi

  if [[ -f rokit.toml ]]; then
    run_step "Installing Rokit-managed tools" rokit install
  fi

  if [[ -f wally.toml ]]; then
    run_step "Installing Wally dependencies" wally install
  fi
fi

if [[ "$DEV_AUTO_GIT_HOOKS" == "1" && -d .githooks ]]; then
  hooks_path="$REPO_ROOT/.githooks"
  run_step "Configuring repository Git hooks" git config --local core.hooksPath "$hooks_path"
  # Loud on purpose: .git/config lives on the shared mount, so these hooks
  # also run when git is used on the HOST (docs/security.md); doctor NOTEs it.
  log "NOTE: .githooks/ wired into core.hooksPath — runs on the host too; DEV_AUTO_GIT_HOOKS=0 disables"
fi

if [[ "$DEV_AUTO_GIT_LFS" == "1" && -f .gitattributes ]] \
  && grep -Eq 'filter=lfs|filter[[:space:]]+lfs' .gitattributes; then
  run_step "Initializing Git LFS for this repository" git lfs install --local
fi

project_hook="$VIBE_DIR/project/post-create.sh"
if [[ -f "$project_hook" ]]; then
  run_step "Running project post-create hook" bash "$project_hook"
fi

log "Bootstrap complete. Run: ./$VIBE_DIR_NAME/vibe doctor"

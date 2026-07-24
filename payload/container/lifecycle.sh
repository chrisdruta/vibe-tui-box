#!/usr/bin/env bash
#
# Project lifecycle hook runner. The engine execs this inside the dev
# container after reconcile (docs/architecture.md): `post-create` on
# every up — marker-guarded below, so it runs the hook once per
# container and self-heals a previously failed attempt — and
# `post-start` after each actual create or start. Hooks are workspace
# files (`.vibe/hooks/<mode>.sh`): workload trust, same as the rest of
# the repo; they run as the container user with the workspace as cwd
# and NO env file loaded (secrets enter one process via `vibe run`,
# never ambiently). A nonzero hook exit fails `vibe up`.
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
export VIBE_PAYLOAD="${script_dir%/container}"

mode="${1:-}"
case "$mode" in
  post-create|post-start) ;;
  *)
    echo "Usage: lifecycle.sh post-create|post-start" >&2
    exit 2
    ;;
esac

hook=".vibe/hooks/$mode.sh"

# ── the no-nested-sandbox posture (docs/security.md) ─────────────────
# cap_drop ALL + no-new-privileges denies the unprivileged user
# namespace every inner sandbox needs (codex's bwrap+seccomp, Claude's
# /sandbox, Chromium's): THIS container is the isolation boundary, so
# inner sandboxes are off-by-design, not broken-by-surprise. Everything
# below is best-effort and swallowed — none of it may fail or stall an
# `vibe up` (this script runs under set -e).

# Codex config seed: OpenAI's documented answer for an externally
# sandboxed environment. Key-absent only — a user-set sandbox_mode
# always wins — and PREPENDED, because top-level TOML keys must precede
# any [table]. Runs every post-create: idempotent, self-healing.
codex_seed_config() {
  codex_cfg="$CODEX_HOME/config.toml"
  mkdir -p "$CODEX_HOME" 2>/dev/null || return 0
  if [ ! -f "$codex_cfg" ]; then
    printf 'sandbox_mode = "danger-full-access"\n' >"$codex_cfg" 2>/dev/null || return 0
    chmod 600 "$codex_cfg" 2>/dev/null || true
  elif ! grep -Eq '^sandbox_mode[[:space:]]*=' "$codex_cfg" 2>/dev/null; then
    tmp="$(mktemp 2>/dev/null)" || return 0
    if { printf 'sandbox_mode = "danger-full-access"\n'; cat "$codex_cfg"; } >"$tmp" 2>/dev/null &&
      mv -f "$tmp" "$codex_cfg" 2>/dev/null; then
      chmod 600 "$codex_cfg" 2>/dev/null || true
    else
      rm -f "$tmp" 2>/dev/null || true
    fi
  fi
}

# The official codex plugin pins per-thread sandbox modes over its
# app-server API — $CODEX_HOME/config.toml cannot override them — so
# its review/task threads would bwrap-fail every shell command here.
# Rewrite the pinned modes to danger-full-access: matched against
# plugin v1.0.6's source and a no-op on anything unmatched (a future
# plugin may grow a real override — BACKLOG tracks upstreaming one).
# Re-applied every post-create, so a `claude plugin update` revert
# heals on the next up. Trade-off, documented in configuration.md:
# patched review threads gain workspace write access; git is the undo.
codex_patch_plugin() {
  cc_plugins="${CLAUDE_CONFIG_DIR:-$HOME/.claude}/plugins"
  [ -d "$cc_plugins" ] || return 0
  find "$cc_plugins" -type f \( -name codex.mjs -o -name codex-companion.mjs \) \
    -path '*codex*' 2>/dev/null |
    while IFS= read -r f; do
      sed -i \
        -e 's/options\.sandbox ?? "read-only"/options.sandbox ?? "danger-full-access"/g' \
        -e 's/request\.write ? "workspace-write" : "read-only"/"danger-full-access"/g' \
        -e 's/sandbox: "read-only"/sandbox: "danger-full-access"/g' \
        -e 's/sandbox: "workspace-write"/sandbox: "danger-full-access"/g' \
        "$f" 2>/dev/null || true
    done
}

# Second-opinion plugin auto-install (v1's post-create install,
# revived — /codex:review et al.), when claude and codex ship side by
# side. User scope lives under CLAUDE_CONFIG_DIR on the agent-state
# volume, so one success persists across rebuilds; the marker sits
# beside it so a failed attempt (offline, pre-login CLI) retries on a
# later up instead of never.
codex_plugin_marker="/vibe/agent-state/.vibe-codex-plugin.done"
if [ "$mode" = "post-create" ] && command -v codex >/dev/null 2>&1; then
  [ -n "${CODEX_HOME:-}" ] && codex_seed_config
  if command -v claude >/dev/null 2>&1; then
    if [ ! -e "$codex_plugin_marker" ] &&
      timeout 60 claude plugin marketplace add openai/codex-plugin-cc >/dev/null 2>&1 &&
      timeout 60 claude plugin install codex@openai-codex --scope user >/dev/null 2>&1; then
      : >"$codex_plugin_marker" 2>/dev/null || true
    fi
    codex_patch_plugin
  fi
fi

# Go code intelligence: when the image ships gopls beside claude (the
# go preset's base does), install+enable the official gopls-lsp plugin
# at user scope so the recommendation popup never gates a fresh
# container. The marketplace add must precede the install — a fresh
# config dir knows no marketplaces at all (verified; both steps are
# idempotent re-run). Same contract as the codex block: best-effort,
# marker on the volume, a failed attempt retries on a later up.
gopls_plugin_marker="/vibe/agent-state/.vibe-gopls-plugin.done"
if [ "$mode" = "post-create" ] && [ ! -e "$gopls_plugin_marker" ] &&
  command -v claude >/dev/null 2>&1 && command -v gopls >/dev/null 2>&1; then
  if timeout 60 claude plugin marketplace add anthropics/claude-plugins-official >/dev/null 2>&1 &&
    timeout 60 claude plugin install gopls-lsp@claude-plugins-official --scope user >/dev/null 2>&1; then
    : >"$gopls_plugin_marker" 2>/dev/null || true
  fi
fi

if [ "$mode" = "post-create" ]; then
  # The marker lives in container-local state: it survives stop/start,
  # dies with the container — exactly "once per container". It is only
  # written after the hook succeeds.
  marker_dir="/var/tmp/vibe-lifecycle"
  marker="$marker_dir/post-create.done"
  [ -e "$marker" ] && exit 0
  if [ -r "$hook" ]; then
    bash "$hook"
  fi
  mkdir -p "$marker_dir"
  : >"$marker"
  exit 0
fi

# post-start: nothing to do without a hook.
[ -r "$hook" ] || exit 0
exec bash "$hook"

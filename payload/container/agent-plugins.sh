#!/usr/bin/env bash
#
# Best-effort agent plugin + sandbox seeding, split out of lifecycle.sh
# and run DETACHED from post-create: the marketplace installs below can
# wait through minutes of network timeouts on an offline host, and none
# of that may fail or stall `vibe up`. Everything here is idempotent —
# markers on the agent-state volume make successes sticky and failures
# retry on a later up — and every action and no-op is logged to
# /var/tmp/vibe-agent-plugins.log, so silence is diagnosable instead of
# indistinguishable from success.
#
# ── the no-nested-sandbox posture (docs/security.md) ─────────────────
# cap_drop ALL + no-new-privileges denies the unprivileged user
# namespace every inner sandbox needs (codex's bwrap+seccomp, Claude's
# /sandbox, Chromium's): THIS container is the isolation boundary, so
# inner sandboxes are off-by-design, not broken-by-surprise.
set -u

log_file="/var/tmp/vibe-agent-plugins.log"
log() {
  printf '%s %s\n' "$(date '+%Y-%m-%dT%H:%M:%S')" "$*" >>"$log_file" 2>/dev/null || true
}

# Codex config seed: OpenAI's documented answer for an externally
# sandboxed environment. Key-absent only — a user-set sandbox_mode
# always wins — and PREPENDED, because top-level TOML keys must precede
# any [table]. The presence check accepts leading whitespace (TOML
# does), so an indented user setting is seen and never doubled — a
# duplicate top-level key would brick codex's parser. A table-scoped
# sandbox_mode also matches and suppresses the seed: skipping is the
# safe direction. Runs every post-create: idempotent, self-healing.
codex_seed_config() {
  codex_cfg="$CODEX_HOME/config.toml"
  mkdir -p "$CODEX_HOME" 2>/dev/null || return 0
  if [ ! -f "$codex_cfg" ]; then
    printf 'sandbox_mode = "danger-full-access"\n' >"$codex_cfg" 2>/dev/null || return 0
    chmod 600 "$codex_cfg" 2>/dev/null || true
    log "seeded $codex_cfg"
  elif ! grep -Eq '^[[:space:]]*sandbox_mode[[:space:]]*=' "$codex_cfg" 2>/dev/null; then
    tmp="$(mktemp 2>/dev/null)" || return 0
    if { printf 'sandbox_mode = "danger-full-access"\n'; cat "$codex_cfg"; } >"$tmp" 2>/dev/null &&
      mv -f "$tmp" "$codex_cfg" 2>/dev/null; then
      chmod 600 "$codex_cfg" 2>/dev/null || true
      log "prepended sandbox_mode to $codex_cfg"
    else
      rm -f "$tmp" 2>/dev/null || true
    fi
  fi
}

# The official codex plugin pins per-thread sandbox modes over its
# app-server API — $CODEX_HOME/config.toml cannot override them — so
# its review/task threads would bwrap-fail every shell command here.
# Rewrite the pinned modes to danger-full-access. Scope is twofold:
# only the openai-codex marketplace tree is searched (an unrelated
# plugin whose path or filenames merely mention codex is never
# touched), and the patterns are matched against plugin v1.0.6's
# source, so every file is classified and LOGGED: patched, already
# patched, or — the case that used to be silent — matching no known
# pattern after a plugin update, which means codex threads will
# bwrap-fail until the patterns catch up (BACKLOG tracks upstreaming a
# real override). Re-applied every post-create, so a `claude plugin
# update` revert heals on the next up. Trade-off, documented in
# configuration.md: patched review threads gain workspace write
# access; git is the undo.
codex_patch_plugin() {
  cc_plugins="${CLAUDE_CONFIG_DIR:-$HOME/.claude}/plugins"
  [ -d "$cc_plugins" ] || return 0
  for codex_root in "$cc_plugins/cache/openai-codex" "$cc_plugins/marketplaces/openai-codex"; do
    [ -d "$codex_root" ] || continue
    find "$codex_root" -type f \( -name codex.mjs -o -name codex-companion.mjs \) 2>/dev/null
  done |
    while IFS= read -r f; do
      rel="${f#"$cc_plugins"/}"
      if grep -Eq 'options\.sandbox \?\? "read-only"|sandbox: "(read-only|workspace-write)"|request\.write \? "workspace-write" : "read-only"' "$f" 2>/dev/null; then
        if sed -i \
          -e 's/options\.sandbox ?? "read-only"/options.sandbox ?? "danger-full-access"/g' \
          -e 's/request\.write ? "workspace-write" : "read-only"/"danger-full-access"/g' \
          -e 's/sandbox: "read-only"/sandbox: "danger-full-access"/g' \
          -e 's/sandbox: "workspace-write"/sandbox: "danger-full-access"/g' \
          "$f" 2>/dev/null; then
          log "patched sandbox modes: $rel"
        else
          log "WARNING: sandbox patch failed on $rel"
        fi
      elif grep -q 'danger-full-access' "$f" 2>/dev/null; then
        log "sandbox modes already patched: $rel"
      else
        log "WARNING: $rel matches no known sandbox pattern (plugin updated?) — codex threads may bwrap-fail"
      fi
    done
}

# Second-opinion plugin auto-install (v1's post-create install,
# revived — /codex:review et al.), when claude and codex ship side by
# side. User scope lives under CLAUDE_CONFIG_DIR on the agent-state
# volume, so one success persists across rebuilds; the marker sits
# beside it so a failed attempt (offline, pre-login CLI) retries on a
# later up instead of never.
codex_plugin_marker="/vibe/agent-state/.vibe-codex-plugin.done"
if command -v codex >/dev/null 2>&1; then
  [ -n "${CODEX_HOME:-}" ] && codex_seed_config
  if command -v claude >/dev/null 2>&1; then
    if [ -e "$codex_plugin_marker" ]; then
      log "codex plugin already installed (marker present)"
    elif timeout 60 claude plugin marketplace add openai/codex-plugin-cc >/dev/null 2>&1 &&
      timeout 60 claude plugin install codex@openai-codex --scope user >/dev/null 2>&1; then
      : >"$codex_plugin_marker" 2>/dev/null || true
      log "installed codex plugin"
    else
      log "codex plugin install failed (offline or pre-login?) — retries next up"
    fi
    codex_patch_plugin
  fi
fi

# Go code intelligence: when the image ships gopls beside claude (none
# of the stock presets install it — this arms only when a project's
# extension or hook adds it), install+enable the official gopls-lsp plugin
# at user scope so the recommendation popup never gates a fresh
# container. The marketplace add must precede the install — a fresh
# config dir knows no marketplaces at all (verified; both steps are
# idempotent re-run). Same contract as the codex block: best-effort,
# marker on the volume, a failed attempt retries on a later up.
gopls_plugin_marker="/vibe/agent-state/.vibe-gopls-plugin.done"
if [ ! -e "$gopls_plugin_marker" ] &&
  command -v claude >/dev/null 2>&1 && command -v gopls >/dev/null 2>&1; then
  if timeout 60 claude plugin marketplace add anthropics/claude-plugins-official >/dev/null 2>&1 &&
    timeout 60 claude plugin install gopls-lsp@claude-plugins-official --scope user >/dev/null 2>&1; then
    : >"$gopls_plugin_marker" 2>/dev/null || true
    log "installed gopls-lsp plugin"
  else
    log "gopls-lsp plugin install failed (offline or pre-login?) — retries next up"
  fi
fi

# No trailing `exit`: the script is best-effort (nothing above may fail
# it) and must stay source-able — the payload tests source it to drive
# the functions directly, and `exit` in a sourced file kills the caller.
true

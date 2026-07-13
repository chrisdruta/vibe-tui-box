#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

json_check() {
  if command -v jq >/dev/null 2>&1; then
    jq -e . "$1" >/dev/null
  elif command -v python3 >/dev/null 2>&1; then
    python3 -m json.tool "$1" >/dev/null
  else
    echo "SKIP: no jq or python3 for JSON validation of $1"
  fi
}

# 1. Shell syntax across all scripts (including extensionless launchers).
shell_files=(
  "$repo_root/install.sh"
  "$repo_root/verify.sh"
  "$repo_root/dev"
  "$repo_root/templates/dev"
)
while IFS= read -r -d '' file; do
  shell_files+=("$file")
done < <(find "$repo_root/scripts" "$repo_root/templates/project" "$repo_root/features" \
  -type f -name '*.sh' -print0)

for file in "${shell_files[@]}"; do
  bash -n "$file"
done

if command -v shellcheck >/dev/null 2>&1; then
  shellcheck "${shell_files[@]}"
else
  echo "SKIP: shellcheck not installed"
fi

# Dev Container Feature manifests must be valid JSON.
for manifest in "$repo_root"/features/*/devcontainer-feature.json; do
  json_check "$manifest"
done

# Host-side scripts must stay bash-3.2 compatible (stock macOS bash).
host_side_files=(install.sh verify.sh dev templates/dev scripts/host/start-ollama.sh)
if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
  for file in "${host_side_files[@]}"; do
    docker run --rm -v "$repo_root:/src:ro" bash:3.2 -n "/src/$file"
  done
else
  echo "SKIP: docker unavailable; bash-3.2 syntax gate not run"
fi

# 2. Install each preset into a scratch git repo and validate the result.
# Submodule add clones this repository's HEAD, so uncommitted changes are invisible.
if [[ -n "$(git -C "$repo_root" status --porcelain)" ]]; then
  echo "WARN: scaffold has uncommitted changes; the submodule clone uses HEAD only." >&2
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

for preset in minimal python bun roblox; do
  target="$tmp/$preset-project"
  mkdir -p "$target"
  git -C "$target" init -q -b main
  git -C "$target" -c user.name=verify -c user.email=verify@localhost \
    commit -q --allow-empty -m init
  "$repo_root/install.sh" --preset "$preset" --url "$repo_root" "$target" >/dev/null

  [[ -x "$target/.devcontainer/dev" ]]
  [[ -f "$target/.devcontainer/devcontainer.json" ]]
  [[ -f "$target/.devcontainer/config.env" ]]
  [[ -f "$target/.devcontainer/AGENTS.md" ]]
  [[ -f "$target/.claude/settings.json" ]]
  [[ -f "$target/.devcontainer/harness/Dockerfile" ]]
  [[ -x "$target/.devcontainer/harness/dev" ]]
  [[ -f "$target/.gitmodules" ]]
  json_check "$target/.devcontainer/devcontainer.json"
  json_check "$target/.claude/settings.json"

  # The exec bit must be recorded in the index (survives core.fileMode=false).
  git -C "$target" ls-files -s .devcontainer/dev | grep -q '^100755'

  # No unrendered placeholders may survive.
  if grep -rn '@[A-Z_]*@' "$target/.devcontainer/devcontainer.json" "$target/.devcontainer/config.env"; then
    echo "FAIL: unrendered placeholder in $preset output" >&2
    exit 1
  fi
done

# Preset-specific spot checks.
grep -q '"INSTALL_BUN": "true"' "$tmp/bun-project/.devcontainer/devcontainer.json"
grep -q ' bun"' "$tmp/bun-project/.devcontainer/config.env"
grep -q '"INSTALL_ROKIT": "true"' "$tmp/roblox-project/.devcontainer/devcontainer.json"
grep -q 'devcontainers/python:3.14' "$tmp/python-project/.devcontainer/devcontainer.json"
grep -q 'devcontainers/base:debian' "$tmp/minimal-project/.devcontainer/devcontainer.json"

# --force reinstall over an existing setup must back up and succeed, and must
# never touch an existing .claude/settings.json.
echo '{"comment": "user-edited"}' >"$tmp/minimal-project/.claude/settings.json"
"$repo_root/install.sh" --preset minimal --url "$repo_root" --force "$tmp/minimal-project" >/dev/null
[[ -f "$tmp/minimal-project/.devcontainer/harness/Dockerfile" ]]
compgen -G "$tmp/minimal-project/.devcontainer.backup.*" >/dev/null
grep -q 'user-edited' "$tmp/minimal-project/.claude/settings.json"

echo "Scaffold verification passed."

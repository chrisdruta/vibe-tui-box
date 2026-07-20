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
  "$repo_root/vibe"
  "$repo_root/src/templates/vibe"
)
while IFS= read -r -d '' file; do
  shell_files+=("$file")
done < <(find "$repo_root/src/scripts" "$repo_root/src/templates/project" \
  -type f -name '*.sh' -print0)

for file in "${shell_files[@]}"; do
  bash -n "$file"
done

if command -v shellcheck >/dev/null 2>&1; then
  shellcheck "${shell_files[@]}"
else
  echo "SKIP: shellcheck not installed"
fi

# Host-side scripts must stay bash-3.2 compatible (stock macOS bash).
host_side_files=(install.sh verify.sh vibe src/templates/vibe src/scripts/update.sh src/scripts/repo-root.sh src/scripts/host/start-ollama.sh src/scripts/host/clip-image.sh)
docker_ok=""
if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
  docker_ok=1
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

  [[ -x "$target/.vibe/vibe" ]]
  [[ -f "$target/.vibe/compose.yaml" ]]
  [[ -f "$target/.vibe/config.env" ]]
  [[ -f "$target/.vibe/AGENTS.md" ]]
  [[ -f "$target/.vibe/yazi/yazi.toml" ]]
  [[ -f "$target/.vibe/yazi/keymap.toml" ]]
  [[ -L "$target/vibe" ]]
  [[ -f "$target/.claude/settings.json" ]]
  [[ -f "$target/.vibe/harness/src/Dockerfile" ]]
  [[ -f "$target/.vibe/harness/src/compose/base.yaml" ]]
  [[ -x "$target/.vibe/harness/vibe" ]]
  [[ -f "$target/.gitmodules" ]]
  json_check "$target/.claude/settings.json"

  # The merged compose config must render (interpolations + both layers),
  # with and without the build profile, and list the base image tag. Uses
  # the WORKING-TREE base so pre-commit changes are exercised (the scratch
  # submodule is committed HEAD).
  if [[ -n "$docker_ok" ]] && docker compose version >/dev/null 2>&1; then
    render_config() {
      VIBE_PROJECT_NAME="vibe-verify" \
      VIBE_WORKSPACE_BASENAME="$(basename "$target")" \
      VIBE_REPO_ROOT="$target" \
      VIBE_USER_UID=1000 \
      docker compose --project-name "vibe-verify" --project-directory "$target" \
        -f "$repo_root/src/compose/base.yaml" \
        -f "$target/.vibe/compose.yaml" \
        "$@"
    }
    render_config config >/dev/null
    render_config --profile build config >/dev/null
    render_config config --images | grep -q '^vibe-verify-base$'
    # the build-only base service must not leak into the runtime service set
    if render_config config --services | grep -qx base; then
      echo "FAIL: base service visible without the build profile" >&2
      exit 1
    fi
  fi

  # The exec bit must be recorded in the index (survives core.fileMode=false).
  git -C "$target" ls-files -s .vibe/vibe | grep -q '^100755'

  # No unrendered placeholders may survive.
  if grep -rn '@[A-Z_]*@' "$target/.vibe/compose.yaml" "$target/.vibe/config.env"; then
    echo "FAIL: unrendered placeholder in $preset output" >&2
    exit 1
  fi

  # examples/<preset>/ must equal what install.sh actually seeds — the
  # examples are rendered artifacts, not hand-maintained docs, and this
  # check is what keeps them honest.
  for f in compose.yaml config.env; do
    if ! diff -u "$repo_root/examples/$preset/$f" "$target/.vibe/$f"; then
      echo "FAIL: examples/$preset/$f is stale — re-render it from src/templates" >&2
      echo "      (bash examples/render.sh regenerates all presets)" >&2
      exit 1
    fi
  done
done

# Preset-specific spot checks.
grep -q 'INSTALL_BUN: "true"' "$tmp/bun-project/.vibe/compose.yaml"
grep -q ' bun"' "$tmp/bun-project/.vibe/config.env"
grep -q 'INSTALL_ROKIT: "true"' "$tmp/roblox-project/.vibe/compose.yaml"
grep -q 'devcontainers/python:3.14' "$tmp/python-project/.vibe/compose.yaml"
grep -q 'devcontainers/base:debian' "$tmp/minimal-project/.vibe/compose.yaml"

# --force reinstall over an existing setup must back up and succeed, and must
# never touch an existing .claude/settings.json or the root vibe symlink.
echo '{"comment": "user-edited"}' >"$tmp/minimal-project/.claude/settings.json"
"$repo_root/install.sh" --preset minimal --url "$repo_root" --force "$tmp/minimal-project" >/dev/null
[[ -f "$tmp/minimal-project/.vibe/harness/src/Dockerfile" ]]
compgen -G "$tmp/minimal-project/.vibe.backup.*" >/dev/null
grep -q 'user-edited' "$tmp/minimal-project/.claude/settings.json"
[[ -L "$tmp/minimal-project/vibe" ]]

# Submodule-first (self-install) flow: the user adds the submodule, then runs
# the installer from inside it — target implied, no re-clone. --extras must
# enable the chosen build args; a rerun must refuse (point at vibe update)
# rather than reseed.
target="$tmp/subfirst-project"
mkdir -p "$target"
git -C "$target" init -q -b main
git -C "$target" -c user.name=verify -c user.email=verify@localhost \
  commit -q --allow-empty -m init
git -C "$target" -c protocol.file.allow=always \
  submodule add --quiet -- "$repo_root" .vibe/harness
"$target/.vibe/harness/install.sh" --preset minimal --extras playwright >/dev/null
[[ -f "$target/.vibe/compose.yaml" ]]
[[ -L "$target/vibe" ]]
# playwright is an image extension now: Dockerfile + dockerignore seeded
# (matching the templates), dev build block appended, node implied.
diff -q "$repo_root/src/templates/extensions/playwright/Dockerfile" "$target/.vibe/Dockerfile" >/dev/null
diff -q "$repo_root/src/templates/extensions/dockerignore" "$target/.vibe/.dockerignore" >/dev/null
grep -q '^        INSTALL_NODE: "true"' "$target/.vibe/compose.yaml"   # implied by playwright
# shellcheck disable=SC2016  # ${VIBE_PROJECT_NAME} is literal file content
grep -q '^        VIBE_BASE_IMAGE: ${VIBE_PROJECT_NAME}-base' "$target/.vibe/compose.yaml"
# shellcheck disable=SC2016  # ${VIBE_PROJECT_NAME} is literal file content
grep -q '^    image: ${VIBE_PROJECT_NAME}-dev' "$target/.vibe/compose.yaml"
git -C "$target" ls-files --error-unmatch .vibe/Dockerfile .vibe/.dockerignore >/dev/null 2>&1 || {
  git -C "$target" diff --cached --name-only | grep -q '.vibe/Dockerfile'
}
git -C "$target" submodule status -- .vibe/harness >/dev/null
if "$target/.vibe/harness/install.sh" --preset minimal >/dev/null 2>&1; then
  echo "FAIL: self-install rerun should refuse when .vibe is already seeded" >&2
  exit 1
fi

echo "Scaffold verification passed."

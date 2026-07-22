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
# EVERY script that can run on the host belongs here — the src/scripts/host/
# tree in full, plus the launcher chain and the libs they source.
host_side_files=(
  install.sh verify.sh vibe src/templates/vibe
  src/scripts/update.sh src/scripts/repo-root.sh
  src/scripts/host/tui.sh src/scripts/host/sidebar.sh
  src/scripts/host/dock.sh src/scripts/host/state-render.sh
  src/scripts/host/clip-image.sh src/scripts/host/clip-to-pane.sh
  src/scripts/host/install-tmux.sh src/scripts/host/start-ollama.sh
)
docker_ok=""
if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
  docker_ok=1
  for file in "${host_side_files[@]}"; do
    docker run --rm -v "$repo_root:/src:ro" bash:3.2 -n "/src/$file"
  done
else
  echo "SKIP: docker unavailable; bash-3.2 syntax gate not run"
fi

# Project-identity helpers: pure-bash unit checks (no docker, no daemon).
# shellcheck source=src/scripts/repo-root.sh
. "$repo_root/src/scripts/repo-root.sh"
slug="$(vibe_project_slug "/tmp/My App (v2)")"
[ "$slug" = "vibe-my-app--v2-" ] \
  || { echo "FAIL: vibe_project_slug sanitization: got '$slug'" >&2; exit 1; }
suffix_a="$(vibe_checkout_suffix "$repo_root")"
suffix_b="$(vibe_checkout_suffix "$repo_root")"
case "$suffix_a" in
  *[!0-9a-f]* | "")
    echo "FAIL: vibe_checkout_suffix not hex: '$suffix_a'" >&2; exit 1 ;;
esac
[ "${#suffix_a}" -eq 8 ] \
  || { echo "FAIL: vibe_checkout_suffix length ${#suffix_a}, want 8" >&2; exit 1; }
[ "$suffix_a" = "$suffix_b" ] \
  || { echo "FAIL: vibe_checkout_suffix not deterministic" >&2; exit 1; }
[ "$suffix_a" != "$(vibe_checkout_suffix /tmp)" ] \
  || { echo "FAIL: vibe_checkout_suffix identical for different paths" >&2; exit 1; }
echo "Project-identity helper checks passed."

# Identity resolution end-to-end through the real launcher, docker stubbed
# (branch coverage for .project-id: seed, respect, reseed-on-corrupt,
# legacy adoption, daemon-down non-persistence). Runs anywhere bash runs.
id_tmp="$(mktemp -d)"
mkdir -p "$id_tmp/bin" "$id_tmp/app/.vibe"
cat >"$id_tmp/bin/docker" <<'SHIM'
#!/usr/bin/env bash
# verify.sh stub: `compose ...` succeeds silently; `ps` obeys FAKE_* envs.
case "${1:-}" in
  ps)
    [ -n "${FAKE_DAEMON_DOWN:-}" ] && { echo "daemon down" >&2; exit 1; }
    printf '%s' "${FAKE_LEGACY_IDS:-}"
    ;;
esac
exit 0
SHIM
chmod +x "$id_tmp/bin/docker"
: >"$id_tmp/app/.vibe/compose.yaml"
git -C "$id_tmp/app" init -q
id_vibe() (
  cd "$id_tmp/app" \
    && PATH="$id_tmp/bin:$PATH" VIBE_SKIP_CONTAINER_DISPATCH=1 \
       bash "$repo_root/vibe" config >/dev/null 2>&1
)
id_file="$id_tmp/app/.vibe/.project-id"
want_fresh="vibe-app-$(vibe_checkout_suffix "$id_tmp/app")"

id_vibe
[ "$(cat "$id_file")" = "$want_fresh" ] \
  || { echo "FAIL: fresh checkout id: got '$(cat "$id_file")', want '$want_fresh'" >&2; exit 1; }
grep -qxF '.vibe/.project-id' "$id_tmp/app/.git/info/exclude" \
  || { echo "FAIL: .project-id not in .git/info/exclude" >&2; exit 1; }
id_vibe
[ "$(cat "$id_file")" = "$want_fresh" ] \
  || { echo "FAIL: id not stable across runs" >&2; exit 1; }
printf 'NOT A VALID name!\n' >"$id_file"
id_vibe
[ "$(cat "$id_file")" = "$want_fresh" ] \
  || { echo "FAIL: corrupt id not reseeded" >&2; exit 1; }
rm -f "$id_file"
FAKE_LEGACY_IDS="abc123" id_vibe
[ "$(cat "$id_file")" = "vibe-app" ] \
  || { echo "FAIL: legacy adoption: got '$(cat "$id_file")', want 'vibe-app'" >&2; exit 1; }
rm -f "$id_file"
FAKE_DAEMON_DOWN=1 id_vibe || true
[ ! -e "$id_file" ] \
  || { echo "FAIL: id persisted while the daemon was unreachable" >&2; exit 1; }
rm -rf "$id_tmp"
echo "Project-identity resolution checks passed."

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
# Every toggle renders as a live line at its default when no extra selects it.
grep -q 'INSTALL_CLAUDE_CODE: "true"' "$tmp/minimal-project/.vibe/compose.yaml"
grep -q 'INSTALL_CODEX: "false"' "$tmp/minimal-project/.vibe/compose.yaml"
grep -q 'INSTALL_GROK: "false"' "$tmp/minimal-project/.vibe/compose.yaml"

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
"$target/.vibe/harness/install.sh" --preset minimal --extras codex,playwright >/dev/null
[[ -f "$target/.vibe/compose.yaml" ]]
[[ -L "$target/vibe" ]]
# playwright is an image extension now: Dockerfile + dockerignore seeded
# (matching the templates), dev build block appended, node implied.
diff -q "$repo_root/src/templates/extensions/playwright/Dockerfile" "$target/.vibe/Dockerfile" >/dev/null
diff -q "$repo_root/src/templates/extensions/dockerignore" "$target/.vibe/.dockerignore" >/dev/null
grep -q '^        INSTALL_NODE: "true"' "$target/.vibe/compose.yaml"   # implied by playwright
grep -q '^        INSTALL_CODEX: "true"' "$target/.vibe/compose.yaml"  # value-set by --extras codex
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

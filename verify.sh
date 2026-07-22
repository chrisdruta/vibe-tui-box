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
  "$repo_root/src/templates/shim"
  "$repo_root/src/config/theme.sh"
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
  install.sh verify.sh vibe src/templates/vibe src/templates/shim
  src/scripts/update.sh src/scripts/repo-root.sh
  src/scripts/host/store.sh src/scripts/host/self-install.sh
  src/scripts/host/dev-mode.sh
  src/scripts/host/tui.sh src/scripts/host/sidebar.sh
  src/scripts/host/dock.sh src/scripts/host/state-render.sh
  src/scripts/host/clip-image.sh src/scripts/host/clip-to-pane.sh
  src/scripts/host/install-tmux.sh src/scripts/host/start-ollama.sh
  src/config/theme.sh
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

# Host root-of-trust store lifecycle — pure bash + git, no docker/daemon. Runs
# under a SCRATCH store ($HOME must own it, so put it under $HOME). Covers:
# store validation, materialize (fetch/fsck/archive/manifest/freeze/atomic),
# manifest tamper detection, symlink-tree rejection, records, and the
# structural compose enforcement across a clean config + several attack shapes.
store_tmp="$HOME/.vibe-verify.$$"
export VIBE_HOME="$store_tmp"
export VIBE_ALLOW_INSECURE_HOME=1
rm -rf "$store_tmp"
# shellcheck source=src/scripts/host/store.sh
. "$repo_root/src/scripts/host/store.sh"

vibe_store_init >/dev/null || { echo "FAIL: store init" >&2; exit 1; }
st_sha="$(git -C "$repo_root" rev-parse HEAD)"
st_dest="$(vibe_materialize "$st_sha" "$repo_root/.git" 2>/dev/null)" \
  || { echo "FAIL: materialize" >&2; exit 1; }
vibe_verify_version "$st_sha" >/dev/null 2>&1 \
  || { echo "FAIL: fresh materialize does not verify" >&2; exit 1; }
vibe_verify_exec_paths "$st_dest" >/dev/null 2>&1 \
  || { echo "FAIL: exec-path verification" >&2; exit 1; }
# Tamper → verify must fail.
chmod u+w "$st_dest/vibe"; printf 'x' >>"$st_dest/vibe"
if vibe_verify_version "$st_sha" >/dev/null 2>&1; then
  echo "FAIL: tampered version still verifies" >&2; exit 1
fi
# Re-materialize clean, then an EXTRA file must fail full verification (H7).
st_dest="$(vibe_materialize "$st_sha" "$repo_root/.git" 2>/dev/null)"
chmod u+w "$st_dest"; : >"$st_dest/EXTRA-FILE"
if vibe_verify_version "$st_sha" >/dev/null 2>&1; then
  echo "FAIL: version with an extra file still verifies" >&2; exit 1
fi
rm -f "$st_dest/EXTRA-FILE"
vibe_verify_version "$st_sha" >/dev/null 2>&1 || { echo "FAIL: clean re-verify after removing extra" >&2; exit 1; }
# path security (H6): non-absolute and world-writable dirs are refused.
if vibe_path_is_secure "relative/x" 2>/dev/null; then echo "FAIL: relative store path accepted" >&2; exit 1; fi
st_ww="$store_tmp/ww"; mkdir -p "$st_ww"; chmod 777 "$st_ww"
if vibe_path_is_secure "$st_ww" 2>/dev/null; then echo "FAIL: world-writable store path accepted" >&2; exit 1; fi
chmod 700 "$st_ww"
# Symlink in a source tree → materialize must refuse.
sl_repo="$store_tmp/slrepo"; mkdir -p "$sl_repo"; git -C "$sl_repo" init -q
ln -s /etc/passwd "$sl_repo/evil"; echo hi >"$sl_repo/ok"
git -C "$sl_repo" -c user.email=t@t -c user.name=t add -A
git -C "$sl_repo" -c user.email=t@t -c user.name=t commit -qm x
sl_sha="$(git -C "$sl_repo" rev-parse HEAD)"
if vibe_materialize "$sl_sha" "$sl_repo/.git" >/dev/null 2>&1; then
  echo "FAIL: materialize accepted a tree containing a symlink" >&2; exit 1
fi
# Records: write + typed read, no eval.
st_rec="$store_tmp/state/projects/testrec"
vibe_record_write "$st_rec" "sha=$st_sha" "project_name=vibe-x-abc12345" "mode=normal"
[ "$(vibe_record_get "$st_rec" sha)" = "$st_sha" ] \
  || { echo "FAIL: record sha read" >&2; exit 1; }
[ "$(vibe_record_get "$st_rec" project_name)" = "vibe-x-abc12345" ] \
  || { echo "FAIL: record name read" >&2; exit 1; }
# Structural compose enforcement: clean passes, attacks are rejected.
enf_dir="$store_tmp/enf"; mkdir -p "$enf_dir"
ws=verifyws
cat >"$enf_dir/clean.yaml" <<YAML
services:
  dev:
    user: vscode
    cap_drop: [ALL]
    security_opt: [no-new-privileges:true]
    volumes:
      - type: bind
        source: $store_tmp/versions/x
        target: /workspaces/$ws/.vibe/harness
        read_only: true
YAML
enf_store="$store_tmp/versions"
vibe_enforce_compose "$enf_dir/clean.yaml" "$ws" "/nonexistent-repo" "$enf_store" >/dev/null 2>&1 \
  || { echo "FAIL: clean compose rejected by enforcement" >&2; exit 1; }
for attack in "privileged: true" "cap_add: [SYS_ADMIN]" "network_mode: host"; do
  { printf 'services:\n  dev:\n    user: vscode\n    cap_drop: [ALL]\n'
    printf '    security_opt: [no-new-privileges:true]\n    %s\n' "$attack"
    printf '    volumes:\n      - type: bind\n        source: %s/x\n        target: /workspaces/%s/.vibe/harness\n        read_only: true\n' "$enf_store" "$ws"
  } >"$enf_dir/bad.yaml"
  if vibe_enforce_compose "$enf_dir/bad.yaml" "$ws" "/nonexistent-repo" "$enf_store" >/dev/null 2>&1; then
    echo "FAIL: enforcement accepted a boundary-weakening config ($attack)" >&2; exit 1
  fi
done
# A sidecar that mounts the host root (or a RW/foreign harness overmount) is a
# host escape the text greps miss — the bind-source parse must catch these.
cat >"$enf_dir/rootbind.yaml" <<YAML
services:
  dev:
    user: vscode
    cap_drop: [ALL]
    security_opt: [no-new-privileges:true]
    volumes:
      - type: bind
        source: $enf_store/x
        target: /workspaces/$ws/.vibe/harness
        read_only: true
  sidecar:
    user: root
    volumes:
      - type: bind
        source: /
        target: /host
YAML
if vibe_enforce_compose "$enf_dir/rootbind.yaml" "$ws" "/nonexistent-repo" "$enf_store" >/dev/null 2>&1; then
  echo "FAIL: enforcement accepted a sidecar binding the host root" >&2; exit 1
fi
cat >"$enf_dir/rwharness.yaml" <<YAML
services:
  dev:
    user: vscode
    cap_drop: [ALL]
    security_opt: [no-new-privileges:true]
    volumes:
      - type: bind
        source: $enf_store/x
        target: /workspaces/$ws/.vibe/harness
        read_only: false
YAML
if vibe_enforce_compose "$enf_dir/rwharness.yaml" "$ws" "/nonexistent-repo" "$enf_store" >/dev/null 2>&1; then
  echo "FAIL: enforcement accepted a writable harness overmount" >&2; exit 1
fi
# A compliant DECOY service must not satisfy the dev requirements for an unsafe
# dev, and user: vscodeevil must not pass the (end-anchored) user check.
cat >"$enf_dir/decoy.yaml" <<YAML
services:
  dev:
    user: root
    volumes:
      - type: bind
        source: $enf_store/x
        target: /workspaces/$ws/.vibe/harness
        read_only: true
  decoy:
    user: vscode
    cap_drop: [ALL]
    security_opt: [no-new-privileges:true]
YAML
if vibe_enforce_compose "$enf_dir/decoy.yaml" "$ws" "/nonexistent-repo" "$enf_store" >/dev/null 2>&1; then
  echo "FAIL: enforcement accepted an unsafe dev with a compliant decoy service" >&2; exit 1
fi
# RW bind of a store version at a decoy target (would let the container rewrite
# the trusted tree) must be refused (C1).
cat >"$enf_dir/rwstore.yaml" <<YAML
services:
  dev:
    user: vscode
    cap_drop: [ALL]
    security_opt: [no-new-privileges:true]
    volumes:
      - type: bind
        source: $enf_store/x
        target: /workspaces/$ws/.vibe/harness
        read_only: true
      - type: bind
        source: $enf_store/x
        target: /mnt/decoy
        read_only: false
YAML
if vibe_enforce_compose "$enf_dir/rwstore.yaml" "$ws" "/nonexistent-repo" "$enf_store" >/dev/null 2>&1; then
  echo "FAIL: enforcement accepted a RW store bind at a decoy target" >&2; exit 1
fi
# First contact must FAIL CLOSED without a tty (piped stdin here).
fc_app="$store_tmp/fcapp"; mkdir -p "$fc_app/.vibe"; git -C "$fc_app" init -q
: >"$fc_app/.vibe/compose.yaml"
git -C "$fc_app" update-index --add --cacheinfo "160000,$st_sha,.vibe/harness" 2>/dev/null || true
if vibe_first_contact "$fc_app" "$st_dest" "$(basename "$fc_app")" "vibe-fc" </dev/null >/dev/null 2>&1; then
  echo "FAIL: first_contact did not fail closed on a non-tty" >&2; exit 1
fi
chmod -R u+w "$store_tmp" 2>/dev/null || true
rm -rf "$store_tmp"
unset VIBE_HOME VIBE_ALLOW_INSECURE_HOME
echo "Host root-of-trust store checks passed."

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
  "$repo_root/install.sh" --preset "$preset" --url "$repo_root" "$target" --no-self >/dev/null

  [[ -x "$target/.vibe/vibe" ]]
  [[ -f "$target/.vibe/compose.yaml" ]]
  [[ -f "$target/.vibe/config.env" ]]
  [[ -f "$target/.vibe/AGENTS.md" ]]
  [[ -f "$target/.vibe/yazi/yazi.toml" ]]
  [[ -f "$target/.vibe/yazi/keymap.toml" ]]
  [[ ! -e "$target/vibe" ]]   # host root ./vibe symlink is intentionally gone
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
# never touch an existing .claude/settings.json. The host root ./vibe symlink
# is intentionally not created (host root-of-trust — host uses `vibe` on PATH).
echo '{"comment": "user-edited"}' >"$tmp/minimal-project/.claude/settings.json"
"$repo_root/install.sh" --preset minimal --url "$repo_root" --force "$tmp/minimal-project" --no-self >/dev/null
[[ -f "$tmp/minimal-project/.vibe/harness/src/Dockerfile" ]]
compgen -G "$tmp/minimal-project/.vibe.backup.*" >/dev/null
grep -q 'user-edited' "$tmp/minimal-project/.claude/settings.json"
[[ ! -e "$tmp/minimal-project/vibe" ]]

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
"$target/.vibe/harness/install.sh" --preset minimal --extras codex,playwright --no-self >/dev/null
[[ -f "$target/.vibe/compose.yaml" ]]
[[ ! -e "$target/vibe" ]]
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

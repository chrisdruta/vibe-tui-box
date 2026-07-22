#!/usr/bin/env bash
set -euo pipefail

# The installer runs git against the TARGET repository, whose local config is
# container-writable in a live project. Wrap every git call so a planted
# core.fsmonitor / hook / pager can't execute during the bootstrap ceremony.
# (Custom clean/smudge filters on `git add` remain a documented bootstrap
# residual — install.sh is run deliberately from a checkout you trust.)
git() {
  command git -c core.fsmonitor= -c core.hooksPath=/dev/null \
    -c core.pager=cat -c protocol.ext.allow=never "$@"
}

usage() {
  cat <<'USAGE'
Usage: install.sh [OPTIONS] [TARGET]

Sets up TARGET (default: current directory) to use this harness:
  - adds this repository as a git submodule at .vibe/harness
  - seeds project-owned files: compose.yaml, config.env, vibe, project/ hooks
  - links ./vibe -> .vibe/vibe at the repository root

TARGET must be the top level of an existing git repository.

Two ways to run it:

  submodule-first (no separate clone; TARGET is implied):
    cd my-project
    git submodule add https://github.com/chrisdruta/vibe-tui-box.git .vibe/harness
    .vibe/harness/install.sh

  from a scaffold clone (handy for many projects / harness development):
    ~/dev/vibe-tui-box/install.sh --preset python ~/dev/my-project

With no arguments on a terminal it runs an interactive interview (preset,
optional extras, confirmation); any argument switches to plain flag mode.

Options:
  --preset minimal|python|bun|roblox   Toolchain preset (default: minimal)
  --extras LIST                        Comma-separated extras to enable in the
                                       seeded compose.yaml: codex, grok, node,
                                       playwright (playwright implies node)
  --url URL                            Submodule URL (default: this scaffold's
                                       origin remote, else its local path)
  --ref BRANCH                         Submodule branch to track (default: main)
  --force                              Back up and replace an existing .vibe
                                       (scaffold mode only)
  -h, --help                           Show this help
USAGE
}

preset="minimal"
extras=""
force=0
target="."
url=""
ref="main"
self_only=0
no_self=0
args_given=$#

# `install.sh --self` (store bootstrap only): establish ~/.vibe from this
# checkout — shim on PATH, canonical remote, materialize this pin, and record
# the surrounding project's trust. Runs no seeding. This is the host
# root-of-trust ceremony; see docs/security.md and docs/installation.md.
script_dir_early="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
for _a in "$@"; do
  case "$_a" in
    --self) self_only=1 ;;
    --no-self) no_self=1 ;;
  esac
done
if [[ $self_only -eq 1 ]]; then
  case "$script_dir_early" in
    */.vibe/harness)
      proj="${script_dir_early%/.vibe/harness}"
      exec bash "$script_dir_early/src/scripts/host/self-install.sh" \
        --project-root "$proj" --ws-base "$(basename -- "$proj")" ;;
    *)
      exec bash "$script_dir_early/src/scripts/host/self-install.sh" ;;
  esac
fi

while (($#)); do
  case "$1" in
    --preset)
      [[ $# -ge 2 ]] || { echo "--preset requires a value" >&2; exit 2; }
      preset="$2"
      shift 2
      ;;
    --extras)
      [[ $# -ge 2 ]] || { echo "--extras requires a value" >&2; exit 2; }
      extras="$2"
      shift 2
      ;;
    --url)
      [[ $# -ge 2 ]] || { echo "--url requires a value" >&2; exit 2; }
      url="$2"
      shift 2
      ;;
    --ref)
      [[ $# -ge 2 ]] || { echo "--ref requires a value" >&2; exit 2; }
      ref="$2"
      shift 2
      ;;
    --force)
      force=1
      shift
      ;;
    --self|--no-self)
      # --self handled above (exec's out); --no-self recorded already. Consume.
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    --*)
      echo "Unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
    *)
      if [[ "$target" != "." ]]; then
        echo "Only one TARGET may be supplied." >&2
        exit 2
      fi
      target="$1"
      shift
      ;;
  esac
done

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

# Self-install mode: this script is already the project's .vibe/harness
# submodule (the submodule-first flow) — the target is the repo around it,
# and the submodule-add step is already done (or gets absorbed below).
self_mode=0
case "$script_dir" in
  */.vibe/harness)
    self_mode=1
    inferred="${script_dir%/.vibe/harness}"
    if [[ "$target" != "." ]]; then
      resolved="$(cd -- "$target" 2>/dev/null && pwd)" || resolved=""
      if [[ "$resolved" != "$inferred" ]]; then
        echo "Running from a project's .vibe/harness — TARGET is implied: $inferred" >&2
        exit 2
      fi
    fi
    target="$inferred"
    if [[ $force -eq 1 ]]; then
      echo "--force is for scaffold-clone installs; this project already has .vibe/." >&2
      echo "To refresh the seeded files, reconcile against src/templates/ (docs/updating.md)." >&2
      exit 2
    fi
    ;;
esac

# Interactive interview: no arguments, on a real terminal. Scripted/CI use
# (any argument, or no tty) keeps the exact flag behavior.
if [[ $args_given -eq 0 && -t 0 && -t 1 ]]; then
  echo "vibe harness installer"
  echo
  echo "Preset:"
  echo "  1) minimal  — debian base: shell tools, Claude Code, uv (default)"
  echo "  2) python   — python:3.14 base"
  echo "  3) bun      — debian base + Bun"
  echo "  4) roblox   — python:3.14 base + Rokit"
  read -r -p "Choose [1-4]: " answer
  case "$answer" in
    ''|1) preset=minimal ;;
    2) preset=python ;;
    3) preset=bun ;;
    4) preset=roblox ;;
    *) echo "Unknown choice: $answer" >&2; exit 2 ;;
  esac
  echo
  echo "Extras (image build args, enabled in the seeded compose.yaml):"
  echo "  codex       — OpenAI Codex CLI (+ its Claude Code plugin; pulls in Node)"
  echo "  grok        — xAI Grok Build"
  echo "  node        — Node.js without an extra agent"
  echo "  playwright  — headless-Chromium system libs, as a project image"
  echo "                extension (.vibe/Dockerfile; implies node)"
  read -r -p "Enable extras? (comma-separated, empty for none): " extras
  echo
  if [[ $self_mode -eq 1 ]]; then
    echo "Target (implied): $target"
  else
    read -r -p "Target repository [$(pwd)]: " answer
    [[ -n "$answer" ]] && target="$answer"
  fi
  echo
  echo "Installing preset '$preset'${extras:+ with extras: $extras} into: $target"
  read -r -p "Proceed? [y/N]: " answer
  case "$answer" in
    y|Y|yes|YES) : ;;
    *) echo "Aborted."; exit 1 ;;
  esac
fi

# Preset deltas applied to the templates (compose.yaml build args + the
# required-commands list in config.env). Every INSTALL_* toggle renders as
# a live line in the seeded compose.yaml — presets and extras only change
# the values.
preset_name=""
base_image="mcr.microsoft.com/devcontainers/base:debian"
install_claude_code="true"
install_codex="false"
install_grok="false"
install_node="false"
install_bun="false"
install_rokit="false"
extra_commands=""

case "$preset" in
  minimal)
    preset_name="Agent Dev"
    ;;
  python)
    preset_name="Python Agent Dev"
    base_image="mcr.microsoft.com/devcontainers/python:3.14"
    ;;
  bun)
    preset_name="Bun Agent Dev"
    install_bun="true"
    extra_commands=" bun"
    ;;
  roblox)
    preset_name="Roblox Agent Dev"
    base_image="mcr.microsoft.com/devcontainers/python:3.14"
    install_rokit="true"
    extra_commands=" rokit"
    ;;
  *)
    echo "Unknown preset: $preset" >&2
    exit 2
    ;;
esac

# Validate extras up front; they apply to the rendered compose.yaml below.
extras="$(printf '%s' "$extras" | tr '[:upper:],' '[:lower:] ')"
for extra in $extras; do
  case "$extra" in
    codex|grok|node|playwright) : ;;
    *) echo "Unknown extra: $extra (valid: codex, grok, node, playwright)" >&2; exit 2 ;;
  esac
done
case " $extras " in
  *" playwright "*)
    case " $extras " in
      *" node "*) : ;;
      *) extras="$extras node" ;;
    esac
    ;;
esac

target="$(cd -- "$target" && pwd)" || { echo "TARGET does not exist: $target" >&2; exit 1; }
destination="$target/.vibe"

toplevel="$(git -C "$target" rev-parse --show-toplevel 2>/dev/null)" || {
  echo "TARGET must be a git repository (the harness is added as a submodule)." >&2
  echo "Run: git -C '$target' init" >&2
  exit 1
}
if [[ "$toplevel" != "$target" ]]; then
  echo "TARGET must be the repository top level: $toplevel" >&2
  exit 1
fi

if [[ -z "$url" ]]; then
  url="$(git -C "$script_dir" remote get-url origin 2>/dev/null || true)"
  if [[ -z "$url" ]]; then
    url="$script_dir"
    echo "Note: no origin remote on the scaffold; using its local path as the submodule URL."
    echo "After publishing, update it with:"
    echo "  git submodule set-url .vibe/harness <GITHUB_URL>"
  fi
fi

if [[ -e "$target/.devcontainer" ]]; then
  echo "Note: $target/.devcontainer exists — a legacy devcontainer-engine setup."
  echo "For an existing harness project prefer the migration in docs/updating.md"
  echo "(it preserves your config.env and hooks). Continuing installs .vibe/"
  echo "alongside; remove the old .devcontainer/ once migrated."
fi

# "Already installed" is judged by the seeded files, not the directory: in
# self mode .vibe/ exists by construction (the harness lives inside it).
if [[ -e "$destination/compose.yaml" || -e "$destination/vibe" ]]; then
  if [[ $force -ne 1 ]]; then
    echo "$destination is already seeded." >&2
    if [[ $self_mode -eq 1 ]]; then
      echo "Update the harness pin instead: ./vibe update" >&2
    else
      echo "Review it, remove it, or rerun with --force." >&2
    fi
    exit 1
  fi
  # Best-effort removal of a previously installed harness submodule before backing up.
  git -C "$target" submodule deinit -f -- .vibe/harness >/dev/null 2>&1 || true
  git -C "$target" rm -rq --cached .vibe/harness >/dev/null 2>&1 || true
  git -C "$target" config -f .gitmodules --remove-section 'submodule..vibe/harness' >/dev/null 2>&1 || true
  # Repos migrated from the devcontainer-era layout can carry the OLD
  # section name with the NEW path (submodule ".devcontainer/harness" with
  # path = .vibe/harness) — remove that registration too, or a forced
  # reinstall leaves .gitmodules inconsistent (2026-07 external review).
  git -C "$target" config -f .gitmodules --remove-section 'submodule..devcontainer/harness' >/dev/null 2>&1 || true
  rm -rf "$(git -C "$target" rev-parse --path-format=absolute --git-common-dir)/modules/.vibe/harness"
  backup="$target/.vibe.backup.$(date +%Y%m%d%H%M%S)"
  mv -- "$destination" "$backup"
  echo "Backed up existing configuration to: $backup"
fi

mkdir -p -- "$destination"

render() {
  sed \
    -e "s|@PRESET_NAME@|$preset_name|" \
    -e "s|@BASE_IMAGE@|$base_image|" \
    -e "s|@INSTALL_CLAUDE_CODE@|$install_claude_code|" \
    -e "s|@INSTALL_CODEX@|$install_codex|" \
    -e "s|@INSTALL_GROK@|$install_grok|" \
    -e "s|@INSTALL_NODE@|$install_node|" \
    -e "s|@INSTALL_BUN@|$install_bun|" \
    -e "s|@INSTALL_ROKIT@|$install_rokit|" \
    -e "s|@EXTRA_COMMANDS@|$extra_commands|" \
    "$1" >"$2"
}

# Seed an image extension: copy TEMPLATE to .vibe/Dockerfile (+ its
# .dockerignore) and append the live dev build block to the seeded
# compose.yaml (the commented example in the template stays as reference —
# edit the appended block rather than uncommenting a second dev key).
seed_extension() {
  cp -- "$script_dir/src/templates/extensions/$1/Dockerfile" "$destination/Dockerfile"
  cp -- "$script_dir/src/templates/extensions/dockerignore" "$destination/.dockerignore"
  cat >>"$destination/compose.yaml" <<'YAML'

  # Image extension enabled at install time (.vibe/Dockerfile chains onto
  # the shared image — contract: .vibe/harness/docs/extending.md). This is
  # the live version of the commented dev example above; edit HERE.
  dev:
    image: ${VIBE_PROJECT_NAME}-dev
    build:
      context: ./.vibe
      args:
        VIBE_BASE_IMAGE: ${VIBE_PROJECT_NAME}-base
YAML
}

# Extras flip toggle values before render; playwright additionally seeds
# its image extension after render (it appends to the rendered file).
for extra in $extras; do
  case "$extra" in
    codex) install_codex="true" ;;
    grok) install_grok="true" ;;
    node) install_node="true" ;;
  esac
done
render "$script_dir/src/templates/compose.yaml" "$destination/compose.yaml"
render "$script_dir/src/templates/config.env" "$destination/config.env"
case " $extras " in
  *" playwright "*) seed_extension playwright ;;
esac
cp -- "$script_dir/src/templates/vibe" "$destination/vibe"
cp -- "$script_dir/src/templates/agents.md" "$destination/AGENTS.md"
cp -a -- "$script_dir/src/templates/project" "$destination/project"
cp -a -- "$script_dir/src/templates/yazi" "$destination/yazi"
chmod +x "$destination/vibe" "$destination/project/"*.sh

# No host-executable workspace entry point (host root-of-trust, decision A):
# the root `./vibe` symlink is deliberately NOT created. A workspace file
# cannot safely tell host from container before it has already run, so the
# host spelling is `vibe` on PATH (the ~/.vibe/bin shim, installed by the
# self-step below). The container gets its own `vibe` on PATH from
# post-create.sh. If a legacy `./vibe` symlink is present from an older
# install, remove it — it is now a host-execution hazard.
if [[ -L "$target/vibe" ]]; then
  echo "Removing legacy root ./vibe symlink (host uses 'vibe' on PATH now)."
  rm -f "$target/vibe"
  git -C "$target" rm -q --cached vibe >/dev/null 2>&1 || true
fi

# Claude Code project settings (statusline, image hooks, sudo/.env-read deny).
# Seeded only when the project has none — an existing .claude/settings.json
# is never touched.
claude_settings_seeded=0
if [[ ! -e "$target/.claude/settings.json" ]]; then
  mkdir -p -- "$target/.claude"
  cp -- "$script_dir/src/templates/claude-settings.json" "$target/.claude/settings.json"
  claude_settings_seeded=1
fi

# Submodule registration. In the submodule-first flow this is already done
# (git submodule add put the harness here); a plain `git clone` into
# .vibe/harness gets absorbed by the same submodule-add command. Scaffold
# mode clones fresh. protocol.file.allow permits local-path URLs
# (pre-publish workflow).
if git -C "$target" submodule status -- .vibe/harness >/dev/null 2>&1; then
  : # already a registered submodule
elif [[ $self_mode -eq 1 ]]; then
  git -C "$target" -c protocol.file.allow=always \
    submodule add -- "$url" .vibe/harness
else
  git -C "$target" -c protocol.file.allow=always \
    submodule add -b "$ref" -- "$url" .vibe/harness
fi

git -C "$target" add \
  .vibe/compose.yaml \
  .vibe/config.env \
  .vibe/vibe \
  .vibe/AGENTS.md \
  .vibe/project \
  .vibe/yazi
if [[ -f "$destination/Dockerfile" ]]; then
  git -C "$target" add .vibe/Dockerfile .vibe/.dockerignore
fi
if [[ $claude_settings_seeded -eq 1 ]]; then
  git -C "$target" add .claude/settings.json
fi

# Record the execution bits in the index explicitly: with core.fileMode=false
# (Windows-side clones, some filesystems) `git add` records 644 and every
# checkout would strip +x from the launchers.
git -C "$target" update-index --chmod=+x \
  .vibe/vibe \
  .vibe/project/post-create.sh \
  .vibe/project/post-start.sh

extras_note=""
[[ -n "${extras// /}" ]] && extras_note=" (extras:$extras)"
printf '\nInstalled the %s preset%s in:\n  %s\n\n' \
  "$preset" "$extras_note" "$destination"
echo "The submodule and seeded files are staged; review and commit them:"
echo "  git -C '$target' status"
echo
if [[ $claude_settings_seeded -eq 0 ]]; then
  echo "Existing .claude/settings.json left untouched. To get the harness"
  echo "statusline and image-preview hooks, merge in the keys from:"
  echo "  $script_dir/src/templates/claude-settings.json"
  echo
fi
# Host root-of-trust: bootstrap the ~/.vibe store from this checkout (shim on
# PATH, canonical remote, materialize this pin, record this project's trust) so
# host `vibe` runs only trusted, materialized code — never the workspace copy.
# --no-self skips it (e.g. CI that provisions the store separately).
if [[ $no_self -eq 0 ]]; then
  echo
  echo "Bootstrapping the host trust store (~/.vibe)…"
  bash "$script_dir/src/scripts/host/self-install.sh" \
    --project-root "$target" --ws-base "$(basename -- "$target")" \
    ${url:+--remote "$url"} || {
    echo "Note: store bootstrap did not complete — run it later with:"
    echo "  $script_dir/install.sh --self"
  }
fi

echo
echo "Next:"
echo "  1. Review .vibe/compose.yaml and config.env"
echo "  2. Add '@.vibe/AGENTS.md' to the project's CLAUDE.md or AGENTS.md"
echo "     so agents inherit the container rules (see docs/onboarding.md)"
echo "  3. Ensure ~/.vibe/bin is on your PATH (the bootstrap prints the line)"
echo "  4. Run  vibe up      (needs docker on the host — nothing else)"
echo "  5. Run  vibe agent"
if [[ $self_mode -eq 1 ]]; then
  echo
  echo "The submodule pin is whatever you just cloned; to pin the newest"
  echo "release instead: vibe update    (stages the move for review)"
fi
echo
echo "Optional — GitHub access from inside the container (git push, gh pr):"
echo "  Mint a fine-grained PAT: https://github.com/settings/personal-access-tokens/new"
echo "    Repository access: Only select repositories -> this repo; set an expiration"
echo "    Contents: Read/write      Pull requests:   Read/write"
echo "    Actions:  Read-only       Commit statuses: Read-only"
echo "    Workflows: Read/write only if agents edit .github/workflows/"
echo "  Then in the container: gh auth login  (github.com -> HTTPS -> paste token)"
echo "  Login and git wiring persist per project; details: docs/configuration.md"

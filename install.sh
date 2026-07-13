#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: install.sh [OPTIONS] [TARGET]

Sets up TARGET (default: current directory) to use this harness:
  - adds this repository as a git submodule at .devcontainer/harness
  - seeds project-owned files: devcontainer.json, config.env, dev, project/ hooks

TARGET must be the top level of an existing git repository.

Options:
  --preset minimal|python|bun|roblox   Toolchain preset (default: minimal)
  --url URL                            Submodule URL (default: this scaffold's
                                       origin remote, else its local path)
  --ref BRANCH                         Submodule branch to track (default: main)
  --force                              Back up and replace an existing .devcontainer
  -h, --help                           Show this help
USAGE
}

preset="minimal"
force=0
target="."
url=""
ref="main"

while (($#)); do
  case "$1" in
    --preset)
      [[ $# -ge 2 ]] || { echo "--preset requires a value" >&2; exit 2; }
      preset="$2"
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

# Preset deltas applied to the templates. Extension entries are rendered into the
# devcontainer.json extensions array after "anthropic.claude-code".
preset_name=""
base_image="mcr.microsoft.com/devcontainers/base:debian"
install_bun="false"
install_rokit="false"
extra_extensions=""
extra_commands=""

case "$preset" in
  minimal)
    preset_name="Agent Dev"
    ;;
  python)
    preset_name="Python Agent Dev"
    base_image="mcr.microsoft.com/devcontainers/python:3.14"
    extra_extensions='"ms-python.python", "charliermarsh.ruff"'
    ;;
  bun)
    preset_name="Bun Agent Dev"
    install_bun="true"
    extra_extensions='"biomejs.biome"'
    extra_commands=" bun"
    ;;
  roblox)
    preset_name="Roblox Agent Dev"
    base_image="mcr.microsoft.com/devcontainers/python:3.14"
    install_rokit="true"
    extra_extensions='"JohnnyMorganz.luau-lsp", "evaera.vscode-rojo"'
    extra_commands=" rokit"
    ;;
  *)
    echo "Unknown preset: $preset" >&2
    exit 2
    ;;
esac

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
target="$(cd -- "$target" && pwd)" || { echo "TARGET does not exist: $target" >&2; exit 1; }
destination="$target/.devcontainer"

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
    echo "  git submodule set-url .devcontainer/harness <GITHUB_URL>"
  fi
fi

if [[ -e "$destination" && $force -ne 1 ]]; then
  echo "$destination already exists." >&2
  echo "Review it, remove it, or rerun with --force." >&2
  exit 1
fi

if [[ -e "$destination" ]]; then
  # Best-effort removal of a previously installed harness submodule before backing up.
  git -C "$target" submodule deinit -f -- .devcontainer/harness >/dev/null 2>&1 || true
  git -C "$target" rm -rq --cached .devcontainer/harness >/dev/null 2>&1 || true
  git -C "$target" config -f .gitmodules --remove-section 'submodule..devcontainer/harness' >/dev/null 2>&1 || true
  rm -rf "$(git -C "$target" rev-parse --path-format=absolute --git-common-dir)/modules/.devcontainer/harness"
  backup="$target/.devcontainer.backup.$(date +%Y%m%d%H%M%S)"
  mv -- "$destination" "$backup"
  echo "Backed up existing configuration to: $backup"
fi

mkdir -p -- "$destination"

render() {
  sed \
    -e "s|@PRESET_NAME@|$preset_name|" \
    -e "s|@BASE_IMAGE@|$base_image|" \
    -e "s|@INSTALL_BUN@|$install_bun|" \
    -e "s|@INSTALL_ROKIT@|$install_rokit|" \
    -e "s|@EXTRA_EXTENSIONS@|${extra_extensions:+, $extra_extensions}|" \
    -e "s|@EXTRA_COMMANDS@|$extra_commands|" \
    "$1" >"$2"
}

render "$script_dir/templates/devcontainer.json" "$destination/devcontainer.json"
render "$script_dir/templates/config.env" "$destination/config.env"
cp -- "$script_dir/templates/dev" "$destination/dev"
cp -- "$script_dir/templates/agents.md" "$destination/AGENTS.md"
cp -a -- "$script_dir/templates/project" "$destination/project"
chmod +x "$destination/dev" "$destination/project/"*.sh

# Claude Code project settings (statusline, sudo-deny). Seeded only when the
# project has none — an existing .claude/settings.json is never touched.
claude_settings_seeded=0
if [[ ! -e "$target/.claude/settings.json" ]]; then
  mkdir -p -- "$target/.claude"
  cp -- "$script_dir/templates/claude-settings.json" "$target/.claude/settings.json"
  claude_settings_seeded=1
fi

# protocol.file.allow: permit local-path scaffold URLs (pre-publish workflow).
git -C "$target" -c protocol.file.allow=always \
  submodule add -b "$ref" -- "$url" .devcontainer/harness

git -C "$target" add \
  .devcontainer/devcontainer.json \
  .devcontainer/config.env \
  .devcontainer/dev \
  .devcontainer/AGENTS.md \
  .devcontainer/project
if [[ $claude_settings_seeded -eq 1 ]]; then
  git -C "$target" add .claude/settings.json
fi

# Record the execution bits in the index explicitly: with core.fileMode=false
# (Windows-side clones, some filesystems) `git add` records 644 and every
# checkout would strip +x from the launchers.
git -C "$target" update-index --chmod=+x \
  .devcontainer/dev \
  .devcontainer/project/post-create.sh \
  .devcontainer/project/post-start.sh

printf '\nInstalled the %s preset in:\n  %s\n\n' "$preset" "$destination"
echo "The submodule and seeded files are staged; review and commit them:"
echo "  git -C '$target' status"
echo
if [[ $claude_settings_seeded -eq 0 ]]; then
  echo "Existing .claude/settings.json left untouched. To get the harness"
  echo "statusline, merge in the keys from:"
  echo "  $script_dir/templates/claude-settings.json"
  echo
fi
echo "Next:"
echo "  1. Review .devcontainer/devcontainer.json and config.env"
echo "  2. Add '@.devcontainer/AGENTS.md' to the project's CLAUDE.md or AGENTS.md"
echo "     so agents inherit the container rules (see docs/onboarding.md)"
echo "  3. Run ./.devcontainer/dev up"
echo "  4. Run ./.devcontainer/dev agent"

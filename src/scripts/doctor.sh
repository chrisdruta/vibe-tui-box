#!/usr/bin/env bash
set -uo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh disable=SC1091
source "$script_dir/lib.sh"

cd -- "$REPO_ROOT" || exit 1
status=0

check_ok() {
  printf 'OK    %s\n' "$1"
}

check_miss() {
  printf 'MISS  %s\n' "$1"
  status=1
}

check_note() { # informational: worth seeing, not a failure
  printf 'NOTE  %s\n' "$1"
}

if [[ "$(id -u)" -ne 0 ]]; then
  check_ok "running as non-root user $(id -un)"
else
  check_miss "container session is running as root"
fi

if [[ -w "$REPO_ROOT" ]]; then
  check_ok "workspace is writable: $REPO_ROOT"
else
  check_miss "workspace is not writable: $REPO_ROOT"
fi

for command_name in $DEV_REQUIRED_COMMANDS; do
  if command -v "$command_name" >/dev/null 2>&1; then
    check_ok "$command_name -> $(command -v "$command_name")"
  else
    check_miss "$command_name is not installed"
  fi
done

# Image stack: yazi drives review (file(1) is its mime dependency, ya its
# remote control the hook uses); chafa/img2sixel render `vibe show`.
for command_name in yazi ya file chafa img2sixel; do
  if command -v "$command_name" >/dev/null 2>&1; then
    check_ok "$command_name -> $(command -v "$command_name")"
  else
    check_miss "$command_name is not installed (old image? vibe rebuild)"
  fi
done
if [[ -n "${TMUX:-}" ]]; then
  if tmux display-message -p '#{client_termfeatures}' 2>/dev/null | grep -q sixel; then
    check_ok "tmux client reports sixel support"
  else
    check_miss "no sixel in client_termfeatures (Windows Terminal >= 1.22?); previews degrade to cell art"
  fi
fi
# yazi shells out to chafa when the terminal offers no graphics protocol
# (e.g. the VS Code terminal), passing flags newer than Debian's package —
# a too-old chafa turns that fallback into a blank pane with an error.
if command -v yazi >/dev/null 2>&1 && command -v chafa >/dev/null 2>&1; then
  if chafa --help 2>/dev/null | grep -q -- '--probe'; then
    check_ok "chafa understands --probe (yazi's cell-art fallback works)"
  else
    check_note "chafa $(chafa --version 2>/dev/null | head -n1 | grep -oE '[0-9.]+' | head -n1) lacks --probe; yazi's cell-art fallback fails in non-sixel terminals (review previews there show an error)"
  fi
fi
# The baked config/lib must be readable by this (non-root) user; a bad COPY
# chmod in the Dockerfile can hide it, silently degrading the baked
# vibe-preview (tmux prefix+i) to stock yazi with no review keys.
if [[ -e /usr/local/lib/vibe ]]; then
  if [[ -r /usr/local/lib/vibe/preview-lib.sh && -d /usr/local/lib/vibe/yazi ]]; then
    check_ok "baked preview lib+config readable (/usr/local/lib/vibe)"
  else
    check_miss "/usr/local/lib/vibe unreadable by $(id -un) — prefix+i runs yazi without review config (image perms bug; vibe rebuild on a fixed pin)"
  fi
fi

agent_bin="${DEV_AGENT_CMD%% *}"
if command -v "$agent_bin" >/dev/null 2>&1; then
  check_ok "agent command is available: $DEV_AGENT_CMD"
else
  check_miss "agent command is unavailable: $DEV_AGENT_CMD"
fi

if [[ -S /var/run/docker.sock ]]; then
  check_miss "Docker socket is mounted; this grants broad host control"
else
  check_ok "Docker socket is not mounted"
fi

if command -v sudo >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1; then
  check_miss "passwordless sudo is available"
else
  check_ok "passwordless sudo is unavailable"
fi

# Repo-wired git hooks cross the container boundary: .git/config lives on the
# shared mount, so a hooksPath set in here also fires when git runs on the
# HOST, with real SSH keys (docs/security.md). Keep that visible every run.
hooks_path="$(git config --local --get core.hooksPath 2>/dev/null || true)"
if [[ -n "$hooks_path" ]]; then
  check_note "git hooks wired: core.hooksPath -> $hooks_path — these also run on the HOST; set DEV_AUTO_GIT_HOOKS=0 before pointing at third-party code"
else
  check_ok "no repo-wired git hooks (core.hooksPath unset)"
fi

if [[ -f "$REPO_ROOT/$DEV_ENV_FILE" ]]; then
  check_ok "$DEV_ENV_FILE exists and is only loaded explicitly"
else
  check_ok "$DEV_ENV_FILE is absent (optional)"
fi

# gh absence is already reported via DEV_REQUIRED_COMMANDS above.
if command -v gh >/dev/null 2>&1; then
  if gh auth token >/dev/null 2>&1; then
    if git config --global --get-all credential."https://github.com".helper 2>/dev/null \
        | grep -q 'gh auth git-credential'; then
      check_ok "gh is logged in and wired as git's credential helper"
    else
      check_miss "gh is logged in but git is not wired (rerun post-start, or: gh auth setup-git)"
    fi
  else
    check_ok "gh is not logged in (optional; see configuration.md -> GitHub access)"
  fi
fi

# Harness pin freshness — offline on purpose (doctor must never hang on the
# network): compares only against already-fetched tags; `vibe update` fetches.
# shellcheck disable=SC2153  # HARNESS_DIR comes from the sourced lib.sh
harness_dir="$HARNESS_DIR"
if git -C "$harness_dir" rev-parse --git-dir >/dev/null 2>&1; then
  pin_desc="$(git -C "$harness_dir" describe --tags 2>/dev/null ||
    git -C "$harness_dir" rev-parse --short HEAD 2>/dev/null)"
  newest_tag="$(git -C "$harness_dir" tag --list 'v*' --sort=-v:refname 2>/dev/null | head -n 1)"
  if [[ -z "$newest_tag" ]]; then
    check_ok "harness pin $pin_desc (no fetched tags to compare against)"
  elif git -C "$harness_dir" merge-base --is-ancestor "$newest_tag" HEAD >/dev/null 2>&1; then
    check_ok "harness pin $pin_desc (up to date with fetched tag $newest_tag)"
  else
    check_note "harness pin $pin_desc is behind fetched tag $newest_tag — vibe update stages the move (review/commit stays yours)"
  fi
fi

exit "$status"

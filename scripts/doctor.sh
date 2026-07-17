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

exit "$status"

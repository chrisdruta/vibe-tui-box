#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh disable=SC1091
source "$script_dir/lib.sh"

if (($# == 0)); then
  echo "Usage: env-run.sh COMMAND [ARGUMENT ...]" >&2
  exit 2
fi

cd -- "$REPO_ROOT"
env_path="$REPO_ROOT/$DEV_ENV_FILE"

if [[ -f "$env_path" ]]; then
  set -a
  # shellcheck disable=SC1090
  source "$env_path"
  set +a
fi

exec "$@"

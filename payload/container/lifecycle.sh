#!/usr/bin/env bash
#
# Project lifecycle hook runner. The engine execs this inside the dev
# container after reconcile (docs/architecture.md): `post-create` on
# every up — marker-guarded below, so it runs the hook once per
# container and self-heals a previously failed attempt — and
# `post-start` after each actual create or start. Hooks are workspace
# files (`.vibe/hooks/<mode>.sh`): workload trust, same as the rest of
# the repo; they run as the container user with the workspace as cwd
# and NO env file loaded (secrets enter one process via `vibe run`,
# never ambiently). A nonzero hook exit fails `vibe up`.
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
export VIBE_PAYLOAD="${script_dir%/container}"

mode="${1:-}"
case "$mode" in
  post-create|post-start) ;;
  *)
    echo "Usage: lifecycle.sh post-create|post-start" >&2
    exit 2
    ;;
esac

hook=".vibe/hooks/$mode.sh"

if [ "$mode" = "post-create" ]; then
  # The marker lives in container-local state: it survives stop/start,
  # dies with the container — exactly "once per container". It is only
  # written after the hook succeeds.
  marker_dir="/var/tmp/vibe-lifecycle"
  marker="$marker_dir/post-create.done"
  [ -e "$marker" ] && exit 0
  if [ -r "$hook" ]; then
    bash "$hook"
  fi
  mkdir -p "$marker_dir"
  : >"$marker"
  exit 0
fi

# post-start: nothing to do without a hook.
[ -r "$hook" ] || exit 0
exec bash "$hook"

#!/usr/bin/env bash
# Dev-container entrypoint from the vibe payload. The engine mounts the
# payload read-only and starts the container with this script as its
# only command; it marks the lifecycle ready and then idles as PID 1.
set -euo pipefail

marker="${VIBE_READY_MARKER:-/tmp/vibe-ready}"
printf 'payload=%s\nstarted_at=%s\n' \
  "${VIBE_PAYLOAD_DIGEST:-unknown}" \
  "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$marker"

exec sleep infinity

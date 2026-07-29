#!/usr/bin/env bash
# Dummy in-container services for TUI dogfood — stand-ins for the real
# ambition (headless blender, rojo serve, MCP daemons). Each is one
# svc.sh window in the in-container `services` tmux session;
# `vibe attach services` is the door in. Idempotent on every start;
# delete this file when done looking.
set -euo pipefail

svc="$VIBE_PAYLOAD/container/svc.sh"

# An HTTP daemon on a workspace port — the mcp-proxy / rojo shape.
bash "$svc" web python3 -m http.server 8080 --bind 127.0.0.1

# A chatty long-runner — the headless-render-loop shape.
bash "$svc" logger bash -c 'while true; do echo "[$(date +%T)] tick"; sleep 3; done'

# A quiet long-runner — the idle-daemon shape.
bash "$svc" idler sleep infinity

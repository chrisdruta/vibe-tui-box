# shellcheck shell=bash
#
# vibe agent-state records dir — the ONE derivation, sourced by every
# container script that reads or writes the runtime state records
# (statusline.sh, agent-state-hook.sh, agent-ps.sh). Before this file
# the string was copied per script and had already drifted ($UID vs
# $(id -u)); the records dir is a cross-script contract, so it gets
# the theme.sh treatment: one definition, sourced, never executed.
#
# Pure definition: no subprocesses, no output — safe on the hot paths
# (statusline renders per tick, the state hook fires per tool use).
# Runtime tmpfs only — never the workspace, never the agent-state
# volume. Harness containers get a real tmpfs: compile mounts one at
# XDG_RUNTIME_DIR (model/names.go RuntimeDirTarget), so records clear
# on every container boot and a hard stop/start can't leave a stale
# "working" record behind. The /tmp fallback is for runs outside the
# harness only — container-ephemeral, but writable-layer, not tmpfs.

# shellcheck disable=SC2034  # consumed by sourcing scripts
VIBE_STATE_DIR="${XDG_RUNTIME_DIR:-/tmp}/vibe-agent-state-$UID"

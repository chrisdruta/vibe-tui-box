#!/usr/bin/env bash
#
# session-closed hook: a project session died, so its container-side
# ghost viewers (VIBE_NESTED tmux clients inside the dev container) are
# dead by definition — dispatch the engine's reap for them. With
# detach-on-destroy hopping the client, no `vibe tui` process observes
# a quit anymore, and this hook covers every kill path (prefix+Q,
# choose-tree x, `vibe down`'s UI kill) in one place; the attach-tail
# reap in `vibe tui` stays as the backstop for kill-server, where hooks
# don't run.
#
# $1 = #{hook_session_name} — the only identity that survives the
# session's death. Gate on the engine's own naming (vibe- + truncated
# base32 project id) so popups and stray sessions never dispatch, and
# background the engine call: a hook must never wait on a docker exec.

set -uo pipefail

name="${1:-}"
case "$name" in
vibe-*) ;;
*) exit 0 ;;
esac
case "${name#vibe-}" in
'' | *[!a-z2-7]*) exit 0 ;;
esac

exe="$(tmux show-options -gqv @vibe_exe 2>/dev/null)"
{ [ -n "$exe" ] && [ -x "$exe" ]; } || exit 0
(
  ("$exe" _reap --session "$name" >/dev/null 2>&1) &
)
exit 0

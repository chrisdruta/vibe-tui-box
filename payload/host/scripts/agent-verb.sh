#!/usr/bin/env bash
# agent-verb.sh — run one address-direct engine verb from an
# agent-menu.sh item: the ONE definition behind the menu's stop /
# dismiss / service-stop commands. Menu command strings pass only the
# server-controlled session id and charset-vetted words; the workspace
# path (the `-d` contract the _stop family resolves the project by)
# and the engine binary are fetched here from tmux, never interpolated
# into a tmux shell string — a path with an apostrophe must not ride
# run-shell (clip-to-pane.sh's rule).
#
#   agent-verb.sh CLIENT SESS _stop ADDR [WID]
#   agent-verb.sh CLIENT SESS _dismiss ADDR
#   agent-verb.sh CLIENT SESS _svcstop NAME
#
# WID (optional): a viewer window buried inside the success branch,
# failure-swallowed — a window the operator already closed by hand
# must not flip the report to failed.
#
# Host-side: bash-3.2-safe (stock macOS). Runs under the vibe server
# via run-shell.
set -u

client="${1:-}"
sess="${2:-}"
verb="${3:-}"
arg="${4:-}"
wid="${5:-}"

note() {
  if [ -n "$client" ]; then
    tmux display-message -c "$client" "$1" 2>/dev/null || true
  else
    tmux display-message "$1" 2>/dev/null || true
  fi
}

# Re-vet the vetted: these words came from agent-menu.sh's gates, but
# this script is its own trust boundary.
case "$verb" in _stop | _dismiss | _svcstop) ;; *) exit 0 ;; esac
case "$sess" in '' | *[!\$A-Za-z0-9_-]*) exit 0 ;; esac
case "$arg" in '' | *[!A-Za-z0-9_-]*) exit 0 ;; esac
case "$wid" in '' | @*) ;; *) exit 0 ;; esac

path="$(tmux display-message -p -t "$sess" '#{session_path}' 2>/dev/null)"
[ -n "$path" ] || exit 0
exe="$(tmux show-options -gqv @vibe_exe 2>/dev/null)"
[ -n "$exe" ] && [ -x "$exe" ] || exit 0

case "$verb" in
_stop) ok="stopped" fail="stop failed" ;;
_dismiss) ok="dismissed" fail="dismiss failed" ;;
_svcstop) ok="stopped service" fail="stop failed" ;;
esac

if (cd "$path" && "$exe" "$verb" "$arg" >/dev/null 2>&1); then
  [ -n "$wid" ] && tmux kill-window -t "$wid" 2>/dev/null
  note "vibe: $ok $arg"
else
  note "vibe: $fail — $arg (container down?)"
fi

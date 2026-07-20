#!/usr/bin/env bash
#
# Open the project workspace as native terminal panes — Windows Terminal
# first. Each pane runs one stable `vibe` command from the repo root, so the
# terminal owns tabs/panes/rendering while the per-agent tmux sessions inside
# the container keep persistence: closing the terminal loses the layout, not
# the work, and rerunning `vibe open` reattaches the same sessions.
#
# Invoked by `vibe open [LAYOUT]` on the host (after ensure_up, so panes
# don't race `compose up`). Layouts are hardcoded cases below for now
# (BACKLOG: config-driven layouts). Without wt.exe — macOS, or WSL without
# Windows Terminal — the fallback prints the per-pane commands: any
# terminal's split feature runs them just as well, and that IS the intended
# degraded mode, not an error.
set -euo pipefail

if [ "$#" -lt 2 ]; then
  echo "Usage: open-terminal.sh REPO_ROOT WS_BASE [LAYOUT]" >&2
  echo "(normally invoked via: vibe open [LAYOUT])" >&2
  exit 2
fi
repo_root="$1"
ws_base="$2"
layout="${3:-default}"

# Pane command lines per layout, one string per pane (word-split when the
# fallback prints them; the wt path re-lists them as argv below to keep
# quoting exact). First pane is the new tab, the rest are splits.
case "$layout" in
  default) panes=("agent" "shell" "review") ;;
  agents)  panes=("agent" "agent -a codex") ;;
  tabs)    panes=("agent" "shell" "review") ;;
  *)
    echo "Unknown layout: $layout (available: default, agents, tabs)" >&2
    exit 2
    ;;
esac

if ! command -v wt.exe >/dev/null 2>&1; then
  echo "No wt.exe (Windows Terminal) on PATH — open panes yourself, each running"
  echo "one of these from $repo_root:"
  for pane in "${panes[@]}"; do
    echo "  ./vibe $pane"
  done
  echo "(persistence comes from the tmux sessions inside, not from the panes)"
  exit 0
fi

# Every pane runs `./vibe CMD` via WSL interop. Args stay separate argv all
# the way — interop quotes them Windows-side — and `-e` execs without a
# shell, so nothing here is parsed twice. `--cd` accepts absolute Linux
# paths; -d pins the distro when we know it (multi-distro hosts).
wsl_cmd=(wsl.exe)
if [ -n "${WSL_DISTRO_NAME:-}" ]; then
  wsl_cmd+=(-d "$WSL_DISTRO_NAME")
fi
wsl_cmd+=(--cd "$repo_root" -e ./vibe)

# Pane appearance: without -p, wt renders a commandline pane with the
# DEFAULT profile's settings (often PowerShell's) — that is how a distro
# color scheme goes missing. WSL registers a Windows Terminal profile named
# after the distro, so WSL_DISTRO_NAME is the right default guess;
# VIBE_OPEN_PROFILE overrides it (set it empty to pass no -p at all).
# The ${arr[@]+...} guard keeps empty-array expansion safe under set -u
# on old bash.
profile="${VIBE_OPEN_PROFILE-${WSL_DISTRO_NAME:-}}"
wt_profile=()
if [ -n "$profile" ]; then
  wt_profile=(-p "$profile")
fi

# A lone ";" argv element separates wt subcommands. split-pane -V puts the
# new pane to the right, -H below the focused pane; --size is the fraction
# given to the NEW pane.
wt_args=(new-tab ${wt_profile[@]+"${wt_profile[@]}"} --title "vibe: $ws_base")
case "$layout" in
  default)
    # agent (left, 70%) | shell (right top) / review (right bottom)
    wt_args+=("${wsl_cmd[@]}" agent)
    wt_args+=(";" split-pane ${wt_profile[@]+"${wt_profile[@]}"} -V --size 0.3 "${wsl_cmd[@]}" shell)
    wt_args+=(";" split-pane ${wt_profile[@]+"${wt_profile[@]}"} -H "${wsl_cmd[@]}" review)
    ;;
  agents)
    # claude | codex, half and half
    wt_args+=("${wsl_cmd[@]}" agent)
    wt_args+=(";" split-pane ${wt_profile[@]+"${wt_profile[@]}"} -V --size 0.5 "${wsl_cmd[@]}" agent -a codex)
    ;;
  tabs)
    # Portrait-friendly: the agent owns the whole first tab; shell over
    # review fills a second tab. Ctrl+Tab is the toggle; land on the agent.
    wt_args+=("${wsl_cmd[@]}" agent)
    wt_args+=(";" new-tab ${wt_profile[@]+"${wt_profile[@]}"} --title "vibe: $ws_base extras" "${wsl_cmd[@]}" shell)
    wt_args+=(";" split-pane ${wt_profile[@]+"${wt_profile[@]}"} -H "${wsl_cmd[@]}" review)
    wt_args+=(";" focus-tab -t 0)
    ;;
esac

if [ -n "$profile" ]; then
  # Say which profile got pinned: when the name doesn't exist in WT's
  # settings, wt silently falls back to the default profile's looks — the
  # mismatch is invisible unless we print the guess (docs/usage.md).
  echo "Opening Windows Terminal — layout '$layout', profile '$profile', $ws_base"
else
  echo "Opening Windows Terminal — layout '$layout' (no profile pin), $ws_base"
fi
exec wt.exe "${wt_args[@]}"

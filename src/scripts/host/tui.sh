#!/usr/bin/env bash
#
# `vibe tui` — the riced host-side tmux UI. One tmux server on a dedicated
# socket (-L vibe) for all vibe projects on this host; one session per
# project. The default layout is the pre-`vibe open` workflow folded into
# one surface: agent pane (docker exec -> container tmux, persistence as
# ever) over a native HOST shell dock (git on the host side, vibe clip).
# More windows come from the palette (prefix+Space) or the "+" tab.
#
# Host-side: keep bash-3.2 compatible (stock macOS).

set -euo pipefail

if [ "$#" -lt 4 ]; then
  echo "Usage: tui.sh REPO_ROOT WS_BASE HARNESS_DIR PROJECT_NAME [--kill|--fresh|--detach]" >&2
  echo "(normally invoked via: vibe tui [--kill|--fresh|--detach])" >&2
  exit 2
fi
repo_root="$1"
ws_base="$2"
harness_dir="$3"
project_name="$4"
action="launch"
case "${5:-}" in
  '') ;;
  --kill) action="kill" ;;
  --fresh) action="fresh" ;;
  --detach) action="detach" ;;
  *)
    echo "vibe tui: unknown flag: $5 (known: --kill, --fresh, --detach)" >&2
    exit 2
    ;;
esac

socket="vibe"
conf="$harness_dir/src/config/tmux-tui.conf"
tmux_bin="${VIBE_TUI_TMUX:-tmux}"

if ! command -v "$tmux_bin" >/dev/null 2>&1; then
  echo "vibe tui needs tmux on the host. Install the pinned 3.7b build:" >&2
  echo "  bash $harness_dir/src/scripts/host/install-tmux.sh" >&2
  echo "(a distro tmux >= 3.4 also works, but sixel image previews degrade below 3.7)" >&2
  exit 1
fi

# --kill / --fresh: stop the UI server — the reset story ("how do I get
# back to a clean tui?") as a first-class flag instead of folklore. Kills
# every project's tui session on the socket; container agent sessions are
# a different server and are untouched. Runs BEFORE the $TMUX guard: it's
# an admin op that must work from anywhere (from inside the tui itself it
# is prefix+Q with more typing — the client just dies with the server).
if [ "$action" = "kill" ] || [ "$action" = "fresh" ]; then
  kill_out="$("$tmux_bin" -L "$socket" kill-server 2>&1)" || true
  case "$kill_out" in
    *"no server"* | *"error connecting"*)
      echo "vibe tui: no UI server on socket '$socket' — nothing to kill."
      ;;
    *"protocol version mismatch"*)
      echo "The running vibe tui server was started by an incompatible tmux." >&2
      echo "Kill it with the binary that started it (usually the distro one):" >&2
      echo "  /usr/bin/tmux -L $socket kill-server" >&2
      exit 1
      ;;
    "")
      echo "vibe tui: UI server killed (container agent sessions untouched)."
      ;;
    *)
      printf '%s\n' "$kill_out" >&2
      ;;
  esac
  [ "$action" = "kill" ] && exit 0
fi

# --detach never attaches, so running it from inside a tmux (including a
# host pane of the tui itself — the prime use case: put a project on the
# sidebar without opening a new terminal) is fine.
if [ -n "${TMUX:-}" ] && [ "$action" != "detach" ]; then
  echo "vibe tui hosts its own tmux — run it from a plain terminal." >&2
  echo "(already on the vibe socket? switch with: tmux -L vibe switch-client -t <session>)" >&2
  exit 1
fi

# Version gate: 3.4 for styles-that-contain-formats and user mouse ranges
# (the conf depends on both); 3.7 is only a warning — below it, sixel
# images are cleared by adjacent-pane redraws (the pre-spike behavior).
raw_version="$("$tmux_bin" -V 2>/dev/null | sed 's/^tmux //')"
numeric="$(printf '%s\n' "$raw_version" | sed 's/^next-//; s/[^0-9.].*$//')"
major="${numeric%%.*}"
minor="${numeric#*.}"
minor="${minor%%.*}"
case "$major" in
  '' | *[!0-9]*) major=0 ;;
esac
case "$minor" in
  '' | *[!0-9]*) minor=0 ;;
esac
if [ "$major" -lt 3 ] || { [ "$major" -eq 3 ] && [ "$minor" -lt 4 ]; }; then
  echo "Host tmux is $raw_version; vibe tui needs >= 3.4 (styles with formats)." >&2
  echo "Install the pinned build: bash $harness_dir/src/scripts/host/install-tmux.sh" >&2
  exit 1
fi
if [ "$major" -eq 3 ] && [ "$minor" -lt 7 ]; then
  echo "Host tmux is $raw_version — fine, but image previews (vibe show) will not" >&2
  echo "survive pane redraws below 3.7. Pinned build: src/scripts/host/install-tmux.sh" >&2
fi

vtmux() {
  "$tmux_bin" -L "$socket" -f "$conf" "$@"
}

# A running server pins the binary it was started with — attaching never
# upgrades it. After a host tmux upgrade the old server keeps serving the
# socket (and e.g. still lacks sixel), so surface the skew instead of
# letting the upgrade silently not take effect.
server_probe="$("$tmux_bin" -L "$socket" display-message -p '#{version}' 2>&1)" || server_probe=""
case "$server_probe" in
  "" | *"no server"* | *"error connecting"*) ;; # no server yet: fresh start below
  *"protocol version mismatch"*)
    echo "The running vibe tui server was started by an incompatible tmux." >&2
    echo "Kill it with the binary that started it (usually the distro one):" >&2
    echo "  /usr/bin/tmux -L $socket kill-server   (container agent sessions are unaffected)" >&2
    exit 1
    ;;
  *)
    if [ "$server_probe" != "$raw_version" ]; then
      echo "vibe tui: running server is tmux $server_probe, your binary is $raw_version —" >&2
      echo "the old server keeps serving until it exits. To pick up the new one:" >&2
      echo "  tmux -L $socket kill-server   (container agent sessions are unaffected)" >&2
    fi
    ;;
esac

# Session per project. Friendly name first (basename); if a same-named
# session belongs to a DIFFERENT checkout, fall back to the unique
# per-checkout project name so two clones never share a session. Plain -t
# names throughout: on 3.7b, display-message -t "=name" silently resolves
# to empty formats (has-session accepts it, display-message doesn't), and
# exact names beat prefix matches anyway — with the path check catching
# anything a prefix match could confuse.
session="$ws_base"
if vtmux has-session -t "$session" 2>/dev/null; then
  existing_path="$(vtmux display-message -p -t "$session" '#{session_path}')"
  if [ "$existing_path" != "$repo_root" ]; then
    session="$project_name"
  fi
fi

# Self-heal a degraded session before reattaching: remain-on-exit keeps a
# dead agent pane as a corpse (deliberate — the layout survives), but
# reattaching INTO a corpse with no hint is a trap. A dead agent among
# live panes is respawned in place; a session with nothing left alive is
# rebuilt from scratch.
if vtmux has-session -t "$session" 2>/dev/null; then
  live_panes=0
  dead_agent=""
  while read -r pane dead role; do
    [ -n "$pane" ] || continue
    if [ "$dead" = "1" ]; then
      [ "$role" = "agent" ] && dead_agent="$pane"
    else
      live_panes=$((live_panes + 1))
    fi
  done <<EOF
$(vtmux list-panes -s -t "$session" -F '#{pane_id} #{pane_dead} #{@vibe_role}')
EOF
  if [ "$live_panes" -eq 0 ]; then
    vtmux kill-session -t "$session"
  elif [ -n "$dead_agent" ]; then
    vtmux respawn-pane -k -t "$dead_agent"
  fi
fi

if ! vtmux has-session -t "$session" 2>/dev/null; then
  # Default agent title = first word of the project's DEV_AGENT_CMD (the
  # config is container-side; this is a best-effort host read for the label).
  agent_title="claude"
  if [ -f "$repo_root/.vibe/config.env" ]; then
    configured="$(sed -n 's/^[[:space:]]*DEV_AGENT_CMD=//p' "$repo_root/.vibe/config.env" | tail -1 | tr -d '"'"'" | cut -d' ' -f1)"
    [ -n "$configured" ] && agent_title="$configured"
  fi

  # VIBE_NESTED rides the session environment into every pane: cexec
  # forwards it into docker exec, and agent-entry.sh turns the INNER
  # status bar off so only this server draws chrome.
  vtmux new-session -d -s "$session" -c "$repo_root" -e VIBE_NESTED=1 -n main "./vibe agent"

  agent_pane="$(vtmux display-message -p -t "$session:main" '#{pane_id}')"
  vtmux set-option -p -t "$agent_pane" @vibe_role "agent"
  vtmux set-option -p -t "$agent_pane" @vibe_title "$agent_title"
  # Agent exit/detach keeps the pane corpse instead of collapsing the
  # layout; prefix+r respawns it.
  vtmux set-option -p -t "$agent_pane" remain-on-exit on

  # Host shell as a BOTTOM dock (IDE-terminal style, 2026-07-22 request):
  # under the agent, not beside it. The sidebar splits full-height later,
  # so the dock spans the area right of the sidebar — the VS Code shape.
  # prefix+t (dock.sh) collapses it to a 1-row strip and back.
  host_pane="$(vtmux split-window -v -l '30%' -c "$repo_root" -t "$agent_pane" -P -F '#{pane_id}')"
  vtmux set-option -p -t "$host_pane" @vibe_role "host"
  vtmux set-option -p -t "$host_pane" @vibe_title "host"

  vtmux select-pane -t "$agent_pane"

  # Global sidebar (conf defaults @vibe_sidebar_on to 1): new-session
  # fires none of the ensure hooks, so stamp the fresh main window here;
  # every later window is covered by the conf's hooks.
  main_win="$(vtmux display-message -p -t "$session:main" '#{window_id}')"
  vtmux run-shell -b "bash '$harness_dir/src/scripts/host/sidebar.sh' ensure '$main_win' 2>/dev/null || true"
fi

# Conf ownership: FIRST-OWNER-AUTHORITATIVE (2026-07-21 decision). The
# server was styled by whichever project's launch created it (-f applies
# at server START only), so VIBE_TUI_CONF — the prefix+R reload target —
# must keep pointing at that owner's conf. The old unconditional
# set-environment here was last-writer-wins: prefix+R could reload
# project A's pinned conf over project B's sessions. Content-identical
# confs (projects on the same pin) adopt silently; real skew warns and
# leaves ownership alone; a vanished owner path self-heals to this
# checkout's conf. The server exists by now (created or found above).
owner_conf="$(vtmux show-environment -g VIBE_TUI_CONF 2>/dev/null | cut -d= -f2-)" || owner_conf=""
if [ -z "$owner_conf" ] || [ ! -f "$owner_conf" ]; then
  vtmux set-environment -g VIBE_TUI_CONF "$conf"
elif ! cmp -s "$owner_conf" "$conf"; then
  echo "vibe tui: the UI server's conf is owned by the project that started it:" >&2
  echo "  $owner_conf" >&2
  echo "This checkout pins a DIFFERENT tui conf — its changes are not active, and" >&2
  echo "prefix+R keeps reloading the owner's. To hand ownership to this project:" >&2
  echo "  tmux -L $socket kill-server   (container agents keep running), then relaunch." >&2
  sleep 3
fi

# --detach: the session is built (or healed) on the server — stop short
# of attaching. From an open tui it shows up in the sidebar/prefix+o
# within a poll tick; a later plain `vibe tui` here attaches to it.
if [ "$action" = "detach" ]; then
  echo "vibe tui: session '$session' ready on the vibe socket (not attached)."
  echo "Pick it up in an open tui via the sidebar or prefix+o; attach directly with: vibe tui"
  exit 0
fi

exec "$tmux_bin" -L "$socket" -f "$conf" attach-session -t "$session"

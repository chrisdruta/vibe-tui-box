#!/usr/bin/env bash
#
# vibe tui project sidebar — the cross-project glance as a vertical pane
# on the far left (graduated from the 2026-07-22 spike; it REPLACES the
# old status-line-2 strip). ONE section, the fleet: every project
# session on the vibe socket as a block under its gutter bar — the
# workspace name in bold (bright = the session this sidebar lives in)
# carrying the idle agents' state dots (the same @vibe_glyph /
# @vibe_dot_fg / @vibe_attn data state-render.sh maintains for the
# tabs), the checkout's git branch, engine facts, and NESTED agent rows
# for the agents that want eyes (docs/tui-layout.md "Agent surfaces":
# the sidebar is activity and signal, the tray is presence and reach,
# `vibe ps` is full truth). Click = switch to that project, jump to
# that window, or spawn a viewer for an agent that has none.
#
# GLOBAL across the whole UI: @vibe_sidebar_on (conf defaults it to 1) is
# the one switch, and the conf's ensure hooks (after-new-window /
# after-select-window / client-session-changed) grow a sidebar into every
# window as it is created or visited. A tmux pane can only live in one
# window, so "one global sidebar" is really one-per-window kept in
# lockstep — same look everywhere, one toggle.
#
# Modes:
#   toggle WINDOW_ID   flip @vibe_sidebar_on: off kills every sidebar pane
#                      on the server; on stamps this window (the hooks
#                      cover the rest as they're visited)
#   ensure WINDOW_ID   idempotent: sidebar present in WINDOW iff flag on
#   render             the draw loop inside the pane
#   click PANE ROW [CLIENT]  switch CLIENT to the project drawn on ROW —
#                      the conf's MouseDown1Pane binding routes sidebar
#                      clicks here; ROW resolves via @vibe_sidebar_map,
#                      which render publishes each frame, so there is no
#                      second copy of the layout arithmetic to drift
#
# Refresh is a 2s poll INSIDE each sidebar pane, but an idle tick is ONE
# display-message round trip: a full redraw happens only when
# @vibe_state_serial moved (state-render.sh bumps it with every dot
# write; tui.sh bumps it on session build/heal) or on every 5th tick —
# the 10s forced frame covers what has no serial: the branch line,
# renames, session create/destroy. The status line stays event-driven.
# Why not events outright: tmux wait-for has a lost-signal race and no
# timeout, bash 3.2 has no `wait -n` (so a fallback poll must exist
# anyway), and signaling sidebars FROM state-render.sh would put
# list-panes + kills on the hot path to save work on the cold one.
#
# DRAWING is the engine's: each frame pipes raw tmux porcelain into the
# hidden `vibe _frame` renderer (internal/tmuxui/frame.go), which owns
# every budget, truncation, and click-map row — this script never does
# layout arithmetic. ENGINE truth (container state vs approved
# candidate, pending requests, dev marker, cold registered projects)
# stays cache-only: a second serial, @vibe_engine_serial (bumped by
# state-mutating engine commands, internal/app/notify.go), or the
# @vibe_engine_refresh slow tick (default 30s) triggers a double-forked
# background fetch of `vibe _fleet` / `vibe _agents` / `vibe _sidebar`
# into a socket-derived cache; _frame only ever READS the cache — never
# Docker — so a slow daemon costs frames nothing and the last good
# truth keeps rendering. The engine binary itself is a prerequisite
# (the sidebar only exists under `vibe tui`): if it vanishes
# mid-session the pane parks on a notice and keeps polling.
#
# Host-side: bash-3.2-safe (stock macOS). Runs under the vibe server
# (run-shell provides TMUX for toggle/ensure; the pane's environment for
# render), so plain `tmux` is always the right binary/socket.
set -u

mode="${1:-render}"
tab="$(printf '\t')"
us="$(printf '\037')"

sidebar_panes() { # sidebar pane ids in window $1, oldest first
  tmux list-panes -t "$1" -F "#{pane_id}$tab#{@vibe_role}" 2>/dev/null |
    awk -F "$tab" '$2 == "sidebar" { print $1 }'
}

sidebar_w() { # the one width knob: @vibe_sidebar_w (conf), default 30
  w="$(tmux show-options -gqv @vibe_sidebar_w 2>/dev/null)"
  case "$w" in '' | *[!0-9]*) w=30 ;; esac
  printf '%s' "$w"
}

create_in() {
  win="$1"
  self="$(cd "$(dirname "$0")" && pwd)/$(basename "$0")"
  # Split BEFORE the agent pane — deliberately NOT full-height (-f):
  # the host dock owns the full window bottom (2026-07-31, dock.sh),
  # so the sidebar sits above it, and dropping -f is what makes the
  # async ensure hooks commute — whichever of dock/sidebar lands
  # second, the geometry converges on sidebar-above-full-width-dock.
  # The split targets the first non-chrome pane explicitly: -t WINDOW
  # would split the ACTIVE pane, which can be the dock. Input off so
  # stray clicks can't type into the render loop; focus returns to
  # where the user was.
  target=""
  while IFS="$tab" read -r id role; do
    case "$role" in
      host | sidebar) ;;
      *)
        target="$id"
        break
        ;;
    esac
  done <<EOF
$(tmux list-panes -t "$win" -F "#{pane_id}$tab#{@vibe_role}" 2>/dev/null)
EOF
  [ -n "$target" ] || target="$win"
  pane="$(tmux split-window -hb -l "$(sidebar_w)" -t "$target" -P -F '#{pane_id}' \
    "exec bash '$self' render")"
  # No pane-level @vibe_glyph shadow here: the border format role-gates
  # the dot instead. (The old empty-string shadow leaked further than the
  # border — window-format lookups resolve user options through the
  # ACTIVE pane, so a focused sidebar erased its window's dot everywhere.)
  tmux set-option -p -t "$pane" @vibe_role "sidebar" \; \
    set-option -p -t "$pane" @vibe_title "projects" \; \
    select-pane -d -t "$pane" \; \
    select-pane -l
}

ensure_in() {
  win="$1"
  found=""
  for p in $(sidebar_panes "$win"); do
    if [ -z "$found" ]; then
      found="$p"
    else
      # the ensure hooks run async (-b) and can race a double-create on a
      # fast window-hop; heal to exactly one
      tmux kill-pane -t "$p" 2>/dev/null
    fi
  done
  [ -n "$found" ] || create_in "$win"
}

case "$mode" in
toggle)
  win="${2:-}"
  [ -n "$win" ] || exit 0
  if [ "$(tmux show-options -gqv @vibe_sidebar_on)" = "1" ]; then
    tmux set-option -g @vibe_sidebar_on 0
    for p in $(tmux list-panes -a -F "#{pane_id}$tab#{@vibe_role}" 2>/dev/null |
      awk -F "$tab" '$2 == "sidebar" { print $1 }'); do
      tmux kill-pane -t "$p" 2>/dev/null
    done
    # The render loop is the tray ghosts' only publisher; with no
    # sidebar left, drop them rather than leave the bar advertising a
    # roster nothing refreshes.
    for s in $(tmux list-sessions -F '#{session_id}' 2>/dev/null); do
      tmux set-option -t "$s" -u @vibe_ghosts 2>/dev/null
      tmux set-option -t "$s" -u @vibe_ghost_map 2>/dev/null
    done
  else
    tmux set-option -g @vibe_sidebar_on 1
    ensure_in "$win"
  fi
  exit 0
  ;;
ensure)
  win="${2:-}"
  [ -n "$win" ] || exit 0
  [ "$(tmux show-options -gqv @vibe_sidebar_on)" = "1" ] || exit 0
  ensure_in "$win"
  exit 0
  ;;
click)
  pane="${2:-}"
  y="${3:-}"
  client="${4:-}"
  { [ -n "$pane" ] && [ -n "$y" ]; } || exit 0
  sid=""
  for entry in $(tmux show-options -pqv -t "$pane" @vibe_sidebar_map 2>/dev/null); do
    case "$entry" in
      "$y":*) sid="${entry#*:}" && break ;;
    esac
  done
  [ -n "$sid" ] || exit 0 # gutter/blank row — not a target
  case "$sid" in
    *:@*)
      # nested agent row with a viewer: "SESSION:WINDOW" — make that
      # window current in its session, then bring this client over
      win="${sid##*:}"
      sess="${sid%%:*}"
      tmux select-window -t "$win" 2>/dev/null
      sid="$sess"
      ;;
    *:agent-*)
      # nested agent row with NO viewer: "SESSION:agent-NAME" — the same
      # attach-only spawn a tray ghost cell dispatches (one definition,
      # agent-open.sh), which brings the client over itself.
      here="$(cd "$(dirname "$0")" && pwd)"
      exec bash "$here/agent-open.sh" "$client" "${sid%%:*}" "${sid#*:agent-}"
      ;;
    *:svc-*)
      # workspace-service row: reach the services viewer at the clicked
      # window (agent-open.sh -s owns the jump-or-spawn).
      here="$(cd "$(dirname "$0")" && pwd)"
      exec bash "$here/agent-open.sh" "$client" "${sid%%:*}" -s "${sid#*:svc-}"
      ;;
    *:svcx-*)
      # DEAD workspace-service row keeps its reach — the `services`
      # session lives on and the kept corpse window holds the crash
      # log, exactly what the click should show. The x marks deadness
      # for the right-click menu's dismiss label only.
      here="$(cd "$(dirname "$0")" && pwd)"
      exec bash "$here/agent-open.sh" "$client" "${sid%%:*}" -s "${sid#*:svcx-}"
      ;;
    *:svcfold)
      # services-HEADER click: fold/unfold that block's services tree
      # (frame.go rosterBlock renders the collapsed header from the S
      # record's trailing flag). State is a session option so each
      # project folds alone and the preference dies with the session;
      # the serial bump repaints every sidebar on its next 2s tick —
      # the same channel every dot write rides.
      sess="${sid%%:*}"
      if [ "$(tmux show-options -qv -t "$sess" @vibe_svc_fold 2>/dev/null)" = "1" ]; then
        tmux set-option -t "$sess" -u @vibe_svc_fold 2>/dev/null
      else
        tmux set-option -t "$sess" @vibe_svc_fold 1 2>/dev/null
      fi
      tmux set-option -g @vibe_state_serial "$$$RANDOM" 2>/dev/null
      exit 0
      ;;
    *:dead-*)
      # dead viewer-less row: the ✗ is signal, not reach — LEFT-click
      # degrades to the project switch (an attach would refuse); the
      # address in the target is the RIGHT-click menu's business
      # (agent-menu.sh row mode → dismiss).
      sid="${sid%%:*}"
      ;;
  esac
  if [ -n "$client" ]; then
    tmux switch-client -c "$client" -t "$sid" 2>/dev/null
  else
    tmux switch-client -t "$sid" 2>/dev/null
  fi
  exit 0
  ;;
fit)
  # Window resizes stretch panes PROPORTIONALLY, so a client visiting a
  # window born at another size balloons/squeezes the sidebar (detached
  # --detach sessions are born 80 cols wide; live report, 2026-07-22).
  # The conf's window-resized hook calls this to snap the sidebar back to
  # its fixed chrome width. Fires only on window-size changes — manual
  # border drags don't resize the window, so they are never fought.
  win="${2:-}"
  [ -n "$win" ] || exit 0
  want="$(sidebar_w)"
  for p in $(sidebar_panes "$win"); do
    cur="$(tmux display-message -p -t "$p" '#{pane_width}' 2>/dev/null)"
    [ "$cur" = "$want" ] || tmux resize-pane -t "$p" -x "$want" 2>/dev/null
  done
  exit 0
  ;;
render) ;;
*) exit 0 ;;
esac

# ── render ───────────────────────────────────────────────────────────────

# Theme (palette + spinner frames) for the working-dot overlay below —
# same artifact, same values as the conf's @thm twin.
case "$0" in */*) here="${0%/*}" ;; *) here="." ;; esac
# shellcheck source=theme.sh disable=SC1091
. "$here/theme.sh"

# ── engine cache ─────────────────────────────────────────────────────────
# Lifetime- and user-matched to the tmux server: beside its socket
# (/tmp/tmux-UID is already 0700). Empty cache_dir disables the whole
# engine layer — every consumer guards on it.
cache_dir=""
spin_lock=""
sock="$(tmux display-message -p '#{socket_path}' 2>/dev/null)"
if [ -n "$sock" ]; then
  cache_dir="${sock%/*}/vibe-tui-cache"
  if ! mkdir -p "$cache_dir" 2>/dev/null || ! chmod 700 "$cache_dir" 2>/dev/null; then
    cache_dir=""
  fi
  spin_lock="${sock%/*}/vibe-tui-spin.lock"
fi

# fetch_engine — refresh the fleet + own-project detail caches in the
# background. Double-forked (the inner job reparents to init; this loop
# never waits and never accumulates zombies), tmp+mv so a failed run
# keeps the last good cache, and stamp-guarded so concurrent sidebars
# (one per window) don't stampede: the stamp holds a start epoch — no
# pid, bash 3.2 has no BASHPID — and ages out after 30s so a fetch that
# died mid-write can't wedge fetching forever. Never called from
# frame(); only the loop and startup trigger it.
fetch_engine() {
  [ -n "$cache_dir" ] || return 0
  exe="$(tmux show-options -gqv @vibe_exe 2>/dev/null)"
  { [ -n "$exe" ] && [ -x "$exe" ]; } || return 0
  if [ -f "$cache_dir/fetch.stamp" ]; then
    fepoch=""
    IFS= read -r fepoch <"$cache_dir/fetch.stamp" 2>/dev/null || :
    case "$fepoch" in
      '' | *[!0-9]*) ;;
      *) [ $(($(date +%s) - fepoch)) -lt 30 ] && return 0 ;;
    esac
  fi
  # The project id resolves through the pane's session (@vibe_project is
  # a session option; format lookup walks the chain).
  proj="$(tmux display-message -p -t "${TMUX_PANE:-}" '#{@vibe_project}' 2>/dev/null)"
  fw="$(sidebar_w)"
  (
    (
      date +%s >"$cache_dir/fetch.stamp" 2>/dev/null
      if "$exe" _fleet --width "$fw" >"$cache_dir/fleet.tmp" 2>/dev/null; then
        mv -f "$cache_dir/fleet.tmp" "$cache_dir/fleet" 2>/dev/null
      else
        rm -f "$cache_dir/fleet.tmp" 2>/dev/null
      fi
      # Container-side agent truth (`vibe ps` rows for every running
      # project): the sidebar's viewer-less rows and the TRAY's ghost
      # cells both join against this. Fleet-wide, one exec per running
      # project — the reason it rides the slow fetch and never a frame.
      if "$exe" _agents --width "$fw" >"$cache_dir/agents.tmp" 2>/dev/null; then
        mv -f "$cache_dir/agents.tmp" "$cache_dir/agents" 2>/dev/null
      else
        rm -f "$cache_dir/agents.tmp" 2>/dev/null
      fi
      if [ -n "$proj" ]; then
        # Width leaves room for the dim gutter frame() adds; a pane
        # narrower than the knob is transient (the fit hook snaps back).
        if "$exe" _sidebar --project "$proj" --width $((fw - 4)) >"$cache_dir/detail.tmp" 2>/dev/null; then
          mv -f "$cache_dir/detail.tmp" "$cache_dir/detail.$proj" 2>/dev/null
        else
          rm -f "$cache_dir/detail.tmp" 2>/dev/null
        fi
      fi
      rm -f "$cache_dir/fetch.stamp" 2>/dev/null
    ) &
  )
}

# watch_engine — ensure the push channel runs (`vibe _watch`, the
# watch-channel daemon): one per project per server, self-guarding via
# a flock beside the cache, so this spawn is idempotent — redundant
# attempts (every sidebar retries on the slow tick) exit in
# milliseconds. The daemon streams container-side change events and
# rewrites the agents cache within ~2s of an inner change; the slow
# tick below stays as the fallback cadence whenever the daemon cannot
# run (no docker, container down, older engine).
watch_engine() {
  [ -n "$cache_dir" ] || return 0
  exe="$(tmux show-options -gqv @vibe_exe 2>/dev/null)"
  { [ -n "$exe" ] && [ -x "$exe" ]; } || return 0
  proj="$(tmux display-message -p -t "${TMUX_PANE:-}" '#{@vibe_project}' 2>/dev/null)"
  [ -n "$proj" ] || return 0
  # Double-fork like fetch_engine: the daemon reparents to init and
  # this loop never waits on it.
  (
    ("$exe" _watch --cache "$cache_dir" --project "$proj" --width "$(sidebar_w)" >/dev/null 2>&1) &
  )
}

# frame — one redraw. The layout arithmetic (budgets, truncation, the
# gutter bars, the nested agent rows, the click map) lives in the
# engine's `vibe _frame` renderer (internal/tmuxui/frame.go), where it
# is table-tested; this side only gathers the raw tmux porcelain and
# paints the returned bytes. The renderer (called with --spin — the
# same-artifact contract; an older engine without the flag never meets
# this script) answers four protocol lines before the body: the click
# map (published as @vibe_sidebar_map for the click mode above — no
# second copy of the layout arithmetic to drift), the tray's ghost
# cells (published as the session's @vibe_ghosts), the ghost map, and
# the working-dot spin cells the sub-tick overlay repaints, then the
# newline-free ANSI body. Engine facts still come cache-only: _frame
# reads the fleet/agents/detail caches, never Docker, so frames stay
# cheap.
nl='
'
frame() {
  exe="$(tmux show-options -gqv @vibe_exe 2>/dev/null)"
  if [ -z "$exe" ] || [ ! -x "$exe" ]; then
    # No engine binary means `vibe tui` itself is gone mid-session;
    # keep polling — an update may land a new binary at the same path.
    printf '\033[H\033[2J engine unavailable'
    return 0
  fi
  geo="$(tmux display-message -p -t "${TMUX_PANE:-}" "#{pane_width}$us#{pane_height}$us#{session_id}" 2>/dev/null)" || return 0
  out="$(
    {
      printf 'G%s%s\n' "$us" "$geo"
      tmux list-sessions -F "S$us#{session_id}$us#{?#{@vibe_name},#{@vibe_name},#{session_name}}$us#{session_path}$us#{@vibe_project}$us#{@vibe_svc_fold}" 2>/dev/null
      tmux list-windows -a -F "W$us#{session_id}$us#{@vibe_glyph}$us#{@vibe_dot_fg}$us#{@vibe_attn}$us#{window_id}$us#{window_name}$us#{window_active}$us#{@vibe_model}$us#{@vibe_state}$us#{@vibe_session}$us#{@vibe_state_epoch}" 2>/dev/null
    } | "$exe" _frame --cache "$cache_dir" --spin 2>/dev/null
  )" || return 0
  case "$out" in *"$nl"*"$nl"*"$nl"*"$nl"*) ;; *) return 0 ;; esac # malformed: keep last frame
  map="${out%%"$nl"*}"
  rest="${out#*"$nl"}"
  ghosts="${rest%%"$nl"*}"
  rest="${rest#*"$nl"}"
  gmap="${rest%%"$nl"*}"
  rest="${rest#*"$nl"}"
  spin_cells="${rest%%"$nl"*}"
  printf '%s' "${rest#*"$nl"}"
  # The healer door for the tray animator (docs/tui-layout.md "The
  # working spinner"): a viewer-less working agent has no title events
  # on this server, so state-render.sh's instant door never fires for
  # it. Spawn attempts gate on the lock's absence — one -e test per
  # frame; spin.sh re-checks under noclobber, so the race is its
  # problem and costs nothing.
  if [ -n "$spin_cells" ] && [ -n "$spin_lock" ] && [ ! -e "$spin_lock" ]; then
    ( (bash "$here/spin.sh" >/dev/null 2>&1) & )
  fi
  if [ "$map" != "$last_map" ]; then
    tmux set-option -p -t "${TMUX_PANE:-}" @vibe_sidebar_map "$map" 2>/dev/null
    last_map="$map"
  fi
  # The TRAY's ghost cells ride the same render: the frame renderer
  # already joined `vibe ps` truth against this server's windows, so
  # publishing its answer as a SESSION option keeps the winlist's one
  # #{E:@vibe_ghosts} splice honest without a second engine call (the
  # conf's splice budget stays the single #(vibe _state)). The ghost
  # map rides beside it — the session names the cells' indexed ghost-N
  # ranges resolve through (a range name truncates at 15 bytes, an
  # option value never; tui-layout.md "Launch surfaces"). Every
  # window's sidebar computes the same strings for the same session,
  # so the writes are idempotent.
  if [ "$ghosts" != "$last_ghosts" ] || [ "$gmap" != "$last_gmap" ]; then
    tmux set-option -t "${geo##*"$us"}" @vibe_ghosts "$ghosts" \; \
      set-option -t "${geo##*"$us"}" @vibe_ghost_map "$gmap" 2>/dev/null
    last_ghosts="$ghosts"
    last_gmap="$gmap"
  fi
}


# This copy's own path, for the slow tick's self-upgrade drift check.
self="$(cd "$(dirname "$0")" && pwd)/$(basename "$0")"
# The working-dot overlay (docs/tui-layout.md "The working spinner"):
# between frames the loop repaints ONLY the spin cells the renderer
# reported — pure printf, no tmux round trip and no engine call per
# animation frame. The frame's own glyph stays the static ●, so a
# skipped overlay (short pane, stale theme) degrades to today's look.
# shellcheck disable=SC2206  # word-split ON PURPOSE: frames are space-separated
spin_frames=(${VIBE_SPIN_FRAMES-})
spin_n=${#spin_frames[@]}
spin_fg="$(vibe_fg "${VIBE_THM_GREEN:-#9ece6a}")"
spin_i=0
spin_cells=""
last_map=""
last_ghosts=""
last_gmap=""
last_serial=""
last_eserial=""
tick=0
etick=0
frames_due=0
# Slow-tick cadence for unprompted engine refetches (covers out-of-band
# container deaths and engine runs from other terminals): the knob is
# seconds, the loop runs on a 2s frame.
er="$(tmux show-options -gqv @vibe_engine_refresh 2>/dev/null)"
case "$er" in '' | *[!0-9]* | 0) er=30 ;; esac
eticks=$((er / 2))
[ "$eticks" -lt 1 ] && eticks=1
printf '\033[?25l'
trap 'printf "\033[?25h"' EXIT
fetch_engine # warm the cache so engine rows appear within a tick or two
watch_engine # start the push channel (idempotent; slow tick retries)
frame
while :; do
  # The 2s tick, sub-divided while anything works: four 500ms steps,
  # each advancing the spinner and repainting exactly the reported
  # cells. Wall-clock per tick stays 2s either way — the poll cadence
  # below never changes.
  if [ -n "$spin_cells" ] && [ "$spin_n" -gt 0 ]; then
    st=0
    while [ "$st" -lt 4 ]; do
      sleep 0.5
      st=$((st + 1))
      spin_i=$(((spin_i + 1) % spin_n))
      buf=""
      for cell in $spin_cells; do
        buf="$buf"$'\033'"[${cell%%:*};${cell#*:}H$spin_fg${spin_frames[spin_i]}"$'\033'"[0m"
      done
      printf '%s\033[H' "$buf"
    done
  else
    sleep 2
  fi
  # ONE round trip per idle tick: die-check and change detection
  # together. '/' separates because either serial may be empty and
  # whitespace splitting would collapse the hole.
  poll="$(tmux display-message -p -t "${TMUX_PANE:-}" '#{window_panes}/#{@vibe_state_serial}/#{@vibe_engine_serial}' 2>/dev/null)" || exit 0
  IFS=/ read -r n serial eserial <<EOF5
$poll
EOF5
  case "$n" in '' | *[!0-9]*) exit 0 ;; esac
  # Last real pane gone (shell exited, window would linger on just us):
  # let the window die with it. The main window's agent corpse still
  # counts as a pane (remain-on-exit), so it keeps its sidebar.
  [ "$n" -le 1 ] && exit 0
  # Engine serial moved → background refetch now, frame this tick (last
  # good) AND the next (the refreshed cache — frames_due covers both).
  # The slow tick refetches regardless; its result rides the next forced
  # frame. The 2s frame itself never invokes the engine.
  etick=$(((etick + 1) % eticks))
  if [ "$eserial" != "$last_eserial" ]; then
    last_eserial="$eserial"
    frames_due=2
    fetch_engine
  elif [ "$etick" -eq 0 ]; then
    fetch_engine
    watch_engine # respawn if the daemon died; a live one holds the lock
    # The self-upgrade (2026-07-28, the attach-heal's loop half): a dev
    # cycle repoints @vibe_payload_dir at a NEW artifact while this
    # loop keeps executing the old artifact's script forever (artifacts
    # are immutable, so it keeps working — and keeps missing every
    # fix). When the dir drifts from the copy we were started from,
    # exec the current script in place: same pane, same pid, new bytes.
    pd="$(tmux show-options -gqv @vibe_payload_dir 2>/dev/null)"
    if [ -n "$pd" ] && [ "$pd/scripts/sidebar.sh" != "$self" ] && [ -r "$pd/scripts/sidebar.sh" ]; then
      exec bash "$pd/scripts/sidebar.sh" render
    fi
  fi
  tick=$(((tick + 1) % 5))
  if [ "$serial" = "$last_serial" ] && [ "$frames_due" -eq 0 ] && [ "$tick" -ne 0 ]; then
    continue # nothing moved; the 10s forced frame covers serial-less bits
  fi
  [ "$frames_due" -gt 0 ] && frames_due=$((frames_due - 1))
  last_serial="$serial"
  frame
done

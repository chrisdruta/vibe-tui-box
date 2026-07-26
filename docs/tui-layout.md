# TUI layout spec

**Authority: design (layout contract).** The BACKLOG's layout pass
demanded a written spec before any wiring — layout arithmetic is where
the sidebar bugs have lived (click-map skew, dot wrapping, resize
ballooning). Wiring follows this file; disagreements edit it first.
Updated 2026-07-25 to the shipped state (the `_frame` move and the
bottom-bar tray included).

Floor: tmux ≥ 3.4 (styles containing formats, user mouse ranges). The
theme block, `@vibe_winlist` (derived from the stock 3.7 window-list
construct), and the bar border are **generated** into marker-delimited
regions of `tmux-tui.conf` from `internal/tmuxui/theme.go` /
`internal/payload/gen` — edit the Go side; hand edits to those regions
are CI drift failures.

## Decisions

### Bar: bottom — the system tray (supersedes "top", 2026-07-25)

The bar sits at the bottom and is the tray: branding start button,
window cells, engine state, clock. The original top call ("the dock
owns the bottom edge") is superseded by operator decision — with the
terminal app's own tabs at the top of the screen, a top bar stacked two
tab strips; at the bottom the collapsed dock strip and the bar read as
one chrome band, and the taskbar muscle memory is worth more than the
strip-stacking concern. The bar is two status lines: `status-format[0]`
is the generated border rule, `status-format[1]` is the tray. The
keybind cheatsheet swaps *into* the tray while the prefix is held
(`#{client_prefix}` over `#{E:@vibe_cheat}` / `#{E:@vibe_winlist}` —
option indirection keeps the stock `W:` construct out of the
conditional's comma parsing), so hints never cost another row.
Top-preferrers set `status-position top` in the user conf.

### Segment inventory (left → right, all in the tray line)

| Segment | Content |
| --- | --- |
| branding | `🥡 vibe-tui-box` start button — click opens the palette |
| `▤` cell | clickable — toggles the host dock (prefix+t as a button); clicking the collapsed dock strip itself also expands it |
| tabs | per-window `dot name`, absolute-centred; the name is the CLI actually running (state-render renames the window from the title channel's display field), attention flash |
| `+` cell | clickable — opens the palette (the "new" chooser) |
| cheatsheet | key hints, shown only while prefix held (replaces tabs) |
| prefix/copy | `⌨` / `copy` indicators (stamped `status-right`) |
| engine state | state glyph, `▲n` only when pending > 0; click opens the request list (`#(vibe _state)` splice in user range `req`) |
| clock | `%H:%M` (stamped `status-right`) |

The bar never carries project identity: the sidebar and the OS window
title (`@vibe_name`) own it, and the ID-derived session name appears in
no chrome. The palette lives in `scripts/palette.sh` — one definition
serving `prefix+Space` and both clickable cells. The title channel the
tabs and roster consume is
`vibe1|project|session|instance|state|display|model`
(`state-render.sh`).

No new `#(...)` engine splices in the conf — the one `_state` splice at
`status-interval 5` is the budget. Everything richer belongs to the
sidebar's cached engine layer.

### Knobs

All knobs are tmux user options, defaulted with `set -goq` so a
`prefix+R` reload never clobbers a live value. Documented set:

| Option | Default | Consumer | Meaning |
| --- | --- | --- | --- |
| `@vibe_sidebar_on` | `1` | sidebar.sh | global sidebar toggle |
| `@vibe_sidebar_w` | `30` | sidebar.sh (+fit hook) | sidebar chrome width, cols |
| `@vibe_dock_size` | `30%` | dock.sh | expanded dock height (rows or `%`) |
| `@vibe_engine_refresh` | `30` | sidebar.sh | unprompted engine refetch, seconds |

Deliberately **not** knobs: bar position, segment order, theme accents.
`status-position` cannot take a format, so an option can't drive it
from the payload conf — and a per-property option surface would grow
without bound. Instead:

### The user conf hook

`vibe tui` (materializeTuiConf) appends, after the payload conf body:

    source-file -q ~/.config/vibe/tui.conf

That file is the sanctioned customization point — the full tmux
language (bar position, accent overrides, extra binds), applied last so
it wins. The store-owned conf is never forked, re-materialization never
eats user edits, and `-q` keeps a missing file silent. Anything a knob
would micro-manage lives here instead.

### Default arrangement

Agent pane dominant; sidebar far left at `@vibe_sidebar_w` fixed cols,
one per window kept in lockstep; dock parked collapsed (1 row) on
session create, expanding to `@vibe_dock_size`; pane borders on top
with role-gated dot + title.

### Sidebar frame contract (`vibe _frame` owns this)

All sidebar layout arithmetic lives in the engine renderer
(`internal/tmuxui/frame.go`); `sidebar.sh` pipes tmux porcelain in and
never does layout math. The contract the renderer implements:

- Row 0 stays blank. The **fleet section** flows from row 1: per
  session a name row with agent state dots, a `⎇ branch` row when
  known, engine facts (stale/stopped glyph, `▲n`, `dev`) or the own
  project's detail block, and a blank slop row — every row claims the
  session as its click target. Cold registered projects (fleet entries
  with no live session) render dim and unclickable — click-dispatching
  `up` is a recorded open product call, not half-shipped here.
- The **aggregate agent roster** starts at the pane midpoint (a long
  fleet pushes it down instead of overlapping), under a ruled `agents`
  header: two-line entries (dot + window name; dim model · project
  detail) plus a gap row, all three sharing one `SESSION:WINDOW` jump
  target. When only part fits, the last slot becomes an overflow count.
  The roster is render-only — no dismiss/kill affordance (BACKLOG
  decision record, 2026-07-25).
- Budgets derive from pane width: text budget is `width−3` (floor 8);
  a session's name budget shrinks 2 cols per state dot (floor 8) so a
  long name can never wrap the dots and skew the click map; roster
  names get `budget−4` (floor 8).

### Dead panes: two corpse fates

A dead agent pane means the *frontend* (docker-exec client) died — the
inner tmux session may be alive. The `pane-died` hook self-cleans a
viewer whose recorded `@vibe_state` is `exited*` (the run's own end was
recorded — the death is explained), and marks everything else `◌`
frontend-dead with respawn (`prefix+r`) and close (`prefix+x`) hints.
Agent exit codes never cross the inner-tmux client boundary, so
recorded state — not the exit status — is the only truth to key on
(BACKLOG decision record, 2026-07-25).

### Resize policy: chrome snaps, content stretches

tmux stretches panes proportionally on window resize; chrome must not
inherit that. The `window-resized` hook snaps the sidebar to
`@vibe_sidebar_w` and a collapsed dock to 1 row. Expanded docks and
content panes stretch proportionally and are never fought after a
manual border drag (border drags don't resize the window — the trigger
distinction is the mechanism, keep it).

### Non-goals

Per-window status bars, a segment-reordering DSL, multiple sidebar
positions, engine calls in format strings. Recorded so the knob
surface stays honest.

## Verification

The spec's regressions are owned by tests: `_state` display form in
`internal/tmuxui/views_test.go`, frame layout/click-map/truncation in
`internal/tmuxui/frame_test.go`, and the user-conf epilogue in
`internal/app/tui_test.go`. The manual check that has caught what
tests miss: resize the sidebar and click every row type — the
click-skew regression class.

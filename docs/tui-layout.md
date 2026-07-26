# TUI layout spec

**Authority: design (layout contract).** The BACKLOG's layout pass
demanded a written spec before any wiring — layout arithmetic is where
the sidebar bugs have lived (click-map skew, dot wrapping, resize
ballooning). Wiring follows this file; disagreements edit it first.
Updated 2026-07-25 to the shipped state (the `_frame` move and the
bottom-bar tray included). Updated 2026-07-26 with the polish-pass
calls (roster flow, sidebar footer, detail display form, status-right
separators, editor popups), wired the same day.

Floor: tmux ≥ 3.4 (styles containing formats, user mouse ranges). The
theme block, `@vibe_winlist` (derived from the stock 3.7 window-list
construct), and the bar border are **generated** into marker-delimited
regions of `tmux-tui.conf` from `internal/tmuxui/theme.go` /
`internal/payload/gen` — edit the Go side; hand edits to those regions
are CI drift failures.

## The frame

The target frame, agreed 2026-07-26 (dogfood project shown; popups are
the transient layer, lazygit-pattern):

```
┌─ projects ────────────┬─ ● claude ────────────────────────────────┐
│ ▍vibe-tui-box ●       │                                           │
│    ⎇ main             │                                           │
│    ● dev · 9766b8d8   │                                           │
│    ▲ 2 pending        │              agent pane                   │
│                       │                                           │
│  · cold-project       │                                           │
│                       │                                           │
│ ─ agents ─────────────│                                           │
│ ▍● claude             │                                           │
│    Fable 5 · vibe-tu… │                                           │
│                       ├─ host ────────────────────────────────────┤
│ C-Space·Space palette │ chris@host:~/dev/vibe-tui-box$            │
├───────────────────────┴───────────────────────────────────────────┤
│ ─────────────────────────────── rule ─────────────────────────────│
│ 🥡 vibe-tui-box    ▤    ● claude    +               ⌨ · ● · 18:51 │
└───────────────────────────────────────────────────────────────────┘

prefix+g → ╭─ lazygit ────────────╮   prefix+f → ╭─ nvim . ─────────╮
prefix+G → ╭─ difftool diff walk ─╮   (85% popups, host binary,
                                       `|| bash -l` fallback)
```

The outer box is the terminal window's edge, drawn only to frame the
figure — the UI adds no outer border; pane borders and the two status
lines are the only chrome. Left to right, top to bottom: sidebar
(fleet section, then the agent roster flowing directly after it,
footer hint on the last row), agent pane with role-gated border title,
host dock collapsed to its 1-row strip, then the two status lines
(rule + tray).

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

The right segments read `⌨ · ● · 18:51` — a dim ` · ` separator
between prefix/copy, the engine-state cell, and the clock (2026-07-26;
before, the bare state dot sat flush against the clock and nothing
signaled it was a distinct, clickable cell). The cheatsheet's
inventory includes the stock affordances people forget (`z` zoom, `[`
scroll/copy, `x` close) alongside the vibe binds — it is the only
discoverability surface once the prefix is down.

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

### Editor surfaces: nvim popups, yazi dropped (2026-07-26)

The stance is **editor-as-surface**: vibe ships tmux glue only, never
an editor config — the flexibility comes from the user's own nvim
setup, not from a vibe-owned distro (the v1 bash→yazi→Lua-plugin
layering is on record as not wanted back, and a vibe-owned nvim
config would be the same maintenance surface under a new name).

- `prefix+f` — files/editor popup: `nvim . || bash -l` at
  `#{session_path}`, 85%, the lazygit pattern verbatim (host binary,
  bind-mounted workspace, store-owned glue only). Browsing, viewing,
  and editing ride whatever the user's config provides (netrw is the
  zero-config floor).
- `prefix+G` — review walk: `git difftool --tool=nvimdiff -y`, gated
  on a non-empty diff (`git diff --quiet` else a "no changes"
  display-message). Plugin-free real vimdiff over every changed file;
  zero config required.
- Both are palette items too (`files (nvim)`, `review diff`) — one
  definition per door, as ever.
- No `@vibe_reviewer` knob: users who want `nvim +DiffviewOpen` or a
  different walker rebind the key in `~/.config/vibe/tui.conf`, the
  sanctioned customization point. The knob list stays honest.
- A/R verdict capture stays engine-owned and viewer-replaceable
  (BACKLOG "Review/image stack revival") — nothing here commits the
  verdict flow to nvim; these popups are the default *viewing* path
  regardless of how revdiff's trial lands.

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
- The **aggregate agent roster** flows directly after the fleet
  section (supersedes "starts at the pane midpoint", 2026-07-26: with
  a small fleet the midpoint rule left two separate dead blocks; the
  flowing roster keeps one contiguous empty region below it — roster
  rows shift only when fleet rows change, which the per-frame click
  map republication already absorbs), under a ruled `agents` header:
  two-line entries (dot + window name; dim model · project detail)
  plus a gap row, all three sharing one `SESSION:WINDOW` jump target.
  When only part fits, the last slot becomes an overflow count. The
  roster is render-only — no dismiss/kill affordance (BACKLOG decision
  record, 2026-07-25).
- The **footer hint row** owns the last row: dim
  `C-Space · Space palette`, truncated to the text budget, render-only
  (no click target — the palette's mouse doors are the tray cells).
  It exists for the cold start: the cheatsheet only appears once the
  prefix is already known.
- The **detail block display form** (`vibe _sidebar`, views.go): one
  line per container — StateToken glyph + role (the renderer draws the
  whole block dim, so the glyphs carry shape, not color) — with the
  engine version riding the first line (`dev-` hashes stripped of the
  prefix and cut to 8: `● dev · 9766b8d8`; release versions as-is).
  Pending renders `▲ n pending`. This supersedes the
  mode+version header line and the `%-12s`-padded state words
  (2026-07-26: `dev dev-9766b8d8ddce` over `dev          running` was
  three "dev"s meaning mode, version prefix, and role — and words
  where every other surface speaks the `● ○ ◐` glyph vocabulary).
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
click-skew regression class. For the editor popups: `prefix+G` on a
clean tree must show the "no changes" message, never a
flash-and-close popup.

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

prefix+f → ╭─ files · nvim/oil ───╮   (90% popups, container-side via
prefix+g → ╭─ git · lazygit ──────╮    vibe exec — a cold host needs
                                       nothing installed)
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

### Editor surfaces: the bundled review stack (2026-07-26, second call)

The first call the same day ("editor-as-surface": host nvim, zero
shipped config, plugin-free floors) was falsified by its first
dogfood: a stock WSL host has neither nvim nor lazygit, so the
zero-config floor did not exist and `prefix+f/g/G` degraded to bare
shell popups. The stance flips: **the container carries an
opinionated, pinned review stack** — the engine already owns that
toolchain (the tmux-pin precedent), and a cold host is the norm, not
the edge. What survives from the first call: the viewer is glue-level
replaceable, verdict capture stays engine-owned, and no `@vibe` knob.

- **Placement.** Binaries ride the tools image on the `wantsAgent`
  gate exactly like tmux (core product UX, not a manifest choice):
  nvim + lazygit as pinned release artifacts (version + per-arch
  sha256 in `builder/install.go`); plugins git-cloned at **pinned
  SHAs** into a root-owned native packpath (`/opt/vibe/nvim`) — no
  plugin manager, no runtime network, and no plugin bytes on volumes
  (that decision record is upheld, not bent); treesitter parsers for
  an engine-owned language list compiled at image build.
- **The stack** (sharp = few pins, review-focused, deliberately NOT
  an IDE — no LSP, no completion, no nerd-font glyphs): mini.nvim
  (pick/statusline/clue/ascii icons from one pin), oil.nvim as the
  files surface (the directory fills the window; mini.files' floating
  columns read as broken inside a full-screen popup — dogfood,
  2026-07-26), gitsigns.nvim, tokyonight.nvim under generated palette
  overrides, nvim-treesitter. **Diff review is lazygit's job**: the
  diffview.nvim surface (`prefix+G`) shipped and was retired the same
  day — lazygit's built-in diff browsing covered it better, and a
  second diff surface was one surface too many.
- **Config** lives in `payload/container/nvim/` (read-only payload,
  XDG state dirs pointed at scratch), so keymap/option iteration
  rides a payload sync — only binaries, plugins, and parsers need an
  image rebuild. `theme.lua` becomes the THIRD generated rendering of
  `internal/tmuxui/theme.go`, beside theme.sh and the conf's @thm
  block: the TUI and the editor read as one product. lazygit gets a
  small generated yml the same way (`nerdFontsVersion: ""`).
- **Keymap contract:** the popup is a transient viewer, so `q` quits
  it from ANYWHERE (`:confirm qa` — the lazygit convention; macro
  recording forfeits its key on a reading surface, 2026-07-26
  dogfood). Leader Space with clue hints on press; `-` parent-dir
  browse (oil — editing the listing edits the filesystem, the way
  BACK from a file, not out), `<leader>f` files, `<leader>/` grep,
  `<leader>b` buffers, `<leader>g…` hunk ops (preview/stage/reset/
  blame), `]h`/`[h` hunk nav, `<leader>y` OSC 52 copy to the host
  clipboard. The popup BORDER TITLE carries the exit hints
  (review.sh's `-T`) — always-visible helper text with zero editor
  machinery.
- **Binds (wired 2026-07-26):** `prefix+f/g` and the two palette
  items route through one host router, `scripts/review.sh CLIENT
  EXE SESSION_PATH files|git` — the popup runs `vibe exec --
  bash /vibe/payload/container/edit.sh <mode>` with `-d` on the
  workspace so the engine resolves the project, and a failed exec
  (stopped container) holds the popup open with the `vibe up` hint
  instead of flashing away. edit.sh owns the container side: it
  points `XDG_CONFIG_HOME` at the payload (nvim and lazygit both
  resolve under it), scratches data/state/cache, and execs the mode.
- **Customization:** the host passthrough (your own editor and
  config) stays one `~/.config/vibe/tui.conf` rebind away and is
  recorded in the backlog; the knob list stays honest.

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
click-skew regression class. For the editor popups: with the
container stopped, `prefix+f/g` must hold the popup open with the
`vibe up` hint, never flash-and-close; and the parser layer's proof
is a `vibe rebuild` (the headless nvim-treesitter install is the one
build step this repo's tests cannot execute).

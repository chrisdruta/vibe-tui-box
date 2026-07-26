# TUI layout spec

**Authority: design (layout contract).** The BACKLOG's layout pass
demanded a written spec before any wiring — layout arithmetic is where
the sidebar bugs have lived (click-map skew, dot wrapping, resize
ballooning). Wiring follows this file; disagreements edit it first.
Updated 2026-07-25 to the shipped state (the `_frame` move and the
bottom-bar tray included). Updated 2026-07-26 with the polish-pass
calls (roster flow, sidebar footer, detail display form, status-right
separators, editor popups), wired the same day. Updated 2026-07-26
(second pass) with the agent-surfaces contract — tray ghost cells
(phase 2) and the nested sidebar roster; design agreed on mockups,
wired the same day (one deviation, recorded in place: a ghost's click
dispatches `vibe attach SESSION`, not `vibe agent -s NAME`). Updated
2026-07-26 (third pass) with the launch-surface contract — the `+`
cell becomes the agents chooser and parallel instances stay inside
the CLI; design agreed on the first agent-surfaces dogfood, wiring
queued (BACKLOG NEXT UP).

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
│ ▍  ⎇ main             │                                           │
│ ▍  ● dev · 9766b8d8   │                                           │
│ ▍  ▲ 2 pending        │              agent pane                   │
│ ▍  ● claude  Fable 5  │                                           │
│                       │                                           │
│ ▏rojo-game ●          │                                           │
│ ▏  ● codex  needs in… │                                           │
│                       │                                           │
│  · cold-project       │                                           │
│                       ├─ host ────────────────────────────────────┤
│ C-Space·Space palette │ chris@host:~/dev/vibe-tui-box$            │
├───────────────────────┴───────────────────────────────────────────┤
│ ─────────────────────────────── rule ─────────────────────────────│
│ 🥡 vibe-tui-box  ▤  ● claude  ● codex  +           ⌨ · ● · 18:51 │
└───────────────────────────────────────────────────────────────────┘

prefix+f → ╭─ files · nvim/oil ───╮   (90% popups, container-side via
prefix+g → ╭─ git · lazygit ──────╮    vibe exec — a cold host needs
                                       nothing installed)
```

The outer box is the terminal window's edge, drawn only to frame the
figure — the UI adds no outer border; pane borders and the two status
lines are the only chrome. Left to right, top to bottom: sidebar
(fleet section with agent rows nested inside each project block under
its gutter bar, footer hint on the last row), agent pane with
role-gated border title, host dock collapsed to its 1-row strip, then
the two status lines (rule + tray — the second tray tab here is a
ghost cell: a container-side session with no viewer window yet).

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
| ghost cells | container-side sessions with no window, dim italic on surface behind a hairline inset; clickable per session (`agent-NAME` range → attach-only viewer spawn), rendered into `@vibe_ghosts` by `vibe _frame` |
| `+` cell | clickable — opens the **agents chooser** (launch what's down, reach what's up — "Launch surfaces" below; wired to the full palette until the chooser ships) |
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
no chrome. (Reaffirmed 2026-07-26 against a `🥡 brand: project` cell —
the coral gutter bar and per-project session already tie the tray to
its project; revisit only on real multi-project dogfood confusion about
which project a tray belongs to, e.g. fullscreen with no OS title.) The palette lives in `scripts/palette.sh` — one definition
serving `prefix+Space` and the 🥡 cell; the `+` cell graduates to the
agents chooser ("Launch surfaces" below). The title channel the
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

### Agent surfaces: presence vs activity (2026-07-26, supersedes the aggregate roster)

Three surfaces show agents; each gets one contract, and no agent or
project is drawn twice on the same surface (the aggregate roster broke
this: every agent appeared as a dot on its project's fleet row AND as
a two-line roster entry whose detail line re-printed the project
name).

| Surface | Contract |
| --- | --- |
| tray | **Presence & reach** — every container-side agent session, viewer window or not, one click away |
| sidebar | **Activity & signal** — projects in use, with nested agent rows for states that need eyes |
| `vibe ps` | **Full truth** — every session with CLI/model detail, scriptable |

- **Tray ghost cells (phase 2).** Sessions without viewer windows
  render in the winlist as dim cells (shipped as the proposal: italic
  + hairline inset over the surface color; dim-only stays the recorded
  fallback if a terminal fights the italics), each a
  `mouse_status_range` user range (`agent-NAME`) like the brand/▤/+
  cells. The dot carries real state (an attention coral is visible
  with no window). Click dispatches the **attach-only** viewer spawn:
  it never starts or restarts an agent; the ghost graduates to a real
  tab on the next refresh. Rows come from the `vibe ps` fetch cache
  (the sidebar's cached engine layer) — the `#(vibe _state)` splice
  stays the conf's whole splice budget.
- **The attach-only spawn is `vibe attach SESSION`** (wiring, 2026-07-26;
  supersedes this section's original `vibe agent -s NAME`). `-s` takes a
  session SUFFIX, and the address grammar is `agent(-cmd)(-name)(-cold)`
  — so `-s` cannot name the default `agent` session or an `-a` variant,
  the two most likely ghosts, and `vibe agent` would launch a CLI if the
  session had died since the fetch. `vibe attach` takes the full address
  the `vibe ps` join already reports and does exactly one thing:
  `tmux new-session -A` on it. One host script (`scripts/agent-open.sh`)
  owns the spawn for both doors — the tray range dispatch and the
  sidebar's viewer-less rows — the palette.sh precedent. It carries the
  `VIBE_NESTED` marker like `vibe agent`, so the viewer it opens is
  reapable when the UI dies.
- **The ghost channel.** No conf-side engine call: the sidebar's frame
  renderer already joins `vibe ps` truth against this server's windows,
  so it publishes the rendered cells as the session option
  `@vibe_ghosts` (a third `vibe _frame` protocol line) and the
  generated winlist splices them with `#{E:@vibe_ghosts}`. The tray and
  the sidebar therefore read one join and can never disagree about what
  exists. The sidebar is that channel's only publisher: toggling it off
  clears the option rather than leaving the bar advertising a roster
  nothing refreshes.
- **Sidebar nesting.** Agent rows sit inside their project's fleet
  block, one line per agent (state dot + CLI name + dim model — the
  project context is positional, so the `model · project` detail line
  is gone). The `─ agents ─` ruled section is gone with it.
- **The signal filter.** A nested row appears for states that ask
  something of the operator: `working`, `running`, `attention`,
  `exited*`, `gone`/frontend-dead. `idle` collapses to its dim dot on
  the project name row — presence without a row. Hiding by
  "inactivity" was considered and rejected: `exited` is inactive and
  is precisely the highest-value glance. Full presence lives in the
  tray and `vibe ps`. The name row's dots are the VIEWER windows' —
  a viewer-less idle session earns neither a row nor a dot here, only
  its tray cell and its `vibe ps` line. Reading the filter needs the
  raw state, which a glyph cannot carry (● is working, running, and
  idle), so state-render.sh stamps `@vibe_state` on the window beside
  the pane and `@vibe_session` as the join key against `vibe ps`;
  windows from an artifact older than that stamp degrade to
  glyph-only signal (✗/◌ and the attention flag) and keep their dots.
- **Viewer-less rows.** A container-side session with signal earns a
  sidebar row even without a window; its click uses the same
  attach-only spawn as the tray ghost. One filter rule regardless of
  whether a viewer exists.
- **Project boundaries: gutter bars, not boxes.** The 2-col gutter
  carries a bar spanning the project's block — coral `▍` for the own
  project (generalizing the existing self marker), border-hex `▏` for
  other in-use projects, none for cold rows; the blank slop row stays
  the vertical separator. Boxes were rejected: the chrome rule above
  (pane borders + two status lines are the only chrome) and the
  30-col budget both forbid border art.

### Launch surfaces: the agents chooser (2026-07-26, third pass)

The first agent-surfaces dogfood broke the LAUNCH side three ways: the
only launch door was the full palette behind `+` (destructive items
one misclick from a "new" affordance); the menu was not mouse-usable
when opened from the tray (MouseDown1Status fires on press,
`run-shell -b` opens the menu async, and the release lands on the
just-appeared menu — tmux dismisses it); and the palette's "agent"
item read as *new* agent while running attach-or-launch `vibe agent` —
with claude live it opened a second viewer on the SAME inner session,
where closing the duplicate merely detaches but Ctrl-C inside kills
the shared session for both, and nothing on screen distinguishes the
two.

Three intents, one owner each:

| Intent | Owner |
| --- | --- |
| open an agent that exists | tray tab / ghost cell / sidebar row (shipped, attach-only) |
| start a new agent | the `+` agents chooser |
| manage a running agent | palette stop/restart (default session; addressing is an open call) |

- **The launch unit is the installed CLI, one per project.** The
  chooser lists one entry per `image.agents` (manifest default
  first), then container shell and host shell. Entries are
  state-aware from the same `vibe ps` fetch cache the ghost cells
  read — the chooser and the tray cannot disagree: a CLI that is
  down launches (`vibe agent` / `-a CLI`); one that is up shows its
  dot and ATTACHES (window jump when a viewer exists, the attach-only
  spawn when not). "New" never silently reattaches and never mints a
  hidden twin.
- **Another claude is claude's job.** Claude Code's background-session
  manager (`←` at the prompt: describe a task → its own session,
  triaged Needs input / Working / Completed, survives the terminal)
  IS the "separate thing" a second instance would reimplement one
  level down — with two writers on one working tree. The chooser's
  claude entry carries the `← for agents` hint; vibe hosts and
  surfaces the CLI's parallelism, it does not manage it.
  `vibe agent -s NAME` stays a CLI-only power tool (deliberate,
  named, shares the checkout); the only UI-worthy parallel instance
  is one with its OWN checkout — the container-per-instance escape
  valve recorded in the BACKLOG beside "Productize worktrees",
  demand-gated.
- **The chooser is engine-rendered.** The shell cannot know
  `image.agents` (the frame-renderer reasoning): a porcelain renders
  the display-menu items from the manifest joined with the agents
  cache; the `+` range and a palette item dispatch it.
- **Tray-opened menus get `-O` and a pinned position** (anchored
  above the tray) so the press-open/release-dismiss race stops eating
  the first click — the palette too. Live-tmux verification class.
- **Palette hygiene.** 🥡 / `prefix+Space` keep the full palette; its
  bare "agent" item retires in favor of the chooser (the label
  promised "new", the semantics delivered attach-or-launch).
  stop/restart keep addressing the default session; per-session
  management is an open call (the `vibe ps` popup is the likely
  door — sidebar rows stay render-only by decision record).
- **Dot semantics under N background sessions (open call).** The
  hook-fed dot approximates "any session needs me" today. Revisit
  trigger: the dot reads idle while claude's agents screen shows
  Needs input — then the statusline JSON's awaiting-input count
  feeds the dot (and maybe the `▲n` pattern).

### Sidebar frame contract (`vibe _frame` owns this)

All sidebar layout arithmetic lives in the engine renderer
(`internal/tmuxui/frame.go`); `sidebar.sh` pipes tmux porcelain in and
never does layout math. The contract the renderer implements:

- Row 0 stays blank. The **fleet section** flows from row 1: per
  session a project block under its gutter bar — a name row carrying
  the idle-agent dots, a `⎇ branch` row when known, engine facts
  (stale/stopped glyph, `▲n`, `dev`) or the own project's detail
  block, the **nested agent rows**, and a blank slop row. Non-agent
  rows claim the session as click target. Cold registered projects
  (fleet entries with no live session) render dim, barless, and
  unclickable — click-dispatching `up` is a recorded open product
  call, not half-shipped here.
- The **nested agent rows** close each project block (agent-surfaces
  decision above; supersedes the flowing aggregate roster, itself the
  2026-07-26 successor of the midpoint rule): one line per
  signal-state agent — state dot + CLI name + dim model — with the
  window jump (`SESSION:WINDOW`) as click target, or the attach-only
  viewer spawn when the session has no window. Idle agents collapse
  to their dim dot on the name row. When a block's rows don't fit,
  its last slot becomes a per-block `… +n agents` overflow. The rows
  stay render-only beyond their one click — no dismiss/kill
  affordance (BACKLOG decision record, 2026-07-25).
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
  a session's name budget shrinks 2 cols per idle dot (floor 8) so a
  long name can never wrap the dots and skew the click map; nested
  agent rows spend the gutter bar + indent + dot (`budget−4`, floor
  8) with the dim model suffix dropped first when the name and model
  can't share the line. The gutter is 2 cols (blank, then the bar),
  every row of a block sits under it, and each row ends one col clear
  of the right edge.
- Content **clips** at the footer's row instead of drawing past the
  pane: a row the pane cannot show enters neither the body nor the
  click map (a frame that overran used to paint into the last row and
  map clicks to invisible rows). Per-block `… +n agents` covers the
  common case; a whole block pushed off the bottom is silent, as
  before.
- The engine truth the frame reads is cache-only and fleet-wide:
  `vibe _agents` (the `vibe ps` join, one row per container-side
  session keyed to its project) joins the fleet and detail caches the
  background fetch fills. It costs one container exec per project
  whose dev container is running, on the engine cadence — never on a
  frame.

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
`internal/tmuxui/views_test.go`, frame layout/click-map/truncation,
the signal filter, the gutter bars, and the ghost cells' format
escaping in `internal/tmuxui/frame_test.go`, the porcelain round trips
(`_fleet`, `_agents`, the frame's tmux records) beside them, and the
user-conf epilogue in `internal/app/tui_test.go`. The manual check that
has caught what tests miss: resize the sidebar and click every row type
— the click-skew regression class; with the nested rows that now
includes a ghost row (it must open a viewer, never start an agent).
Once the chooser ships, its porcelain round trip is table-tested like
the fleet's, a running entry must attach (never double-launch), and
the manual mouse check is opening the chooser by CLICKING `+` and then
clicking an item — the `-O` regression class. For the editor popups: with the
container stopped, `prefix+f/g` must hold the popup open with the
`vibe up` hint, never flash-and-close; and the parser layer's proof
is a `vibe rebuild` (the headless nvim-treesitter install is the one
build step this repo's tests cannot execute).

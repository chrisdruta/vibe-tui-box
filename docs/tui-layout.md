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
the CLI; design agreed on the first agent-surfaces dogfood, wired the
same day. Updated 2026-07-26 (fourth pass) with the roster and the
meta line: every live agent is a sidebar row (idle dim), each block
reads identity → meta → roster, and ● is agents-only on that surface.
Updated 2026-07-29 (signal-density pass) with age, state words,
counts, churn, and the footer's second row — design agreed on
mockups, shipped the same day. Updated 2026-07-29 (polish pass) with the meta
line's segment-boundary overflow wrap, the roster ages' `<1m` floor,
the `bright` palette entry, the pinned canvas (`window-style` /
`popup-style` bg), and the ▤ cell's move to the bar's left cluster —
screenshot dogfood, wired the same day. Updated 2026-07-29 (spinner)
with the working-dot animator: the `@vibe_spin` channel, the tab and
ghost formats, and the sidebar's sub-tick overlay — the status-redraw
mechanism measured on the pinned 3.7b, wired the same day.

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
| `▤` cell | clickable — toggles the host dock (prefix+t as a button); clicking the collapsed dock strip itself also expands it. Sits in the LEFT cluster beside the brand (2026-07-29 polish pass — spliced into the absolute-centred winlist it floated with the tabs, a global chrome control reading as part of the window list; this table and the frame mockup always drew it at left). Outside the `#{client_prefix}` swap, it stays put while the cheatsheet replaces the middle |
| tabs | per-window `dot name`, absolute-centred; the name is the CLI actually running (state-render renames the window from the title channel's display field), attention flash |
| ghost cells | container-side sessions with no window, dim italic on surface behind a hairline inset; clickable per session (`ghost-N` index range resolved through `@vibe_ghost_map` → attach-only viewer spawn — range names clip at 15 bytes, "Launch surfaces"), rendered into `@vibe_ghosts` by `vibe _frame` |
| `+` cell | clickable — opens the **agents chooser** (launch what's down, reach what's up — "Launch surfaces" below) |
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
replaceable and no `@vibe` knob (verdict capture, the third survivor,
was later dropped outright — 2026-07-27; the closed revival record
lives in the backlog's git history).

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

### Preview window: ctrl+click follows paths, images render sixel (2026-07-27)

Ctrl+click is a host-owned gesture over EVERY pane: the conf's
`C-MouseDown1Pane` routes to `scripts/open-path.sh`, which reads the
clicked line back via capture-pane (`#{mouse_word}` fragments on the
3.7 default word-separators; q-escaping `#{mouse_line}` shifts the
column arithmetic — both rejected by test), walks a path out around
the mouse column (optional `:line`/`:line:col`), maps host-workspace
prefixes onto the container's `/workspace` (any other absolute path
is tried as-is in the container first — `vibe clip`'s `/tmp` drops;
only a container miss falls back to the host-side "outside the
workspace" message), and verifies existence over `vibe exec`. Text opens in the review stack's nvim popup
(review.sh/edit.sh `file` mode, `+line` jump). Everything that isn't
a resolvable path is a silent no-op — the gesture costs prose nothing.

Image extensions (png/jpg/jpeg/gif/webp/bmp) open the **preview
window**: ONE reusable window per project session, found by its
`@vibe_view` marker (the window NAME carries `⌗ filename` — a name
lookup would break the reuse the first time the name changed),
respawned per click, running `vibe exec -- show-image.sh FORMAT PATH`.
chafa (pinned source build in the tools image, v1's exact
version+checksum — the image half of the review stack) encodes; the
HOST tmux ingests the sixel natively and re-emits it on redraw. That
is the v1 lesson upheld (b2819b1: passthrough dies under nesting,
native ingest survives), simplified: this window is a host pane, so
only one tmux layer is ever in the loop — the container tmux pin
matters for future in-transcript images, not here.

Fidelity is gated at click time, **loudly** (2026-07-27 decision:
degradation the operator can see, never silent): sixel only when the
host server is >= 3.7 (older tmux drops the raster on adjacent-pane
redraws — the sidebar tick would wipe it; probe verdict A on the
dogfood host, 2026-07-27) AND the client negotiated sixel. Anything
less renders chafa symbols under an inverse-video "low-fi preview"
header, and `vibe doctor`'s tmux check says the same one screen
earlier. A resize clears sixel on every tmux (upstream reflow), so
show-image.sh re-renders on SIGWINCH — sidebar/dock toggles self-heal
instead of leaving a blank pane. Any key closes; the window dies with
its process.

Two hard limits shape the render, both learned by bisection
(2026-07-27): the exec pty is born 0x0 unless ExecCreate pins
ConsoleSize (the resize forwarder's first send races the exec start
and is rejected — dockerapi pins it now, and render treats any
degenerate stty answer as "let chafa fall back"), and tmux discards
any sixel DCS longer than its 1MB input buffer WHOLE, not clipped
(827KB showed, 1.27MB vanished). show-image.sh therefore encodes to
scratch and shrinks by thirds until the emit fits under 900KB before
a byte touches the terminal.

### Default arrangement

Agent pane dominant; sidebar far left at `@vibe_sidebar_w` fixed cols,
one per window kept in lockstep; dock parked collapsed (1 row) on
session create, expanding to `@vibe_dock_size`; pane borders on top
with role-gated dot + title.

### The canvas: panes default to the palette bg (2026-07-29)

`window-style "bg=#{@thm_bg}"` pins every pane's default background to
the palette, and `popup-style` gives popups the same canvas (plus fg —
transient chrome like the menus, which already carried `menu-style`).
Before this the chrome floated on whatever bg the emulator happened to
carry: the tabs' surface insets, the ghost cells' hairlines, and the
sidebar's dim ramp are all mixed against `@thm_bg`, and the screenshot
dogfood's purple host scheme showed them on a bg they were never tuned
for. Pane FOREGROUND deliberately stays the terminal's — inner apps
own their text; bg is the one attribute the chrome needs pinned.
Light-scheme hosts override both in the user conf, the sanctioned
customization point.

### The working spinner (2026-07-29)

The `working` dot animates — a braille orbit (`theme.go
SpinnerFrames`, rendered into theme.sh as `VIBE_SPIN_FRAMES`) that is
**presentation of the state, never a state glyph**: every surface
falls back to the static ● whenever no animator runs, and the frames
are visually disjoint from the dot/circle vocabulary so a frozen frame
cannot be misread as a state. Three surfaces, two mechanisms, one
budget rule (no new `#()` splices, no `status-interval` change, zero
cost while nothing works):

- **The tray rides an option.** `spin.sh` — one per server, noclobber
  lock beside the tmux socket (no flock(1) on stock macOS; the race
  left open costs a duplicate animator writing identical frames) —
  rotates the global `@vibe_spin` at 4Hz while any window's
  `@vibe_state` is `working`, and exits restoring the static dot when
  nothing is (liveness checked every ~2s). **Measured on the pinned
  3.7b: a bare `set -g` on a user option redraws the status line by
  itself, ~350 bytes, status only** — that measurement is the whole
  design: the tab-dot formats read
  `#{?#{==:#{@vibe_state},working},#{@vibe_spin},#{@vibe_glyph}}`, a
  working ghost cell's glyph is the `#{@vibe_spin}` ref itself, and
  the conf defaults the option to the static ● (`-goq`) so no
  animator means exactly the pre-spinner bar. Two spawn doors, both
  blind behind the lock: state-render.sh on every `working` event
  (instant, viewer windows), and the sidebar's frame whenever it
  carries spin cells (the healer — a viewer-less working agent has no
  title events on this server).
- **The sidebar repaints its own cells.** `vibe _frame --spin` (the
  fifth protocol line, before the body; flag-gated so an older
  sidebar.sh keeps its four-line protocol across a binary swap)
  reports the drawn working dots' ANSI `ROW:COL`, and the render loop
  sub-divides its 2s tick into four 500ms steps that repaint exactly
  those cells — pure printf, no tmux round trip, no engine call per
  animation frame. The frame's own glyph stays the static ●, so a
  skipped overlay degrades to the pre-spinner look.
- **The pane-border dot deliberately does NOT animate.** Option
  writes repaint the status line only (the same measurement), so a
  border reference to `@vibe_spin` would freeze on whatever frame was
  current at the border's last redraw and read as broken. The border
  keeps ●; the working agent's own TUI is right below it anyway.

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
  `mouse_status_range` user range (`ghost-N` — an index, because range
  names clip at 15 bytes; "Launch surfaces") like the brand/▤/+
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
- **Cold entries in the chooser (2026-07-28, Chris — supersedes the
  "cold is the tray's business" note in the chooser tests).** The
  chooser grows a `:cold` block after the warm one: one entry per
  installed CLI that `vibe agent --cold` can actually start without
  repo instruction files (claude/codex — `tmuxui.coldKinds`, kept in
  lockstep with agent-session.sh's instruction-skip case; grok stays
  out until that script learns its flag). Same reach-vs-launch
  verdicts on the `-cold` address, same never-a-second-viewer rule;
  the new verbs `launchc`/`launchac` are the `--cold` twins of
  launch/launcha. Before this, cold sessions were reachable
  everywhere but launchable nowhere in the tui — the only door was
  typing the verb in a shell.
- **The stop door: right-click on the bar (2026-07-28, Chris —
  supersedes the backlog's "vibe ps popup is the likely door" lean).**
  Right-click on an agent viewer TAB or a ghost cell opens
  agent-menu.sh for that ONE session: stop agent (address-direct —
  `vibe _stop ADDRESS` → agent-session.sh `kill`, attach's precedent;
  reverse-mapping an address into `--stop` flags would recompute the
  grammar in a second place), open viewer (ghosts), close viewer only
  (tabs — the stock Kill gesture renamed honestly, since it never
  stopped the session). The menu opens on the RELEASE per the
  tray-door lesson; the press arm serves non-agent tabs a trimmed
  copy of the pinned tmux's stock window menu (the one default the
  bind costs). A tab's stop BURIES ITS OWN VIEWER (2026-07-28): the
  death record rides the pane's title channel, which the stop just
  killed, so the pane-died hook can never see the death as explained
  and would leave a "Pane is dead" corpse forever — we ordered this
  death, the window goes with it. Crash corpses keep the
  corpse-with-hints fate: an unexplained death should stay visible.
  The same dogfood drew the dead/live line across the whole bar:
  GHOST CELLS RENDER ONLY LIVE SESSIONS — a dead ghost's click ran an
  attach that refuses dead sessions by design, a button wired to a
  refusal — while dead stays on the signal surfaces, the sidebar's ✗
  row and the chooser's launch-again entry. The watch channel
  repaints the roster after a stop. Restart stays
  palette-default-only until dogfood asks for per-address.
- **The ghost channel.** No conf-side engine call: the sidebar's frame
  renderer already joins `vibe ps` truth against this server's windows,
  so it publishes the rendered cells as the session option
  `@vibe_ghosts` plus the cells' session names in range order as
  `@vibe_ghost_map` (`vibe _frame` protocol lines two and three) and
  the generated winlist splices the cells with `#{E:@vibe_ghosts}`.
  The tray and the sidebar therefore read one join and can never
  disagree about what exists. The sidebar is that channel's only
  publisher: toggling it off clears both options rather than leaving
  the bar advertising a roster nothing refreshes.
- **Sidebar nesting.** Agent rows sit inside their project's fleet
  block, one line per agent (state dot + CLI name + dim model — the
  project context is positional, so the `model · project` detail line
  is gone). The `─ agents ─` ruled section is gone with it.
- **The roster: every live agent is a row** (2026-07-26, Chris —
  supersedes the signal FILTER, which collapsed `idle` to a dim dot
  on the project name row; three dogfood rounds read that dot as
  "claude is missing", and the asymmetry made it worse: a hookless
  CLI caps at `running` and kept a full row while napping, so the
  better-instrumented agent looked less present). Signal now STYLES
  instead of hiding: `idle` rows render their name dim (the dot keeps
  its state color), signal states — `working`, `running`,
  `attention`, `exited*`, `gone`/frontend-dead — keep the fg color,
  attention keeps its coral dot. The name row carries no agent dots
  anymore. Hiding by "inactivity" stays rejected for the original
  reason: `exited` is inactive and is precisely the highest-value
  glance. Reading idle-vs-signal needs the raw state, which a glyph
  cannot carry (● is working, running, and idle), so state-render.sh
  stamps `@vibe_state` on the window beside the pane and
  `@vibe_session` as the join key against `vibe ps`; windows from an
  artifact older than that stamp degrade to glyph-only signal (✗/◌
  and the attention flag).
- **Viewer-less rows.** A container-side session earns a sidebar row
  even without a window (idle included — the roster rule); its click
  uses the same attach-only spawn as the tray ghost, or jumps to the
  stamped window when one exists. One rule regardless of whether a
  viewer exists.
- **Project boundaries: gutter bars, not boxes.** The 2-col gutter
  carries a bar spanning the project's block — coral `▍` for the own
  project (generalizing the existing self marker), border-hex `▏` for
  other in-use projects, none for cold rows; the blank slop row stays
  the vertical separator. Boxes were rejected: the chrome rule above
  (pane borders + two status lines are the only chrome) and the
  30-col budget both forbid border art.

### Workspace services: svc windows join the surfaces (2026-07-28, shipped same day)

Terminology first, because two things now share a word: **sidecars**
are the manifest's `services:` — planned infrastructure in their own
containers, digest-bound and approval-gated. **Workspace services**
are svc.sh windows in the in-container `services` tmux session —
workload processes (dev servers, MCP daemons, headless tools) stood up
by `.vibe/hooks/post-start.sh` at workload trust. This section is
about the latter; today their only door is `vibe attach services` and
the sidebar cannot see them at all.

- **Data: a second pass in agent-ps.sh**, one row per window of the
  `services` session — `name|state|epoch|detail`. No state records,
  no title channel: a service window is a pane directly on the inner
  server, so `#{pane_dead}` + `#{pane_dead_status}` carry liveness AND
  the exit code — the fact that never crosses the docker-exec client
  boundary for agents, which is what forced their whole records
  system. The state vocabulary folds to `running` / `exited(RC)`;
  there is no idle/working/attention because nothing here converses.
  Rows ride the existing fetch-cache exec on the engine cadence —
  zero extra container round-trips.
- **Corpses are kept**: svc.sh sets `remain-on-exit` on the services
  session, so a dead window persists with its scrollback (the crash
  log) instead of vanishing — without this the sidebar could never
  show ✗ for a service, because tmux would have closed the evidence.
  Same philosophy as the agent corpse-with-hints fate: an unexplained
  death stays visible until the operator clears it.
- **Engine**: `PSResult` grows service rows beside the agent rows;
  `vibe ps` prints them (full-truth surface stays full).
- **Sidebar**: service rows close the project block AFTER the nested
  agent rows — the name dim while running (signal styles, never hides
  — the roster rule), `✗ name` bright when dead. The per-block `… +n`
  overflow is shared with agent rows and tallies entries only.
  **Grouped block (second dogfood, 2026-07-28)**: when a block has
  services, the roster splits under dim render-only `agents` /
  `services` header rows with entries hanging off `├`/`└` tree
  connectors, and the per-row `svc` qualifier is gone — the header
  says it once. This partially revives the section labels the
  aggregate-roster era removed, on new grounds: positional context
  disambiguated one kind of row, not two. A block with NO services
  keeps the flat form verbatim — the common agents-only project pays
  no rows and no connectors.
- **Tray: nothing.** The tray contract is presence-and-reach for
  agent sessions; service reach is one door (the `services` session)
  and adding cells would re-break "no entity drawn twice".
- **Clicks**: LEFT is reach — the attach-only spawn on address
  `services` (the `@vibe_session` stamp dedups the viewer exactly as
  for agents), selecting the clicked window. The row target always
  carries the window NAME (`svc-`/`svcx-`), never the viewer window
  id: the services viewer is shared, and an id target would strip the
  name the right-click menu resolves verbs through. The
  shipped-with-a-gap version (viewer raised without re-aiming its
  inner window; re-aim on fresh spawns only, to save a container exec
  per click) survived exactly one dogfood: with the shared viewer
  open, every service click read as "goes to the first service". The
  re-aim is now unconditional — a fresh spawn selects via
  `vibe attach services WINDOW`, an existing viewer via a backgrounded
  `vibe _svcselect` (agent-session.sh `svc-select`; the exec-per-click
  cost was the wrong thing to save). RIGHT serves the per-row
  menu the sidebar already speaks: live rows get **stop**
  (`kill-window` — honest and cheap; services are the operator's own
  workload, so the "never drive agents" cession does not apply), dead
  rows get **dismiss** (clear the corpse). **Restart is rejected for
  now**: the command lives only in post-start.sh, and running an
  engine lifecycle hook out-of-band is a line not worth blurring — the
  recorded follow-on is a per-project "re-run post-start" palette verb
  that heals ALL missing services through the hook's own idempotence,
  if dogfood asks for it.
- **Chooser: out of scope.** Launching a service IS the hook; there is
  nothing for a launch surface to offer.

### Signal density: age, words, counts, churn (2026-07-29, designed; shipped same day)

A screenshot audit of tools one layer up (an agent multiplexer, a
worktree orchestrator, a task manager — unnamed here, per the
README's stance) surfaced six glance signals the sidebar withholds
while already owning most of the data. Design agreed on mockups; the
inline treatment won — one line per entry stays law. Target block:

```
▍ vibe-tui-box ●
▍   ⎇ main  +128 −40
▍   ● dev · 9766b8d8
▍   ▲ 2 pending
▍   agents · 2
▍   ├ ● claude  Fable 5   42m
▍   └ ● codex              3h
▍   services · 3
▍   ├ ● web                2h
▍   ├ ● logger             2h
▍   └ ✗ idler  exit 1     12m

 C-Space·Space palette
 f files · g git · v clip
```

- **Age on every roster row** — right-aligned, dim, compact
  (`42m`/`3h`/`2d`), meaning time IN STATE, not session uptime.
  Agent cache rows already shipped an epoch in agent-ps.sh's rows and
  the porcelain dropped it (now threaded through, `_agents` grammar
  v2); window rows get theirs from state-render.sh stamping
  `@vibe_state_epoch` beside `@vibe_state` only when the value
  CHANGES. (The design's "services already ship an epoch" premise was
  false against the code — the second pass emitted an empty field; it
  now derives one: /proc starttime for a live window — tmux has no
  pane-start format and `#{window_activity}` resets with every byte a
  chatty service prints — and `#{window_activity}` for a dead one,
  where it stopped moving at death.) The text renders at frame time
  from cached epochs — minute granularity needs no cadence the frame
  does not already have; no new polling, no extra container
  round-trips. Sub-minute ages floor at `<1m` on this surface
  (2026-07-29, the polish pass): exact seconds shimmered across
  near-identical rows on every forced frame and overstated the 10s
  redraw cadence — "19s" was up to 10s stale while reading live.
  `vibe ps` keeps the exact seconds: a point-in-time snapshot is
  accurate at the moment it prints.
- **State words for `attention`/`exited` only** — the word takes the
  model's slot on exactly the rows where the dot's color is the whole
  message today (glance ambiguity, and color-blindness). Nominal rows
  pay nothing: signal styles, never hides, and quiet stays quiet.
- **Dead-service forensics** — `✗ idler  exit 1     12m`: the RC
  already rides the folded `exited(RC)` state from
  `#{pane_dead_status}`; plumb it to the row instead of collapsing to
  a bare ✗. Pairs with the age: a corpse tells you what and when.
- **Group-header counts** — dim `agents · 2` / `services · 3` on the
  headers the grouped block already draws, and the shared `… +n`
  overflow folds IDLE rows first: a signal row is never the hidden
  one.
- **Churn on the branch line** — `⎇ main  +128 −40`, dim. One
  host-side `git diff --shortstat HEAD` (staged and unstaged both
  count: the question is "has the agent changed anything", not "what
  isn't staged yet") per in-use project, riding the cached engine
  layer (`_fleet` grammar v2, the churn field before the name) on the
  `@vibe_engine_refresh` cadence — never the frame path, so
  `#(vibe _state)` stays the conf's whole splice budget. Answers "has
  the agent actually changed anything" without opening lazygit.
- **The footer's second row** — `f files · g git · v clip` under the
  cold-start palette pointer, height-gated: it renders only when the
  frame has slack, so a short pane loses the new hint, never the old
  one. The review stack is the product's best surface and, before the
  prefix is known, its least discoverable.

Budgets: per-row drop order under the text budget is dot+name never,
age second-to-last, model/detail first — extending agentLabel's
model-drops-first precedent. Porcelain: `AgentEntry` grows `Epoch`
and `Detail`, and the `_agents` grammar gains the two fields (the
leading version field exists for exactly this).

Rejected in the same pass: the two-line detail row (bends "every
live agent is a row" into "some agents are two"), and the per-agent
activity one-liner — container-fed free text rendered into trusted
sidebar chrome crosses the `terminal.Encode` separation the broker
surface is built on; that one is a decision record to write, not a
cleanup to ship, if dogfood ever asks.

### Launch surfaces: the agents chooser (2026-07-26, third pass)

The first agent-surfaces dogfood broke the LAUNCH side three ways: the
only launch door was the full palette behind `+` (destructive items
one misclick from a "new" affordance); the menu was not mouse-usable
when opened from the tray (mechanism below — it was never usable by
mouse at all); and the palette's "agent"
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
  `image.agents` (the frame-renderer reasoning): `vibe _chooser`
  renders the verdict rows (`label / key / verb / arg`) from the
  manifest joined with the agents cache plus the choosing session's
  window porcelain; `scripts/chooser.sh` keeps the tmux mechanics —
  it composes the display-menu items (charset-vetting every field
  that becomes a shell word or target, like agent-open.sh) and falls
  back to the palette if anything upstream is missing, so a dead
  chooser can never eat the tray's only launch door.
- **Script-opened menus need `-M -O` together, and menu doors open on
  MouseUp** (established 2026-07-26 against the pinned 3.7b, twice:
  PTY mouse-injection rigs plus reading `menu.c` after both
  single-flag fixes failed dogfood in turn). The full mechanism —
  three layers, each masked the next:
  1. *No `-M`*: a menu opened by a CLI client (a script under
     `run-shell`) carries no mouse event and tmux marks it NOMOUSE —
     item clicks are swallowed outright and a button RELEASE reads as
     "other button" and closes it. Every script menu was born
     keyboard-only (the palette's "click bait"), and any menu that
     opened while the tray click's button was still down died on that
     click's release — hence the MouseUp door.
  2. *`-M` alone*: the menu itself switches the client to all-motion
     tracking (1003), and a bare motion event is RELEASE-CODED
     (SGR button 35 & MOUSE_MASK_BUTTONS == 3). The first pointer
     twitch outside the box is treated as a release outside → close
     ("disappears when I move my mouse"). Motion inside with nothing
     aimed yet is just as fatal.
  3. *`-M -O` (stay-open)*: motion events hover and AIM the choice, a
     press fires the aimed item, a press outside closes, and motion
     outside is tolerated. Correct for every real pointer: the menu
     turned 1003 on, so aiming motion always precedes the press. The
     earlier "`-O` press-chooses-last-hover" rejection was measured on
     a NOMOUSE menu (no `-M`), where no motion was processed at all —
     wrong conclusion, right observation.
  Both scripts pass `display-menu -M -O`; the two menu DOORS dispatch
  on `MouseUp1Status` — the opening click's release is already spent
  before the menu exists. Immediate actions (tabs, dock, req, ghost
  cells) stay on MouseDown: press-act is tmux-native there and no menu
  is involved. Position: the chooser pins above the tray (`-y S` — it
  is the `+` button's menu); the palette takes display-menu's default,
  centered both axes — the look it always had (a `-y S` on the palette
  bottom-anchored the tall menu and read as broken centering,
  2026-07-26 dogfood).
- **Status range names clip at 15 bytes, so ghost ranges carry an
  index** (found 2026-07-26 dogfood: tmux's `struct style_range` keeps
  the name in `char string[16]`, so the ghost cell range
  `agent-agent-codex` dispatched as `agent-agent-cod`, the conf
  stripped a prefix, and `vibe attach agent-cod` MINTED a junk
  bare-shell inner session named `agent-cod` — four viewer windows of
  nothing). Three fixes, each independently sufficient for that bug:
  ghost cells render `#[range=user|ghost-N]` and publish the session
  names in range order as `@vibe_ghost_map` (a fourth `vibe _frame`
  protocol line riding the same render as `@vibe_ghosts`; option
  values have no length cliff) which `agent-open.sh -g N` resolves and
  re-vets; container-side `agent-session.sh attach` now REFUSES to
  create agent-convention sessions (attach means attach — `vibe agent`
  is the launch door; non-agent names keep `-A`'s create, an empty
  `services` session is a feature); and every viewer/launch window is
  stamped `@vibe_session` so the ghost/chooser dedup join never waits
  on title events a hookless CLI (codex, a bare shell) will never
  send — that stamp gap is why ghost clicks piled up duplicate
  viewers instead of clearing the ghost. The stamp is a SELF-stamp
  (2026-07-26, fourth pass — supersedes the same day's per-spawner
  stamps): `vibe agent` and `vibe attach` mark their own window from
  inside the pane (`app.stampViewerWindow`, gated on the vibe-engine
  socket), which covers every door with one definition — including
  the tui's own startup window, the one door the spawner-stamp round
  missed, which is why the restored-but-silent startup claude stayed
  invisible until its first message.
- **The viewer join counts stamps, not glyphs** (second dogfood,
  2026-07-26: codex's ghost survived three open viewers — a zombie
  button spawning another viewer per click — because the frame's
  viewed-map only counted windows with a `@vibe_glyph`, which hookless
  CLIs never earn). The rule: `viewed` = any window carrying a
  `@vibe_session` stamp, glyph or not — it clears ghosts and turns the
  sidebar cache row's click into a window jump; `drawn` (glyph
  windows) stays the narrower no-double-draw filter, so a glyphless
  viewer's session keeps its cache-fed roster row. Same principle in
  the chooser: a stamped viewer wins even with a cold/missing fetch
  cache (verdict `open`, never a `vibe agent` whose `-A` would mint a
  second viewer); a recorded-dead session still launches. The idle
  form of the same rule: a stamped-but-glyphless viewer whose cache
  state is `idle` (the startup claude — record persisted across the
  reopen, no title event yet) renders its dim roster row from CACHE
  truth, jumping to the stamped window (the roster decision above —
  this row was briefly a name-row dot, which the startup dogfood
  couldn't see).
- **Palette hygiene.** 🥡 / `prefix+Space` keep the full palette; its
  bare "agent" item retires in favor of the chooser (the label
  promised "new", the semantics delivered attach-or-launch).
  stop/restart keep addressing the default session (their labels say
  "default" since 2026-07-28); per-session management is right-click's
  job — the bar's tabs and ghost cells and the sidebar's agent rows
  all answer with agent-menu.sh (the ps-popup lean and the render-only
  record both superseded 2026-07-28).
- **Dot semantics under N background sessions (open call).** The
  hook-fed dot approximates "any session needs me" today. Revisit
  trigger: the dot reads idle while claude's agents screen shows
  Needs input — then the statusline JSON's awaiting-input count
  feeds the dot (and maybe the `▲n` pattern).

### The watch channel (`vibe _watch`, 2026-07-26, prototype)

The engine-truth latency fix ("agent updates feel slow" dogfood): the
agents cache — the `vibe ps` join feeding the tray's ghost cells, the
sidebar's cache rows, and the chooser's verdicts — used to refresh
only on the sidebar's 30s slow tick, so a hookless agent appearing or
dying took up to 30s to show anywhere. Rendering was never the
bottleneck (it is already Go, cache-only, serial-gated at 2s); the
POLL was. The watch channel replaces polling with push:

- **One daemon per project per server** (`vibe _watch`, hidden;
  spawned by the sidebar loop, self-guarding via a flock beside the
  cache so redundant spawns exit in milliseconds). It holds ONE
  long-lived `docker exec` on the container sentinel and reacts to its
  lines; the sidebar's slow tick stays as the fallback cadence, so the
  daemon is an accelerator, never a dependency.
- **The sentinel** (`payload/container/agent-watch.sh`) fingerprints
  the inner tmux sessions (names + attached counts — deliberately NOT
  `session_activity`, which moves per output byte) plus the state
  records dir listing, every 1s LOCALLY (a stat beside a unix socket
  is noise; the docker round trips stay host-side and fire only on
  change). Protocol: `E` per change (one at start = the sync fetch),
  `H` heartbeat ~15s. Upgrade path recorded in BACKLOG: a control-mode
  tmux client for true push.
- **The stdin leash.** Docker keeps exec'd processes alive after the
  client detaches, so an unleashed sentinel would stack one poller per
  daemon reconnect. The sentinel's poll sleep IS a 1s `read -t` on
  stdin: the daemon holds stdin open for the stream's life, and
  teardown (daemon exit, tmux server death, ctx cancel) closes it —
  EOF ends the sentinel within a second. Verified live: create/kill an
  inner session and touch a record → three `E`s; close stdin → clean
  exit, no orphans.
- **On `E`: fetch, publish, frame.** The daemon re-runs the fleet
  agents fetch (the sidebar's exact `_agents` path), replaces the
  cache tmp+rename (own tmp name — the slow tick writes beside it,
  last rename wins), and bumps `@vibe_state_serial` — the FRAME-only
  serial, deliberately not `@vibe_engine_serial`, which would tell the
  sidebar to refetch what was just fetched. Fetches are floored at 2s
  apart (state records churn while an agent works; each fetch is a
  docker exec), so bursts coalesce. Chain: inner change → ≤1s sentinel
  → fetch (~0.2s) → ≤2s sidebar tick → frame. **~1-3s worst case,
  down from 30.**
- **Lifecycle.** The tmux server owns the daemon: a 10s liveness probe
  exits it when the server dies (and the canceled exec closes the
  leash). Container down or stream death → capped backoff (1-10s)
  reconnect. A stale stream (no line for 60s despite heartbeats) is
  reaped and reconnected. The flock dies with the process — no pidfile
  staleness dance.

### Sidebar frame contract (`vibe _frame` owns this)

All sidebar layout arithmetic lives in the engine renderer
(`internal/tmuxui/frame.go`); `sidebar.sh` pipes tmux porcelain in and
never does layout math. The contract the renderer implements:

- Row 0 stays blank. The **fleet section** flows from row 1: per
  session a project block under its gutter bar — a name row, ONE dim
  **meta line** (2026-07-26, supersedes the separate branch row +
  multi-line detail block: three near-identical indented rows read as
  mush): `⎇ branch` then engine facts joined with ` · ` — the own
  project's compact `vibe _sidebar` line, other projects' fleet facts
  (stale/stopped glyph, `▲n`, `dev`) — then the **nested agent
  rows**, and a blank slop row. Over the text budget the meta line
  **wraps at segment boundaries** onto continuation rows (2026-07-29,
  the polish pass — supersedes the raw character clip, whose
  mid-segment `dev …` hid exactly the engine facts the line exists to
  show): one line stays the common case, and this is overflow-driven
  wrapping, not a revival of the rejected always-on multi-line block.
  A single segment wider than the budget still character-clips — the
  safety net, not the design. Non-agent rows claim the session as
  click target. Cold registered projects (fleet entries with no live
  session) render dim, barless, and unclickable — click-dispatching
  `up` is a recorded open product call, not half-shipped here.
- The **nested agent rows** close each project block (agent-surfaces
  decision above; supersedes the flowing aggregate roster, itself the
  2026-07-26 successor of the midpoint rule): one line per LIVE OR
  SIGNAL agent — state dot + CLI name + dim model, the whole name dim
  when idle (the roster decision above; idle previously collapsed to
  a name-row dot) — with the window jump (`SESSION:WINDOW`) as click
  target, or the attach-only viewer spawn when the session has no
  window. When a block's rows don't fit, its last slot becomes a
  per-block `… +n agents` overflow. Left-click stays the rows' one
  reach gesture; RIGHT-click opens the same per-session menu the bar
  serves (2026-07-28, superseding the 2026-07-25 render-only record —
  agent-menu.sh `row` mode resolves through @vibe_sidebar_map), where
  dead rows get their `dismiss` (✗ is a record; seen means clearable)
  and live rows get stop/open. A dead viewer-less row's LEFT-click
  degrades to the project switch — its old attach spawn refused dead
  sessions by design and minted corpse windows.
- The **footer hint row** owns the last row: dim
  `C-Space · Space palette`, truncated to the text budget, render-only
  (no click target — the palette's mouse doors are the tray cells).
  It exists for the cold start: the cheatsheet only appears once the
  prefix is already known.
- The **engine-facts display form** (`vibe _sidebar`, views.go): ONE
  compact line of ` · `-joined segments for the meta line — per
  container its bare role when nominal (absence of a glyph IS the
  nominal signal; the sidebar's ● belongs to agents alone), `◐ role`
  stale / `○ role` stopped otherwise, the engine version riding the
  first segment (`dev-` hashes stripped of the prefix and cut to 8:
  `dev 9766b8d8`; release versions as-is), then `▲n` pending. This
  supersedes the multi-line detail block with its `● dev · hash` rows
  (2026-07-26, same dogfood as the meta line: the detail's ● read as
  an agent named "dev"), which itself superseded the mode+version
  header line and the `%-12s`-padded state words.
- Budgets derive from pane width: text budget is `width−3` (floor 8);
  nested agent rows spend the gutter bar + indent + dot (`budget−4`,
  floor 8) with the dim model suffix dropped first when the name and
  model can't share the line. The gutter is 2 cols (blank, then the bar),
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
the signal filter, the gutter bars, the spin cells' coordinates and
clip guard, and the ghost cells' format
escaping in `internal/tmuxui/frame_test.go`, the porcelain round trips
(`_fleet`, `_agents`, the frame's tmux records) beside them, and the
user-conf epilogue in `internal/app/tui_test.go`. The manual check that
has caught what tests miss: resize the sidebar and click every row type
— the click-skew regression class; with the nested rows that now
includes a ghost row (it must open a viewer, never start an agent).
The chooser's reach-vs-launch verdicts and porcelain live in
`internal/tmuxui/chooser_test.go` and the manifest/cache join in
`internal/app/chooser_test.go` (a running entry must reach, never
double-launch); the manual mouse check is opening the chooser by
CLICKING `+`, MOVING the pointer (the menu must survive — the
`-M`-without-`-O` regression class), and clicking an item — provable
headlessly with a PTY that injects SGR mouse sequences into an
attached scratch client (the 2026-07-26 rig: press+release on the
range cell, bare-motion events (button 35) outside the box, a hover
motion onto the item row, press+release there, then assert the item's
window exists AND carries the `@vibe_session` birth stamp; a second
pass clicks a `ghost-N` cell and asserts the `@vibe_ghost_map`
resolution). For the editor popups: with the
container stopped, `prefix+f/g` must hold the popup open with the
`vibe up` hint, never flash-and-close; and the parser layer's proof
is a `vibe rebuild` (the headless nvim-treesitter install is the one
build step this repo's tests cannot execute).

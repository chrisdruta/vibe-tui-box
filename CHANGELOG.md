# Changelog

Projects pin a release artifact by digest; tags mark intentional upgrade
points (see [docs/installation.md](docs/installation.md)).

## Unreleased — v2 cutover

**BREAKING: the bash/compose harness is gone; `vibe` is one compiled Go
binary.** Clean slate, no migration: v1 installs reinstall
([docs/installation.md](docs/installation.md)), projects `vibe init` fresh.
The v1 line and its history remain in git up to the cutover commit.

- Removed: the root `vibe` launcher, `install.sh`, `verify.sh`, `src/`
  (Dockerfile, compose base, host + container scripts, templates), the
  `examples/` compose presets, and the `.vibe/harness` submodule
  distribution. Presets now ship inside the binary (`payload/presets/`,
  seeded by `vibe init`).
- Replaced: project configuration is one closed `.vibe/vibe.yaml`
  ([docs/configuration.md](docs/configuration.md)) — no user-authored
  compose, no compose scanner; the engine compiles a canonical plan and
  drives the Docker API directly, reconciling containers by candidate
  digest.
- New: `image.agents` / `image.toolchains` bake into images. The engine
  generates an install Dockerfile from its closed recipe set (the v1
  image ARGs' successor: pins move with engine releases; claude and grok
  track their installers' stable channel), builds a per-project tools
  image on the digest-pinned base, and runs the dev container from it.
  An enabled extension now builds FROM the tools image instead of the
  base. Engine-authored, so no per-digest approval — no project bytes
  enter the build; the manifest only selects recipes. The dogfood
  manifest drops its hand-rolled claude-install extension Dockerfile.
- Restored: **in-container GitHub push** (2026-07-29 — the v1 gh
  wiring had silently died in the cutover). `gh` rides every agent
  image as a pinned release artifact; `GH_CONFIG_DIR` points into the
  agent-state volume so a per-project fine-grained PAT pasted into
  `gh auth login` survives rebuilds; post-start wires gh as git's
  credential helper and rewrites SSH remotes to HTTPS in the
  container-local gitconfig. One deliberate delta from v1: the wiring
  is no longer login-gated — this container has no SSH keys and no
  host gitconfig (v1's reason to wait for the opt-in), so pre-login
  push now asks for `gh auth login` instead of dying on publickey.
  See [docs/configuration.md](docs/configuration.md) "GitHub access".
- New: **container deaths reach the sidebar in seconds** (2026-08-04,
  docs/tui-layout.md "The watch channel", the container-level half).
  The watch daemon (`vibe _watch`) also subscribes to the docker event
  stream — server-filtered to managed-label containers and lifecycle
  actions, never the `exec_*` chatter its own fetches generate — and
  bumps `@vibe_engine_serial` on any event, so every sidebar refetches
  fleet+agents+detail on its next 2s tick. Out-of-band deaths (docker
  stop, OOM, a crashing sidecar) used to ride the 30s slow tick.
  `dockerapi.Client` gains the narrow `Events` stream; same
  accelerator-never-dependency contract as the sentinel stream
  (coalesced bumps, capped-backoff resubscribe, slow tick as fallback).
  The daemon also learned to retire itself when the host shim stops
  resolving to its own binary — a resident process must die to be
  replaced (its flock blocks any successor), so a dev sync now reaches
  the daemon within a slow tick instead of waiting for a tui restart.
- New: **the services tree folds** (2026-08-02, docs/tui-layout.md
  "The tree folds"). LEFT on a block's `services` header toggles a
  per-session fold (`@vibe_svc_fold`), collapsing the group to its
  counted header — `services · 3 ▸`. Folded entries leave the layout
  AND the overflow math, so a crowded block gets its agent rows back;
  a fold hiding a signal row (dead service, stale sidecar) renders the
  header bright — the ✗ can leave the pane but never the glance. The
  frame's S record grew an optional trailing flag; an older sidebar.sh
  simply never folds.
- Fixed: **binary handoffs now land on a running tui** (2026-08-02 —
  a dev sync landed and NOTHING in the live UI changed). Every shim
  repoint (`dev on`/`sync`, `dev off`, `update` with a pinned project)
  re-materializes the conf and restamps `@vibe_exe` (the symlink,
  never a resolved digest path) and `@vibe_payload_dir` on the live
  server before the serial bump, so engine calls and click-time script
  resolution follow immediately and sidebar render loops self-exec the
  new script on payload-dir drift. Fire-and-forget: a dead server
  never fails the sync that did the real work.
- New: **per-project egress visibility** (R6, 2026-07-31). Every
  provisioned project's plan synthesizes a dns ledger sidecar
  (`vibe-<id>-svc-dns`: digest-pinned CoreDNS, engine-authored Corefile
  from the artifact payload, bounded query log) that the dev
  container's resolver rides — the log IS the project's domain ledger,
  read raw via `vibe logs dns` or joined with a pure-/proc live-socket
  sample in the tui palette's "network egress" popup (prefix+E, hidden
  `_egress` porcelain — no new public verb). Opt out per project with
  `runtime.egress: off`; `dns` joins the reserved service names (a
  manifest with a user `services.dns` now fails validation). Visibility
  only: direct-to-IP and DoH bypass the ledger by design
  ([docs/security.md](docs/security.md) "Egress"). The sidecar is the
  one closed-policy exception: it runs as root with exactly
  `NET_BIND_SERVICE` re-granted (the CoreDNS binary's file capabilities
  cannot exec against an empty permitted set — found live 2026-07-31),
  inexpressible from the manifest and validation-scoped to that one
  container. Note: every
  provisioned project's candidate hash changes, so the first `vibe up`
  after this upgrade replaces containers once.
- New: `--refresh-agents` on `vibe up` / `vibe rebuild` re-pulls the
  channel-tracking agents to latest. The agent installers otherwise freeze at whatever
  the Docker layer cache captured on the first build, so a plain rebuild
  keeps yesterday's Claude; the flag weaves a per-refresh cache-buster
  into just the claude/codex/grok layers (codex floats to its `latest`
  npm dist-tag), rebuilds them, and persists the refresh generation on
  the project record so later plain rebuilds stay on the fresh build
  (warm-cached) instead of reverting. The pinned system toolchains
  (Go/Node/apt) sit in earlier layers and never rebuild.
- Changed: **the agent refresh token now means the resolved upstream
  version** (wanted and shipped 2026-07-29, the saturated-link
  dogfood). `vibe rebuild` used to bust the channel-tracking agent
  layers with a fresh timestamp — re-downloading identical
  claude/codex builds when upstream hadn't moved. The engine now
  probes each channel's own release endpoint at rebuild time (the same
  files the installers resolve through: claude/grok's bare version
  files, codex's npm dist-tags; one small GET apiece, 10s timeout) and
  mints `AgentRefresh` as a per-agent fingerprint
  (`claude=2.1.220 codex=0.146.0`): unmoved upstream → identical token
  → warm Docker layer cache, zero downloads; a real bump re-pulls
  exactly that agent. Cache-busters went PER-AGENT
  (`VIBE_AGENT_REFRESH_CLAUDE`/`_CODEX`/`_GROK`, each declared
  directly above its own layer — placement is load-bearing under the
  classic builder's declaration-onwards busting) and claude moved to
  the END of the agent chain, so the near-daily claude release stops
  re-pulling its neighbors (one-time layer re-pull on the first
  rebuild after this reorder). Every failure mode — no prober,
  unreadable manifest, dead endpoint, non-version content — degrades
  to the timestamp fallback that busts like before: staleness never
  hides behind a failed probe, and probed values pass the same
  shell-inert charset manifest pins do before touching a build arg.
  Legacy bare-timestamp records keep busting until their next rebuild
  re-mints. Also fixed in passing: unversioned grok's BoM row now says
  `stable channel` — its installer's actual default — not `latest`.
- New: **the working dot spins** (docs/tui-layout.md "The working
  spinner", 2026-07-29). While an agent works, its dot animates — a
  braille orbit that is presentation of the state, never a state
  glyph: any surface without a live animator falls back to the static
  ●. The tray rides one option: spin.sh (one per server, noclobber
  lock beside the socket) rotates `@vibe_spin` at 4Hz while anything
  works, and the design leans on a measurement, not a hope — on the
  pinned 3.7b a bare `set -g` redraws the status line by itself
  (~350 bytes, status only), so the tab dots and working ghost cells
  animate with zero new `#()` splices and no `status-interval`
  change. The sidebar animates its own pane: `vibe _frame --spin`
  reports the drawn working dots' coordinates (a flag-gated fifth
  protocol line) and the render loop sub-divides its 2s tick into
  four 500ms repaints of exactly those cells — pure printf, no tmux
  and no engine per animation frame. The pane-border dot deliberately
  stays static (the same measurement: borders don't repaint on option
  writes, and a frozen frame would read as broken), and everything
  costs nothing while no agent works.
- Changed: **the sidebar's polish pass, first round** (docs/tui-layout.md,
  2026-07-29 screenshot dogfood). The meta line wraps at segment
  boundaries onto continuation rows when it overflows the text budget
  — the raw character clip's mid-segment `dev …` hid exactly the
  engine facts the line exists to show; one line stays the common
  case, and a quiet block pays nothing. And roster ages floor at
  `<1m`: exact seconds shimmered across near-identical rows on every
  forced frame while overstating the 10s redraw cadence. `vibe ps`
  keeps exact seconds — a snapshot is accurate the moment it prints.
  Second round: the three conf sites that hardcoded `#ffffff` (the
  current tab, menu selection, copy mode) now read a new `bright`
  palette entry — theming stays total: no rendering carries a literal
  color outside theme.go, and a future palette shift can't strand a
  stray white. Third round: the canvas is pinned —
  `window-style "bg=#{@thm_bg}"` defaults every pane to the palette
  background and `popup-style` matches, so the chrome's surface insets
  and dim ramps render on the bg they were mixed against instead of
  whatever the emulator carries (the dogfood host's purple scheme left
  them floating). Pane foreground stays the terminal's — inner apps
  own their text — and the user conf overrides both. Fourth round: the
  ▤ dock cell moves to the bar's left cluster beside the brand —
  spliced into the absolute-centred winlist it floated with the tabs,
  a global chrome control reading as part of the window list, while
  the spec's mockup and segment table always drew it at left; outside
  the prefix swap it also stays put while the cheatsheet replaces the
  middle.
- New: **the sidebar learns to tell time — the signal-density pass**
  (docs/tui-layout.md "Signal density", designed and shipped
  2026-07-29). Six glance signals, all inline (one line per entry
  stays law), no new polling and no new container round-trips: every
  roster row wears a right-aligned dim age meaning time IN STATE
  (state-render.sh stamps `@vibe_state_epoch` only on transition;
  cache rows ride the epoch agent-ps.sh always emitted, now threaded
  through the `_agents` grammar — bumped to v2 with epoch + detail);
  `attention` and `exited` rows put the state word in the model's
  slot (the dot's color is no longer the whole message — glance
  ambiguity, color-blindness), with `exit RC` forensics on ✗ rows;
  grouped headers carry dim counts (`agents · 2`) and the `… +n`
  overflow folds DIM rows first, so a signal row is never the hidden
  one; the branch line wears churn (`⎇ main  +128 −40`, one
  `git diff --shortstat HEAD` per in-use project on the fetch path,
  `_fleet` grammar v2); and a height-gated second footer row surfaces
  the review-stack keys (`f files · g git · v clip`) whenever the
  pane has slack. Fixed along the way: the services pass of
  agent-ps.sh claimed an epoch it never emitted — it now derives one
  (/proc starttime while running, `#{window_activity}` once dead), so
  `vibe ps` service rows grow ages too.
- New: **workspace services join the TUI** (docs/tui-layout.md
  "Workspace services") — the svc.sh windows post-start hooks stand up
  are no longer attach-only-invisible. `vibe ps` grows a services
  section; the sidebar gives each window a roster row (dim while
  running, bright ✗ on death) — blocks with services group under dim
  `agents` / `services` headers with `├`/`└` tree connectors (second
  dogfood; agents-only blocks keep the flat form) — with left-click
  reach into the services
  viewer at the clicked window — `vibe attach SESSION [WINDOW]` on a
  fresh spawn, a backgrounded `vibe _svcselect` re-aim when the shared
  viewer is already open (the first dogfood caught the gap same day:
  without it every click read as "goes to the first service") — and
  right-click stop/dismiss (`vibe _svcstop` → agent-session.sh
  `svc-kill`). svc.sh now keeps corpses: remain-on-exit is set before
  the command runs (placeholder → option → respawn), so a crashed
  service stays visible with its crash log in the scrollback until
  dismissed. No state records, no title channel — a service window is
  a pane on the inner server, so `#{pane_dead_status}` carries the
  exit code agents never get. Terminology split in the docs: sidecars
  (manifest `services:`) vs workspace services (svc windows). The
  tray and chooser deliberately stay agent-only.
- Changed: **approve and reject take the request ID** — `vibe request
  approve add-port` resolves through the host-owned pending record,
  whose id→digest binding was frozen at poll time; retyping the 64-char
  digest was friction, not safety, and the confirmation still shows the
  digest. `--digest sha256:…` remains the explicit/scripted form
  (2026-07-28, the R2 approval-ergonomics item).
- Removed: **the `harness:` manifest field** — required,
  shape-validated, and consumed by nothing; which engine runs a project
  is the host registry's artifact pin. Removed from schema, presets,
  and docs while schema changes are still free (2026-07-28, the R1
  schema call); manifests still carrying the line fail the closed
  unknown-key check — delete the line.
- New: **the sidebar speaks right-click too** — agent rows answer
  with the same per-session menu as the bar (one definition,
  agent-menu.sh, resolved through the click map): live rows get
  open/stop, viewer rows stop-and-bury, and dead ✗ rows get
  **dismiss** — a new `vibe _dismiss` → agent-session.sh `dismiss`
  clears the state record behind the glance (refused while the
  session runs; launch-again stays the chooser's door). Supersedes
  the 2026-07-25 "roster stays render-only" record — its grounds
  moved when right-click became the TUI's own agent vocabulary and
  menu-stops started burying their viewers. Right-click elsewhere on
  panes: mouse-mode apps get the event through (the inner tmux),
  other panes a trimmed stock pane menu, and the sidebar never shows
  the stock menu whose Kill ate sidebars.
- New: **the tui heals itself on attach** — `vibe tui` joining an
  already-running server now re-sources the freshly materialized conf
  (tmux applies `-f` only at server start, so every dev cycle used to
  leave stale bindings until a manual `prefix+R` — the 2026-07-28
  dogfood, twice in one evening), and the sidebar render loops
  self-upgrade on their slow tick: when `@vibe_payload_dir` drifts
  from the copy a loop was started from, it execs the current
  artifact's script in place — same pane, new bytes. The generated
  conf is reload-idempotent by its own prefix+R contract (`-o`
  option defaults), which is what makes the heal safe.
- Changed: **the refusal-button sweep reached the sidebar** — a dead
  viewer-less agent row keeps its ✗ signal but its click degrades to
  the project slop instead of spawning an attach that refuses dead
  sessions (and minting a corpse window in the process); a dead row
  with a real crash-corpse window still jumps to it. The palette's
  stop/restart items now say "default agent" — the labels were
  silently default-only all along; per-session stop is right-click's
  job, launch variants the chooser's.
- New: **right-click stops agents from the bar** — an agent viewer
  tab or a tray ghost cell answers right-click with a per-session
  menu: stop agent (ends the SESSION — the stock menu's Kill only
  ever closed the viewer while the agent lived on in the roster),
  open viewer for ghosts, close-viewer-only for tabs. Stop travels
  address-direct: `vibe _stop ADDRESS` → agent-session.sh's new
  `kill` mode (attach's precedent — no flag reverse-mapping),
  idempotent, death recorded by the run-mode EXIT trap as ever.
  Non-agent window tabs keep a trimmed copy of the stock tmux window
  menu on the press, exactly where it always was. A tab's stop buries
  its own viewer window (the death record rides the pane's title
  channel the stop just killed, so the corpse could never self-clean),
  and the bar now draws the dead/live line everywhere: ghost cells
  render only LIVE sessions — dead stays on the signal surfaces, the
  sidebar's ✗ row and the chooser's launch-again entry. Crash corpses
  keep their corpse-with-hints fate: unexplained deaths stay visible.
- New: **cold agents are launchable from the tui** — the agents
  chooser (`+` cell, palette "agents") grows a `:cold` block after
  the warm entries: one per CLI that `vibe agent --cold` can start
  without repo instruction files (claude/codex; grok stays out until
  agent-session.sh knows its skip flag). Same reach-vs-launch
  verdicts on the `-cold` address — a live cold session attaches or
  jumps, never doubles — via the new `launchc`/`launchac` dispatch
  verbs. Previously cold sessions were reachable everywhere but
  launchable only by typing the verb in a shell.
- New: **the preview window — ctrl+clicked images render in the tui**
  (docs/tui-layout.md "Preview window"). One reusable `@vibe_view`
  window per project session (respawned per click, named `⌗ filename`)
  runs show-image.sh over `vibe exec`; chafa — back in the tools image
  as v1's exact pinned source build, the image half of the review
  stack — encodes to sixel and the HOST tmux ingests it natively (the
  v1 nesting lesson, minus the nesting: a host pane is one tmux
  layer). Fidelity gates at click time, loudly: sixel needs host tmux
  >= 3.7 (older drops the raster on adjacent-pane redraws — the
  sidebar tick) plus negotiated sixel; anything less renders chafa
  symbols under an inverse-video low-fi header, and `vibe doctor`'s
  tmux check now grades the same floor (new `Version` on the tmux
  client feeds it). show-image.sh re-renders on SIGWINCH — resize
  clears sixel on every tmux (upstream reflow), so sidebar/dock
  toggles self-heal. Any key closes. Rebuild required for chafa.
  Renders are byte-budgeted: tmux discards any sixel DCS over its 1MB
  input buffer whole (size-ladder bisected 2026-07-27: 827KB shows,
  1.27MB vanishes), so show-image.sh encodes to scratch and shrinks
  until the emit fits with headroom. Dogfood-confirmed end to end
  2026-07-27: clip and workspace images render sixel, sidebar toggle
  and pane resize repaint in place, the view window respawns rather
  than stacking tabs.
- New: **ctrl+click follows the path under the pointer** — anywhere in
  the tui, every pane. open-path.sh reads the clicked line back via
  capture-pane (`#{mouse_word}` arrives as fragments: the 3.7 default
  word-separators split on `./:-`), walks a path out around the mouse
  column with an optional `:line`/`:line:col` suffix, maps
  host-workspace prefixes onto the container's `/workspace`, verifies
  existence over `vibe exec`, and opens text in the review stack's
  nvim popup (review.sh/edit.sh gain a `file` mode — same chrome,
  `+line` jump). Image extensions route to the preview window (entry
  above). Absolute paths outside the workspace resolve in the
  CONTAINER first (`vibe clip`'s /tmp drops preview like anything
  else); prose, unresolvable words, and `~` paths no-op silently, and
  a host-only path gets a message instead of a wrong open.
- New: `vibe agent --stop` / `--restart` end or replace the persistent
  agent session (combine with `-s`/`-a`/`--cold` to address a variant) —
  the lifecycle affordance persistence lacked: the only stops were
  exiting the CLI by hand or `vibe down`. The TUI palette gains matching
  "stop agent" / "restart agent" entries, and the quit prompts now say
  how to stop an agent instead of only that it keeps running. Reattaching
  a session that runs a different agent than requested (`agent.cmd` is
  read live, so flipping it never touched the running session) now
  prompts to restart instead of silently delivering the old agent.
- New: displays show truth, addresses stay stable. The title channel
  gained `cmd` and `model` fields: the host renames the agent's viewer
  window to the CLI actually running (`claude`, `codex:review` — tabs,
  pane border, and sidebar roster follow), the sidebar shows the
  statusline-reported model as a dim suffix, and `vibe ps` carries both
  in its detail column. Session names (`agent`, `agent-review`) remain
  the stop/-s/-a addresses.
- Changed: tmux in the tools image is the engine-pinned 3.7b source
  build with `--enable-sixel` (v1's exact version + checksum) instead
  of distro tmux. The v2 cutover had silently regressed the pin,
  reintroducing the sixel-drop-on-redraw v1 recorded (bookworm ships
  3.3a) and splitting container tmux semantics from the host's. The
  carrier probe accepts both the pinned and the old apt path, so
  pre-pin images keep working until their next rebuild.
- Fixed: agent-pane flicker under the pinned tmux (ghost cursors during
  streaming — a known Claude-Code-in-tmux class: 3.7b advertises
  synchronized updates to the agent, but each tmux hop only EMULATES
  sync, and the chain outward was never declared). Now every hop is
  real: the engine forwards the caller's TERM through interactive
  container execs (docker defaulted the pty to bare "xterm", which
  negotiated everything except sync), the inner server declares
  tmux*:RGB/sync/extkeys for its host-pane terminal, and the host conf
  declares xterm*:sync toward the outer emulator. Escape hatches if a
  CLI still misdetects: /tui fullscreen or CLAUDE_CODE_NO_FLICKER=1.
- New: the bar gained a top border — a dim rule line (status 2), the
  boundary between pane content and the tray the dock strip alone
  didn't provide.
- New: the bar is a bottom system tray. `🥡 vibe` is a clickable start
  button (palette; the `+` cell too — one definition in
  `scripts/palette.sh` behind key and clicks), window tabs show dot +
  name (indexes dropped from display; `M-1..9` still navigate), the
  right side is the clickable engine state plus a clock, and holding
  the prefix swaps the tabs for a keybind cheatsheet in place — no
  second row. Project identity moved out of the bar entirely: the
  sidebar and the OS window title (display name, not the raw session
  ID) carry it. The sidebar's agent roster starts at the pane midpoint
  (projects top half, agents bottom half) instead of hugging the
  bottom, renders fleet-style two-line entries (name over a dim
  model · project detail — no more single-line width fight), and wears
  a ruled header matching the pane border's "projects" title.
- New: claude's harness wiring ships as a **vibe plugin**
  (`payload/container/claude-plugin/`, loaded per session with
  `--plugin-dir` from the read-only payload — never installed, nothing
  lands on the agent-state volume): the agent-state hooks, the subagent
  statusline, and `/vibe:request`, which authors a well-formed rebuild
  request from inside the agent. The `--settings` file shrinks to the
  keys a plugin cannot express (`statusLine`, `autoMemoryEnabled`,
  `autoUpdates`, `sandbox`).
- New: the TUI owns the daily cycle. `vibe tui` first starts the
  project's **approved** candidate when its containers are not running
  (no input freeze, no approval movement — changed inputs still take a
  deliberate `vibe up`), instead of racing an instantly-dying agent pane
  into `no server running`. `vibe down` also closes the project's UI
  session, and the palette gains "park project (down + quit)" — evening
  parks it, morning is `vibe tui` alone. The UI server sets
  `remain-on-exit failed`, so a pane whose command dies keeps its error
  text readable (and the pane-died respawn hint, previously dead code
  without user opt-in, actually fires) while clean exits still close.
- Changed: claude's in-container self-updater is disabled
  (`DISABLE_AUTOUPDATER=1` in the dev container env plus `autoUpdates:
  false` in the payload settings). Self-updates landed in the container's
  writable layer and silently reverted on the next replace; the image is
  now the only version authority, moved exactly by `--refresh-agents`.
  Plan change: claude projects compile a new candidate on their next
  `vibe up`, which replaces containers once.
- New: claude logins persist across rebuilds. With `claude` in
  `image.agents` the plan sets `CLAUDE_CONFIG_DIR` to the agent-state
  volume, and the generated tools image bakes the mount point
  vscode-owned so fresh volumes are writable by the agent.
- New: the v1 tmux TUI is back. `vibe tui` runs on a dedicated
  `vibe-engine` socket, loading the v1 conf (theme, C-Space prefix,
  palette, mouse, status formats) ported to `payload/host/tmux-tui.conf`
  and materialized from the pinned artifact with stamped paths. The v1
  host scripts ride along as payload bash under `payload/host/scripts/`
  — project sidebar (prefix+b), host dock (prefix+t), clipboard image →
  agent prompt (prefix+v) — reached via `@vibe_payload_dir`, keeping the
  v1 rule that the host executes only store-owned bytes. The palette
  gains a broker `requests` entry.
- New: the agent session layer
  (as-built: [docs/architecture.md](docs/architecture.md), agent
  sessions; design record in git history)
  restores the two v1 properties the cutover dropped. Persistence:
  `vibe agent` runs the CLI inside a container-side tmux session
  (`agent-session.sh`) that survives its viewer — killing the pane, the
  tui, or the terminal no longer kills the conversation, and rerunning
  `vibe agent` (or prefix+r respawn) reattaches; the command grows
  `--cold`, `-a/--agent`, `-s/--session`, and container execs default
  to `/workspace` instead of the image WORKDIR. State channel: Claude
  Code hooks feed working/attention/idle/exited through the pane-title
  channel into the tab/border/sidebar dots and the attention flash,
  with exit records and a frontend-dead corpse mark. Roster: `vibe ps`
  additionally lists the current project's agent sessions
  (engine-rendered; container-controlled bytes are sanitized), the tui
  reaps ghost inner viewers after a quit, and v1's
  statusline/subagent-statusline glue returns via `claude --settings`.
- Changed: `vibe tui` stamps each session with `@vibe_project` (the
  full project ID; session names carry a truncated one) so host
  scripts can address the engine renderers per session. The unconsumed
  `_statusline` renderer is removed — the container-side Claude
  statusline (`claude --settings` glue) won that seat; `_sidebar`,
  `_state`, and `_fleet` remain.
- New: project lifecycle hooks and the services session (roadmap R5,
  2026-07-24). The engine runs `.vibe/hooks/post-create.sh` (once per
  container, marker-guarded so a failed first run self-heals) and
  `post-start.sh` (after every actual start) inside the container after
  reconcile — workload trust, container user, `/workspace` cwd, no env
  file; a failing hook fails `vibe up` before the approved pointer
  moves. Post-start hooks stand up long-running processes as windows in
  the in-container `services` tmux session via the idempotent payload
  helper `svc.sh`; `vibe attach [SESSION]` (agent-session.sh's attach
  mode) is the door in. Also new: `vibe logs [SERVICE] [-f] [--tail N]`
  streams dev-container or sidecar logs without raw docker.
- New: presets `go`, `node`, `bun`, and `playwright` (extension
  Dockerfile + browser-install hook sample) beside `minimal`; every
  preset now also seeds a shared overlay — `.vibe/AGENTS.md` teaching
  agents the environment and the rebuild-request protocol, plus inert
  `.vibe/hooks/*.sample` templates. All presets render and
  schema-validate in tests.
- New: engine hardening (roadmap R4, 2026-07-24). `vibe gc [--dry-run]
  [--min-age DUR]` — the store's only deletion path — removes
  unreferenced artifacts/candidates/snapshots (never registry pins,
  approved or pending-bound candidates, live leases, or young objects)
  plus stale staging, superseded `bin/` copies, and forgotten projects'
  broker/approval/dev state. `vibe request show` and the approve
  confirmation now render a bounded, sanitized plan diff (approved →
  pending candidate) computed from the immutable candidates — the
  trusted half of the decision beside the agent's untrusted summary.
  Native fuzz targets cover schema load, envfile, digest/ID parsers,
  request JSON, and the terminal encoder/diff, with a CI fuzz-burst
  job; the first run caught a width-underflow crash in the diff
  renderer (fixed; crasher committed as a regression seed).
- New: the sidebar consumes the engine renderers (2026-07-24) —
  `_fleet` became a US-separated porcelain and `_sidebar` a detail
  block; state-mutating commands bump `@vibe_engine_serial`, and the
  sidebar fetches engine truth (stale/stopped vs approved, pending
  requests, dev marker, cold projects) in a double-forked background
  cache that never touches the 2s frame and degrades to the shell-only
  view without the engine. CI ShellCheck now covers the host scripts.
- New: TUI layout pass (2026-07-24, spec-first —
  [docs/tui-layout.md](docs/tui-layout.md)): `_state` renders display
  form (no more protocol prefix in the status bar), `@vibe_dock_size`
  and `@vibe_engine_refresh` knobs, width-derived sidebar truncation,
  and `~/.config/vibe/tui.conf` sourced last as the sanctioned
  customization point. Palette + glyph maps single-source from
  `internal/tmuxui/theme.go`; payload generation renders `theme.sh`
  and the conf's `@thm` block, so the drift gate catches palette skew.
- New: gopls-lsp plugin auto-install (2026-07-24) — containers shipping
  `gopls` beside claude get Claude Code's official Go LSP plugin
  installed+enabled at user scope in post-create (marketplace add
  first: a fresh config dir knows no marketplaces; both steps verified
  idempotent), so Go code intelligence works without the
  recommendation popup gating each fresh container.
- Changed: presets seed `agents: [claude, codex]` (2026-07-24) — both
  agents by default in every preset and the dogfood manifest; codex
  drags the node toolchain in automatically. Trim to `[claude]` in
  `.vibe/vibe.yaml` if you don't want the second agent baked.
- Fix: the no-nested-sandbox posture is back (2026-07-24) — v1 encoded
  it (`997336b`), the cutover dropped it, and raw codex died at its
  first shell command again: `cap_drop ALL` + `no-new-privileges`
  denies the user namespace codex's bwrap+seccomp sandbox needs.
  Post-create now re-seeds `sandbox_mode = "danger-full-access"` into
  `$CODEX_HOME/config.toml` (key-absent only, prepended, 0600 — the
  documented mode for externally sandboxed environments), restores the
  Claude `/sandbox` degrade block in the payload settings, and — new
  territory v1 never had — rewrites the codex Claude-plugin's pinned
  per-thread sandbox modes (its app-server API ignores config.toml) to
  full access, exact-matched against plugin v1.0.6, re-applied each up
  so a plugin update heals, no-op on unmatched versions.
  docs/security.md regains the "Inner agent sandboxes" section.
- Fix: codex logins now actually persist (2026-07-24) — with `codex` in
  `image.agents` the plan sets `CODEX_HOME` to the agent-state volume,
  matching what the docs promised and what claude already did via
  `CLAUDE_CONFIG_DIR`. Previously `~/.codex/auth.json` sat on the
  container's writable layer and died with every rebuild.
- New: the codex second-opinion plugin auto-install is back
  (2026-07-24, v1 parity) — when claude and codex ship together,
  post-create best-effort installs `openai/codex-plugin-cc` into Claude
  at user scope (`/codex:review`, `/codex:adversarial-review`, …),
  marker-guarded on the agent-state volume so one success persists
  across rebuilds and a failed attempt retries on a later `up`.
- New: opt-in Claude auto memory (2026-07-24) — manifest field
  `agent.memory: auto|off` (default off: a hardened container opts IN
  to cross-session memory, it doesn't inherit Claude Code's on-default).
  The payload settings pin `autoMemoryEnabled` off; `auto` derives a
  flipped settings copy at session start, and the memory directory
  rides the agent-state volume across rebuilds. `vibe init` asks on
  interactive runs (`--auto-memory[=BOOL]` decides it for scripts).
- New: `vibe clip [DIR] [--path-only]` is back (2026-07-24) — the v1
  clipboard-image verb, now a thin engine wrapper over the pinned
  artifact's hardened `clip-image.sh` (the same script behind the tui's
  `prefix+v`). Default mode streams the host clipboard PNG into the
  running dev container's `/tmp`; `DIR` writes through the bind mount
  and needs no daemon. The tmux binding keeps calling the script
  directly, so the two entry points stay version-independent.
- Changed: statuslines render in constant jq (2026-07-24) —
  `statusline.sh` one pass (was six), `subagent-statusline.sh` two
  processes total under its 5s cap (was one plus four per task), output
  byte-identical.
- New: dev-mode ergonomics — `vibe dev on/sync` atomically repoints
  `~/.vibe/bin/vibe` at the fresh dev build (`dev off` hands back to
  the newest release artifact), and dev binaries are stamped
  `dev-src-<digest12>` from the source snapshot so `vibe version` can
  tell builds apart.
- New engine surface ([docs/usage.md](docs/usage.md)): immutable
  content-addressed artifacts/candidates/snapshots under `~/.vibe`;
  `provision`/`update` release flow with streamed checksum verification;
  the rebuild-request broker (`vibe request`) with digest-addressed
  approval; digest-approved image extensions; `vibe dev on/sync/off` for
  engine development; doctor, tmux TUI, and hidden state renderers.
- v1-era docs (architecture, usage, configuration, installation,
  security, extending, updating, services, onboarding, agent-state,
  browser-automation, local-models, roblox) were removed or rewritten for
  v2; the v1 versions live in git history.
- Docs remastered as-built (2026-07-24): `architecture-v2.md` →
  `docs/architecture.md` and `go-engine-design.md` →
  `docs/engine-internals.md` (compacted, mermaid diagrams); the port
  plan, adversarial review, and agent-session design records retired to
  git history with pointers; `ROADMAP.md` added — the sequenced path to
  the first tagged release, **v1.0** (decision: tags continue this
  repo's own `v0.x` line; "v2" stays the architecture generation's name,
  not a tag).
- CI now gates the Go engine: fmt/vet/build/test, golangci-lint,
  payload-manifest drift, ShellCheck on the container payload, and the
  three-platform cross-compile matrix.
- Docs-vs-code reconciliation (2026-07-25), driven by a full audit under
  the rule *out of spec with a design doc = buggy code*. Code moved to
  the documented design: env-file values are now exec-scoped (`vibe
  run`/agent CLI only — no longer baked into the container's ambient
  env at create), entering dev mode asks the documented source-trust
  confirmation (syncs of an already-dev project stay quiet), `vibe
  init` no longer pins a dev-build artifact to a release-mode project,
  grok logins now live on the agent-state volume and survive rebuilds,
  and the palette's clip-image verb passes the right argument again.
  Docs moved to the as-built truth everywhere else, each page now
  declaring its authority (design vs as-built): one normative home per
  invariant (container policy → security.md; Dockerfile contract →
  `builder.ValidateDockerfile`; exit codes → usage.md), the stale TUI
  layout spec rewritten to the `_frame` era, and restated
  code-derivable detail (package glossaries, command listings, dep
  enumerations) cut in favor of pointers.
- Codex sandbox machinery hardened (2026-07-26; clears the two [high]
  findings from the 2026-07-24 adversarial review that blocked v1.0):
  the config seed now recognizes an indented user `sandbox_mode` (no
  more duplicate-key brick), the companion-plugin patch is scoped to
  the openai-codex marketplace tree so an unrelated plugin can never be
  rewritten, and Go fixtures drive both shell functions
  (`internal/payload/agentplugins_test.go` — the script lost its
  trailing `exit` to stay source-able for them). `vibe config` now
  prints a human-readable plan summary; the canonical JSON moved behind
  the standard `--json` flag. Docs: trust-layer diagram atop
  architecture.md, when-the-CLIs-actually-sandbox clarity in
  security.md, and the Claude settings layering (engine pins four keys
  per session; project scope in-repo; user scope persistent on the
  agent-state volume) recorded in configuration.md.
- tui: engine-verb popups actually run the engine now. display-popup
  does not format-expand its shell-command, so `prefix+u/D/p` and the
  tray's request cell handed bash a literal `#{@vibe_exe}` ("command
  not found" in the popup); only the palette door worked, because
  display-menu pre-expands chosen commands. All four now route
  run-shell → the new `scripts/popup.sh` (client + engine path
  expanded where expansion is documented, single-quoted against
  hostile paths), which also single-sources the standard popup chrome
  for the palette's requests/ps/doctor items; fixture-tested via a
  fake tmux (`internal/payload/popup_test.go`).
- tui polish pass (spec-first in docs/tui-layout.md, target frame
  embedded as ASCII): the sidebar's agent roster flows directly after
  the fleet section instead of parking at the pane midpoint, a dim
  `C-Space · Space palette` footer owns the last row, the detail
  block speaks the glyph vocabulary (`● dev · 9766b8d8` /
  `▲ 2 pending` — no more mode/version/role stutter), the tray's
  right segments gained ` · ` separators, the cheatsheet learned
  `z zoom · [ scroll · x close`, and the bar's rule line is generated
  at 1000 cols (clients wider than 400 no longer clip it).
- **New: the bundled review stack** — `prefix+f` (files: nvim + oil,
  the directory fills the window, editing the listing edits the
  filesystem) and `prefix+g` (git: lazygit) as container-side popups
  over `vibe exec`: a cold host needs nothing installed. nvim 0.12.4,
  lazygit 0.63.1, and five plugins at pinned SHAs (mini.nvim, oil,
  gitsigns, tokyonight, nvim-treesitter) bake into the tools image
  with treesitter parsers for 23 languages compiled at build via the
  official tree-sitter 0.25.10 CLI (the 0.26.x prebuilts link glibc
  2.39 — newer than the bookworm base; the docs' "≥ 0.26.1" floor is
  unenforced and `tree-sitter build` is all the installer runs).
  Config lives in the read-only payload — no plugin manager, no
  runtime network, no editor state outside scratch — and theme.lua +
  lazygit's yml are generated from `internal/tmuxui/theme.go`, so the
  TUI and the editors read as one product. Popup borders carry the
  exit hints; `q` quits from anywhere (the lazygit convention).
  Dogfood shaped it same-day: lazygit was promoted to the sole diff
  surface (a trial `prefix+G` diffview popup shipped and retired
  within hours), and mini.files gave way to oil. Your own editor
  stays one `~/.config/vibe/tui.conf` rebind away (recipe in
  usage.md).
- Hot paths went fork-free: statusline.sh (per tick),
  agent-state-hook.sh (per tool use), and subagent-statusline.sh
  dropped `id`/`cat`/`whoami`/`awk`/`tr|head` pipelines for pure-bash
  expansions; the records-dir derivation single-sources from the new
  `payload/container/state-dir.sh`.
- Branch-review remainder closed (all nine): one `AgentMode` replaces
  the stop/restart bool pair (invalid combos unrepresentable;
  agent-session.sh rejects `--restart` in stop mode), `vibe down`
  renders its output before killing the UI session it may be running
  inside (SIGHUP ignored around the kill, so `--json` consumers get
  clean exits), the tui pre-flight dropped its redundant Docker ping,
  bun/rokit layers moved ahead of the agent cache-buster so
  `--refresh-agents` re-runs agents only, and palette.sh builds its
  client flag bash-3.2-safely.
- New: rebuild output is a live bill of materials instead of the raw
  Docker firehose. The engine already knows the tools image's parts
  before the build (`GenerateInstallPlan`: base, tmux, review stack,
  plugins, parsers, toolchains, agents — with their pins), so `vibe
  up`/`rebuild` now draw those rows on stderr and flip each one
  pending → building → cached/built in place, with per-part timing,
  the expected "will rebuild (refresh)" verdict up front, and one
  `built <tag> in 42s · 9 cached · 2 built` summary line. Extension
  builds (no engine plan) grow a row per Dockerfile instruction. Raw
  layer output is suppressed but ring-buffered: a failing step replays
  its last 30 lines under the marked row. `--verbose` (new global
  flag) streams the unabridged build instead; non-TTY stderr stays
  silent as before. Under the hood the classic-builder stream is
  parsed once at the dockerapi seam (`Step N/M`, `---> Using cache`,
  errorDetail) into typed Progress events — the same seam pulls
  already used — and the app layer stamps each step with its BoM part.
  `vibe config` prints the same parts statically as `part` rows, with
  re-pull-on-rebuild verdicts.
- New: the agent surfaces split three ways — the tray is presence, the
  sidebar is signal, `vibe ps` is full truth
  ([docs/tui-layout.md](docs/tui-layout.md) "Agent surfaces"), and no
  agent is drawn twice on one surface. **Tray ghost cells:** a
  container-side agent session with no window on this server renders
  in the winlist as a dim italic cell on the surface color behind a
  hairline inset, its dot carrying real state (an attention coral is
  visible with no window open), each its own `mouse_status_range`;
  clicking one opens a viewer — attach-only (`vibe attach SESSION`:
  never starts, never restarts), after which the ghost graduates to a
  real tab. **Nested sidebar roster:** the `─ agents ─` section folds
  into the fleet blocks as one row per live agent (state dot + the
  CLI actually running + dim model — idle rows render dim, per the
  same-day roster decision below); viewer-less agents get rows whose
  click is the same attach-only spawn.
  Project blocks now sit under 2-col **gutter bars** (coral for the
  session you are in, border-hex for another project in use, none for
  a cold one) and overflow per block (`… +n agents`) instead of one
  fleet-wide count. Both surfaces read ONE join — the frame renderer
  matches the new `vibe _agents` fetch-cache rows against this
  server's windows and publishes the tray's cells as `@vibe_ghosts`,
  so the conf keeps its single `#(vibe _state)` splice and the tray
  and sidebar cannot disagree about what exists. Also: the frame now
  clips at the footer row instead of painting (and click-mapping) rows
  the pane cannot show, `vibe ps` rows carry cli/model as their own
  columns, and a viewer opened from the UI carries `VIBE_NESTED` so it
  is reapable on quit.
- New: the tray's `+` cell opens the **agents chooser** — launch
  what's down, reach what's up
  ([docs/tui-layout.md](docs/tui-layout.md) "Launch surfaces"). One
  state-aware entry per installed CLI (`image.agents`, manifest
  default first), plus the shells: a CLI that is down launches
  (`vibe agent` / `-a KIND`), one that is up shows its recorded-state
  glyph and reaches the existing session (window jump, or the
  attach-only spawn when no viewer exists) — never a second viewer on
  a running session, which is what the old `+`→palette→"agent" path
  silently minted. Verdicts render engine-side (`vibe _chooser`: the
  manifest joined with the same `vibe ps` fetch cache the tray's
  ghost cells read, so the chooser and the tray cannot disagree;
  cache-missing degrades to launch verdicts that `-A` semantics keep
  honest). Menus are now actually mouse-usable: a script-opened menu
  (any `run-shell` → `display-menu`) carries no mouse event and tmux
  marks it NOMOUSE — item clicks were swallowed and any button
  release closed it, which is why clicking palette items never
  worked; `-M` alone then died on the first pointer motion, because
  the menu enables all-motion tracking and a bare motion event is
  release-coded. Both menu scripts now pass `display-menu -M -O`
  (motion hovers and aims, a press fires the aimed item, a press
  outside closes), and the two menu doors dispatch on
  `MouseUp1Status` so the opening click's release is spent before the
  menu exists (both layers established with a PTY mouse-injection rig
  against the pinned tmux, plus `menu.c`). The chooser pins above the
  tray (`-y S`); the palette keeps display-menu's default centered
  position, and the tray's `+` cell gets a default-background spacer
  so it no longer fuses with the last ghost cell. The palette keeps the full command set
  under 🥡 / `prefix+Space`, its bare "agent" item replaced by the
  chooser. Parallel instances of one CLI stay inside the CLI by
  decision record (BACKLOG): "another claude" is Claude Code's own
  background-session manager, not a vibe-minted twin.
- Fixed: a tray ghost-cell click could MINT a junk inner session
  instead of opening a viewer — tmux clips status range names at 15
  bytes (`struct style_range`), so the ghost range `agent-agent-codex`
  dispatched as truncated `agent-cod` and `vibe attach` created a
  bare-shell session by that name. Ghost ranges now carry an index
  (`ghost-N`) resolved through the frame render's new
  `@vibe_ghost_map` session option (session names have no length
  cliff there); `agent-session.sh attach` refuses to create
  agent-convention sessions (attach means attach — `vibe agent` is
  the launch door); and every launch/viewer window is stamped
  `@vibe_session` at birth, so the ghost/chooser dedup no longer
  waits on title events a hookless CLI (codex, a bare shell) never
  sends — the gap that let ghost clicks pile up duplicate viewers.
- New: the **watch channel** — `vibe _watch`, a per-project daemon the
  sidebar loop spawns (flock-singleton beside the cache), holding one
  long-lived exec stream on a container sentinel
  (`agent-watch.sh`) that emits a line when the inner tmux topology or
  the agent state records change. On each event the daemon re-runs the
  agents fetch, atomically replaces the cache the tray/sidebar/chooser
  read, and bumps the frame serial: agent presence now lands in ~1-3s
  instead of the 30s slow-tick poll (which remains as the fallback
  cadence). The sentinel is leashed to the stream by stdin-EOF so
  daemon reconnects never stack orphan pollers
  ([docs/tui-layout.md](docs/tui-layout.md) "The watch channel").
- Changed: each sidebar project block now reads as **identity → meta →
  roster**: the separate branch row and multi-line detail block merge
  into ONE dim meta line (`⎇ main · dev 9766b8d8 · ▲2`), and engine
  facts drop their `●` (a nominal container is its bare role; ◐/○
  still mark stale/stopped) so on the sidebar a `●` row always means
  an agent — the old `● dev · hash` line read as an agent named
  "dev".
- Changed: the sidebar shows the full agent **roster** — every live
  agent gets a row, with idle rows rendered dim (name in the dim
  color, dot keeping its state color) instead of collapsing to a bare
  dot on the project name row (Chris, 2026-07-26: three dogfood
  rounds read that dot as "claude is missing", while a hookless
  `running` codex kept a full row doing nothing). Signal states keep
  the fg color and the attention coral; the name row carries no agent
  dots anymore; viewer-less idle sessions get a row too (attach-only
  spawn as the click, or the stamped-window jump).
- Fixed: the startup agent window is no longer invisible until the
  first message. Two halves: the `@vibe_session` viewer stamp is now a
  SELF-stamp — `vibe agent` / `vibe attach` mark their own window from
  inside the pane (gated on the vibe-engine socket), one definition
  covering every launch door including the tui's own startup window,
  which the per-spawner stamps missed (those are removed from
  chooser.sh/palette.sh/agent-open.sh); and a stamped viewer whose
  cache state is presence-not-signal (a restored `idle` claude that
  has not spoken yet, so no glyph exists) now renders in the sidebar
  from cache truth — as the dim roster row, per the roster entry
  above.
- Fixed: the viewer join now counts birth stamps, not glyphs — the
  frame's viewed-map only counted windows with a state glyph, so a
  hookless CLI's ghost survived its own open viewers (a zombie button
  spawning another `agent-codex` viewer per click) and its sidebar
  row kept offering the spawn. A stamped glyphless window now clears
  the ghost and turns the sidebar row's click into a window jump; the
  chooser likewise jumps to a stamped viewer even when the fetch
  cache is cold (previously that degraded to launch, whose `-A`
  reattach minted a second viewer of the running session — the
  duplicate-claude path).
- Fixed: an all-pinned agent selection no longer sends the
  `VIBE_AGENT_REFRESH` build arg its Dockerfile never declares, which
  drew the daemon's unconsumed-build-arg warning on every build.
- New: **the briefing gets wired** (2026-07-31, closing the backlog's
  root-AGENTS.md-import flag). The seeded `.vibe/AGENTS.md` reached no
  agent mechanically — Claude Code auto-reads only CLAUDE.md, and
  codex reads root AGENTS.md prose but has no import syntax — so
  `vibe init` now also seeds a root AGENTS.md pointer (prose for
  codex, `@.vibe/AGENTS.md` for Claude) and a one-line
  `CLAUDE.md` → `@AGENTS.md` shim. Existing root files are never
  touched: they come back as `kept existing …` notices with the wiring
  hint, and the seeds are engine-owned constants — presets stay
  confined to `.vibe/`. This repo grew its own shim the same day
  (dogfood claude sessions had been flying without the dev guide).
- Changed: **svg joins the preview extensions** (2026-07-31) — and was
  never the recorded one-liner: the pinned chafa source build had no
  librsvg, so "chafa handles it" was true of v1's environment, not
  this build. `librsvg2-dev` joins the chafa layer's codec packages
  and `svg` joins open-path.sh's extension gate; first tools-image
  rebuild picks up the loader.

## v1 final state (unreleased, superseded by the v2 cutover)

The v1 line's post-v0.7.3 re-founding (new engine, new front door, new
host security architecture) was superseded by v2 before it ever released.
Its detailed record was removed from this file 2026-07-28; git history
holds it.

## v0.7.3 — 2026-07-19

- **Changed: image previews render actual pixels where possible.** Small
  `png`/`jpeg`/`gif`/`bmp` images now render through `img2sixel` with
  integer nearest-neighbor upscaling — crisp pixels instead of smooth
  blending, which was exactly wrong for small textures and icons; images
  larger than the pane downscale with lanczos3. `webp`/`avif`/`svg`/`tiff`
  stay on `chafa`. Applies to the preview window, `vibe review` (which now
  probes the host terminal for sixel support and real cell metrics), and
  `vibe show`.
- **Fixed: silent blank previews from lying extensions.** The real format is
  sniffed from magic bytes, never the file name — generated assets are often
  webp bytes named `.jpg`, which previously routed to the wrong decoder and
  rendered nothing.
- **New: render diagnostics.** `vibe show --diag PATH` and the viewer's `d`
  key report sniffed format vs extension, native size, renderer choice, exit
  code, and the renderer's stderr; every render attempt also logs one line
  to a self-truncating debug log
  (`$XDG_RUNTIME_DIR/.vibe-preview-debug.log`).
- **New: shared `preview-lib.sh`** (sniffing / rendering / diagnostics),
  sourced by the viewer and `vibe show`, baked at
  `/usr/local/lib/vibe/preview-lib.sh`. `vibe doctor` now checks
  `chafa`/`img2sixel` and the tmux client's sixel support.
- Default `VIBE_PREVIEW_GLOB` widened to `*.gif *.bmp *.avif` (the glob only
  filters watching; rendering trusts the sniffed format).
- **Rebuild required** (`vibe rebuild`) to bake the lib and the new viewer.

## v0.7.2 — 2026-07-19

- **New: `vibe status` / `vibe down`.** Host-side container lifecycle without
  raw docker incantations: `status` lists this project's container(s) (name,
  state, image, ports); `down` stops & removes the container while leaving
  named volumes (agent state) untouched — `vibe up` recreates it. Both match
  by the devcontainer CLI's `devcontainer.local_folder` label and need a
  docker client on the host.
- **New: `vibe attach [SESSION]`.** Attach (or create) an arbitrary tmux
  session in the container — the door into a long-lived services session a
  project's `project/post-start.sh` stands up (dev servers, watchers, …).
  Session name resolves argument > new `DEV_ATTACH_TMUX_SESSION` config.env
  key (seeded commented-out) > `main`. Replaces per-project attach scripts.

## v0.7.1 — 2026-07-18

- **Changed: the image viewer is passive by default.** Verdict keys and the
  per-image verdict label now exist only when a decisions target is
  configured; without one the viewer just views — the right behavior for the
  everyday case of glancing at a `vibe clip` capture or a prompt paste, where
  "undecided" demanded a decision nobody owed. Review mode activates via
  `VIBE_PREVIEW_DECISIONS` in `config.env` (every instance, including the
  `prefix+i` window) or the new per-batch form below. Rebuild required to
  bake the new viewer.
- **New: `vibe review DIR`** reviews one directory as a batch: watches `DIR`
  (workspace-relative), records verdicts to `DIR/vibe-decisions.jsonl`. Built
  for staged generation pipelines — one directory and one `vibe review` per
  approval gate; stage semantics (regenerate vs refine on reject) stay in the
  project's agent skills.
- **New: reject notes.** In review mode `n`/`x` prompts for an optional
  one-line reason (Enter skips) recorded as a `"note"` field in the verdict
  JSONL — turns reject-and-redo loops from rerolling into steering.
- **Changed: agent onboarding prompt clones fresh.** The paste-prompt in
  [docs/onboarding.md](docs/onboarding.md) no longer looks for (or reuses) a
  local `~/dev` scaffold clone; it always shallow-clones the latest harness
  to a throwaway `/tmp` directory.

## v0.7.0 — 2026-07-18

- **New: image review — `vibe review` and the tmux `preview` window.**
  `scripts/preview-viewer.sh` watches a directory for image batches
  (`VIBE_PREVIEW_DIR` / `VIBE_PREVIEW_GLOB` in `config.env`), renders them
  newest-first with single-key navigation, and appends approve/reject
  verdicts to a JSONL file (`VIBE_PREVIEW_DECISIONS`; append-only, last line
  per path wins) for a pipeline or agent to consume. Run it as `vibe review`
  in any host terminal — chafa renders straight to it, no tmux in the pixel
  path (the reliable mode) — or as a dedicated `preview` tmux window via
  `prefix + i`. Baked into the image as `/usr/local/bin/vibe-preview` —
  rebuild required.
- **Changed: Claude Code image hooks feed the review window** instead of
  popping preview splits — transient splits cannot reliably hold a sixel
  render on tmux 3.5a (client redraws replace images with placeholders;
  passthrough smears next to a busy TUI). The hook ensures the window
  exists (detached, never steals focus) and enqueues the path; the window
  name lights up via `monitor-activity` when unfocused. A prompt paste the
  TUI converts to an `[Image #N]` attachment carries no path in the hook
  payload; the hook falls back to the newest `/tmp/clip-*.png` under 10
  minutes old. `VIBE_PREVIEW_SECONDS` and the 30s debounce are retired.
- **Changed: in-tmux sixel rendering hardened** — the viewer sizes images by
  measuring the emitted sixel raster (chafa's captured-output cell metrics
  are unreliable), centers with margins so the header stays visible, ships
  the image as a self-positioning anchored passthrough envelope, and heals
  redraw-wiped pixels flicker-free a tick later. `vibe show` with no
  argument now also considers the watch directory.

## v0.6.0 — 2026-07-18

- **New: auto image preview in Claude Code sessions** — hooks in
  `templates/claude-settings.json` (`UserPromptSubmit` + `PostToolUse: Read`
  → `scripts/preview-image-hook.sh`) pop a self-closing tmux split whenever
  an image path appears in your prompt or the agent reads an image file
  (focused only for the instant the sixel renders, then focus returns). Tune the duration with `VIBE_PREVIEW_SECONDS` in `config.env`.
  Existing projects adopt the hooks by merging the template block at their
  next pin update.
- **New: `vibe show [PATH]`** — sixel image preview in the terminal, the
  companion to `vibe clip`: with no argument it renders the newest
  `/tmp/clip-*.png` so you can see what an agent is about to look at (agent
  TUIs only show `[Image 1]` placeholders). Also `prefix + i` inside the agent
  tmux session opens the same preview in a transient split pane. Adds `chafa`
  and `libsixel-bin` to the image — rebuild required.

## v0.5.2 — 2026-07-17

- **Fix: `vibe clip` broken on WSL** (v0.5.1 regression) — WSL only shares
  environment variables listed in `WSLENV` with Windows processes, so
  `CLIP_WIN_PATH` (introduced by v0.5.1's injection hardening) was `$null`
  inside `powershell.exe` and the clipboard save crashed — then falsely
  reported success, cascading into a missing-file error. The variable is now
  forwarded via `WSLENV`, the PowerShell step only reports `SAVED` after an
  actual save (real errors are surfaced instead of "No image on the
  clipboard"), and the script verifies the file exists before streaming it
  into the container.

## v0.5.1 — 2026-07-17

- **Security fixes from a code review** (host-boundary hardening):
  - `clip-image.sh` no longer interpolates the destination path into the
    PowerShell or AppleScript it runs — a path containing a quote could break
    out into **host** command execution. The path now travels as an
    environment variable (PowerShell) / run-handler argument (AppleScript).
  - `clip-image.sh` confines workspace-mode writes: the destination is
    resolved with `pwd -P` and rejected if it escapes the real repo root
    (defeating a repo-planted symlink like `.captures -> ../../.ssh`), and an
    existing symlink at the target file is refused.
  - `vibe clip DIR` (workspace mode) no longer auto-starts the container — it
    writes straight to the bind mount, so nothing needs to be running.
  - The agent-command split (`DEV_AGENT_CMD`, `-a`) runs under `set -f`, so a
    value containing `*` can no longer glob-expand repo filenames into agent
    arguments.
  - Launcher symlink resolution replaces GNU-only `readlink -f` with a portable
    loop (restores the stock-macOS bash-3.2 host invariant).
  - post-start's GitHub rewrite now also covers `ssh://git@github.com/` remotes,
    set idempotently (unset-all then add) so restarts don't accumulate values.
  - The `npx @devcontainers/cli` fallback is version-pinned (`@0.87.0`) instead
    of resolving mutable `latest` on the host; override per run with
    `DEVCONTAINER_CLI_SPEC`.
- **Agent-driven update prompt** in [updating.md](docs/updating.md): paste-ready
  prompt that moves the pin, reads the changelog between versions, reconciles
  the project-owned seeded files against the new templates (project values win
  on conflict), and reports what needs a human decision. Companion to the
  onboarding prompt; linked from the README.

## v0.5.0 — 2026-07-17

- **`dev` back-compat shim removed**: `harness/dev` is gone and the seeded
  wrapper execs `harness/vibe` directly. Pre-v0.4.0 installs must replace
  their `.devcontainer/dev` wrapper in the same commit that moves the pin to
  ≥ v0.5.0 — see [updating.md](docs/updating.md) → Crossing the v0.4.0 rename.
- **Login-gated GitHub git wiring**: when (and only when) `gh` is logged in,
  `post-start.sh` wires gh as git's credential helper and rewrites
  `git@github.com:` remotes to HTTPS inside the container — restoring the
  container-local `~/.gitconfig` after every rebuild, so an SSH-cloned repo
  shared with the host stays pushable in-container. The `gh auth login` is the
  opt-in; never logging in leaves git untouched. `vibe doctor` reports the
  state (logged in + wired / not wired / not logged in). configuration.md
  gains a fine-grained-PAT permission quick reference, install.sh prints the
  permission set in its next-steps output, and updating.md documents crossing
  v0.4.0 from older installs (GH_CONFIG_DIR, settings merge, wrapper rename).
  Also: post-start's
  exec-bit self-heal now covers the renamed `vibe` wrapper.

## v0.4.0 — 2026-07-17

- **Per-project `gh` logins**: `GH_CONFIG_DIR` now points into the agent-state
  volume, so `gh auth login` (recommended: paste a per-project fine-grained
  PAT — single repo, Contents read/write, no `workflow` scope) persists across
  rebuilds and stays compartmentalized per project. Host-level `GH_TOKEN`
  forwarding is unchanged but documented as the one-token-everywhere trade;
  `gh auth login` refuses while it is set. See configuration.md → GitHub access.
- **Seeded Claude settings deny `.env` reads**: `Read(./.env)` /
  `Read(./.env.*)` join the sudo/su denies in the seeded
  `.claude/settings.json` — an agent-level guardrail against prompt-injected
  secret reads, not a boundary (see security.md, which also documents the
  `/dev/null`-over-secret-file mount recipe for project secrets agents never
  need). Existing projects keep their own settings file; merge manually.
- **The launcher is now `vibe`** (was `dev`): seeded as `.devcontainer/vibe`,
  real script at `harness/vibe`. `harness/dev` remains as a back-compat shim so
  existing consumer wrappers keep working across a pin bump, and the seeded
  wrapper tries `vibe` then `dev` so it also works against older pins.
  Entries below predate the rename; read their `dev` commands as `vibe`.
- **Global launcher**: `vibe` resolves the target project by walking up from
  the current directory to the nearest `.devcontainer/devcontainer.json`
  (falling back to the project the script lives in) and survives being
  symlinked (`readlink -f`) — one `~/.local/bin/vibe` symlink now serves every
  harness project from any subdirectory. The previously documented host-wide
  symlink was broken.
- **Auto-up**: container commands (`agent`, `shell`, `run`, `exec`, `doctor`,
  `bootstrap`, `clip`) start the container when it isn't running (detected via
  the devcontainer CLI's `devcontainer.local_folder` label, or an exec probe
  when no docker client is present). Start-up progress goes to stderr so
  `vibe run` stdout stays pipeable; a cold `vibe agent` is the whole morning
  routine.
- **Docs: [positioning.md](docs/positioning.md)** — the layer this harness
  occupies vs. agent loops and orchestrator UIs, its principles and non-goals,
  and the recorded decision to keep auth agent-native and per-project (no
  centralized credential store); cross-linked from agent-state and security
  docs.
- **`dev agent --cold`**: fresh-perspective agent session without repo instruction
  files — Claude via `--safe-mode`, Codex via `-c project_doc_max_bytes=0`; agents
  without a known skip mechanism refuse. Cold runs get their own tmux session
  (`<session>-cold`) so they never reattach to a warm one.
- **`dev agent -a/--agent CMD`**: per-invocation agent override (e.g.
  `dev agent -a codex`, composable with `--cold`) without touching
  `DEV_AGENT_CMD`; each override gets its own tmux session (`<session>-codex`).
- **`dev clip [DIR]`**: save the host clipboard image into the container's `/tmp`
  (or a workspace-relative `DIR` to keep it) and put the container path on the
  clipboard — the workaround for Ctrl-V image paste being unreachable from
  inside the container. WSL (PowerShell) and macOS (AppleScript); new host
  helper `scripts/host/clip-image.sh`.
- **Codex plugin for Claude Code auto-installed** when `INSTALL_CODEX=true`:
  post-create adds [openai/codex-plugin-cc](https://github.com/openai/codex-plugin-cc)
  (user scope, persisted in the agent-state volume), giving Claude sessions
  `/codex:review`, `/codex:rescue`, and friends without switching panes. Warns
  instead of failing bootstrap when offline.

## v0.3.0 — 2026-07-12

- **playwright-deps Dev Container Feature** + [browser-automation recipe](docs/browser-automation.md):
  headless Chromium for shell-driven agent browsing (`@playwright/cli`). Feature
  option `version` pins the playwright release that resolves the apt dependency list.
- **`dev agent` can run in a persistent tmux session** (`DEV_AGENT_TMUX`, seeded on
  for new installs; unset = previous behavior). Rerunning attaches; detaching keeps
  the agent alive.
- **Onboarding scaffolding**: seeded `.devcontainer/AGENTS.md` (container rules for
  agents; import via `@.devcontainer/AGENTS.md`), [docs/onboarding.md](docs/onboarding.md)
  with an agent reconcile prompt.
- **Default Claude statusline**: harness-shipped `scripts/statusline.sh` /
  `scripts/subagent-statusline.sh`; `install.sh` seeds `.claude/settings.json`
  (statusline + sudo/su deny) when the project has none.
- **Hardening**: Dockerfile builds with `pipefail` and asserts installed binaries
  (a failed `curl | bash` previously produced a cached layer with the tool
  missing); launcher exec bits are recorded in the git index and self-healed at
  post-start (survives `core.fileMode=false` checkouts).
- verify.sh covers `features/`, seeded files, and index modes; CI workflow added.

## v0.2.0 — 2026-07-12

- macOS support: cross-platform Ollama host helper, arm64-verified image, docs.

## v0.1.0 — 2026-07-12

- Initial release: generic agent dev-container harness (hardened non-root image,
  preset installer, lifecycle scripts, agent-state volume, docs).

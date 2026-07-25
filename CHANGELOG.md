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
- New: `--refresh-agents` on `vibe up` / `vibe rebuild` re-pulls the
  channel-tracking agents to latest. The agent installers otherwise freeze at whatever
  the Docker layer cache captured on the first build, so a plain rebuild
  keeps yesterday's Claude; the flag weaves a per-refresh cache-buster
  into just the claude/codex/grok layers (codex floats to its `latest`
  npm dist-tag), rebuilds them, and persists the refresh generation on
  the project record so later plain rebuilds stay on the fresh build
  (warm-cached) instead of reverting. The pinned system toolchains
  (Go/Node/apt) sit in earlier layers and never rebuild.
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
  window to the CLI actually running (`claude`, `codex·review` — tabs,
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
  bottom.
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

## v1 final state (unreleased, superseded by the v2 cutover)

The delta since v0.7.3 was a re-founding of the v1 line: a new engine, a
new front door, and a new host security architecture; it was superseded
by v2 before release and is retained here for the record. Grouped by
theme, breaking changes first.

- **`INSTALL_GO` image toggle** (default `false`): official Go tarball,
  pinned `GO_VERSION` + per-arch checksum, upstream layout under
  `/usr/local/go`; image `PATH` gains `/usr/local/go/bin` and `~/go/bin`.
  First consumer is the harness itself — the host-engine Go port
  ([docs/go-port-plan.md](docs/go-port-plan.md)) develops inside the
  dogfood container, which flips the toggle on.

- **BREAKING: the devcontainer engine is gone — `vibe` drives docker
  compose + docker exec directly.** Host requirements drop to git + docker
  (no Node, no `@devcontainers/cli`). The container is defined by the
  harness base compose file (workspace mount, agent-state volume,
  hardening, environment, `vibe.project` label) with the project-owned
  `.vibe/compose.yaml` merged on top via `-f` stacking; `vibe config`
  prints the merged result. `vibe up` runs compose and then the lifecycle
  itself: `post-create.sh` once per container, `post-start.sh` on every
  start. Everything exec'd runs through `docker exec` with a real pty.
  - **The consumer layout is now `.vibe/`** (compose.yaml, config.env,
    AGENTS.md, project/ hooks, yazi/, harness submodule).
    `devcontainer.json` is retired: ports are
    compose `ports:` entries (loopback-only policy unchanged), extra
    env/mounts are compose keys, and `updateRemoteUserUID` became an
    explicit `USER_UID` build arg. Migration is one commit
    (docs/updating.md "Migrating to the compose engine"); agent logins
    survive because the state-volume name is unchanged (documented ABI).
    The legacy layout is still recognized well enough to migrate from
    inside it, and `status`/`down` still clean up devcontainer-era
    containers.
  - **Retired:** VS Code `customizations` blocks and per-preset extension
    lists, the `features/` directory, and the `DEVCONTAINER_CLI_SPEC`
    override. CI builds via `./vibe build`.
  - **New: project image extensions replace Dockerfile flag creep.** A
    build-only `base` service produces the shared image; a project needing
    system tooling chains its own `.vibe/Dockerfile`
    (`FROM ${VIBE_BASE_IMAGE}`, ends `USER vscode`) and the launcher
    sequences base → extension builds. Runtime hardening stays
    compose-side, so no extension image can weaken the running container.
    Contract in docs/extending.md; worked examples (playwright, blender)
    in `examples/extensions/`. `INSTALL_PLAYWRIGHT_DEPS`/
    `PLAYWRIGHT_VERSION` moved out of the shared Dockerfile accordingly.
  - **New install UX: submodule-first + interactive.** The recommended
    install is `git submodule add <url> .vibe/harness` then
    `.vibe/harness/install.sh` — everything arrives over git, pinnable and
    diffable. With no arguments on a terminal it interviews (preset,
    `--extras codex,grok,node,playwright`, confirm); any argument keeps
    exact flag behavior for scripts/CI.
  - **Repo reorganized under `src/`** (same breaking release, so the move
    costs consumers nothing): harness internals in `src/*`, entry points
    at the root, and top-level `examples/` holds the exact files each
    preset seeds, kept in lockstep by verify.sh.
  - **Seeded compose: every `INSTALL_*` toggle is a live rendered line**
    (including the previously implicit `INSTALL_CLAUDE_CODE`); flipping
    one is edit-in-place + `vibe rebuild`. The codex⇒Node implication is
    noted inline.
- **BREAKING: host root of trust — host-executed code moved out of the
  workspace bind.** The 2026-07-22 security review showed that any
  host-side execution of container-writable files (the workspace `./vibe`,
  tui hook scripts inside the checkout) lets a compromised container
  process escalate to host code execution. The fix is an architectural
  relocation:
  - **`~/.vibe` store + shim.** `install.sh --self` installs a host store:
    `~/.vibe/bin/vibe` (the ONLY stable host entry point — put it on
    PATH), immutable verified versions under `~/.vibe/versions/<sha>/`
    (whole-tree sha256 manifest checked before every exec), a host-owned
    mirror of the harness repo, and per-project trust records. The
    workspace `./vibe` no longer executes on the host — it survives
    in-container only. First contact with a project prompts to trust its
    pin (TOFU; publisher authenticated against the canonical host-owned
    mirror remote); the root `./vibe` symlink of the interim layout is
    gone (install.sh removes a legacy one). `vibe provision` is the
    non-interactive form for
    CI/cron, `vibe self-update` refreshes the store, and `vibe dev`
    (dev mode) snapshots a working tree — including uncommitted changes —
    into an immutable version for harness development.
  - **The container sees the harness read-only,** overmounted at
    `.vibe/harness` from the store, so in-container agents can read (and
    `./vibe` can run) the harness but can never rewrite what the host
    executes. Container consumers take the project name from
    `$VIBE_PROJECT_NAME` instead of trusting workspace files.
  - **The compose boundary is enforced, not assumed.** Before touching the
    daemon, the launcher snapshots the compose input closure, renders it
    under a sanitized environment, and STRUCTURALLY verifies the rendered
    model: the dev service must keep `user: vscode`, `cap_drop: ALL`,
    `no-new-privileges`, must not be privileged or unconfine
    seccomp/apparmor; bind-mount sources are restricted (workspace,
    docker socket never, store binds only as THE read-only harness
    overmount); host-reading compose features (`include`/`extends`/
    `env_file`/file-backed configs+secrets/local-driver device binds) are
    refused; the extension build context is frozen. Render and daemon see
    byte-identical input. `--unsafe` (global flag) disables the boundary
    for one command, loudly.
  - **`vibe update` is mirror-only.** It fetches into the host mirror,
    verifies, and stages the pin move via `git update-index` — host git
    porcelain never runs against the workspace checkout (whose hooks and
    filters are container-writable).
  - **The tui trusts the store, not the checkout.** Host hooks resolve
    scripts through a per-session harness dir pointing into the store;
    the first post-upgrade `vibe tui` refuses to join a pre-upgrade
    (unsafe) server and asks for `--fresh`; `vibe doctor` checks the
    overmount is present and read-only.
  - **Trust stance (documented in docs/security.md "What is trusted"):**
    the project's compose/code is the OWNER's configuration and is
    trusted; the boundary defends against a compromised container process
    tampering its way to the host, not against a hostile project author.
    install.sh and first-contact warn to read a third-party repo's
    `.vibe/` before the first `vibe up`.
- **BREAKING: the host `GH_TOKEN` passthrough is gone.** Container env is
  baked at create time and visible to every process — the wrong place for
  a credential. GitHub auth is `gh auth login` inside the container
  (fine-grained PAT pasted once; persists in the agent-state volume). A
  reference PAT kept in `.env` must use a neutral name: `GH_TOKEN`/
  `GITHUB_TOKEN` there would override the stored login (docs/
  configuration.md; crossing note in docs/updating.md).
- **The repository is now `vibe-tui-box`** (was the devcontainer-era
  name). GitHub redirects old clone/submodule URLs indefinitely; walk
  consumer `.gitmodules` URLs forward at the next pin bump. The CLI stays
  `vibe`.
- **New: `vibe tui` — the front door.** The workspace as a riced HOST-side
  tmux (socket `-L vibe`, needs tmux ≥ 3.4 on the host; a pinned 3.7b
  source build ships via `src/scripts/host/install-tmux.sh`): agent pane
  over a collapsible host-shell bottom dock, tabbed windows with
  agent-state dots, a command palette on `prefix+Space` (clip, git popup,
  review, new/named agents, project switcher), and a clickable
  cross-project sidebar (below). Layout-vs-persistence split: host tmux
  owns layout/theme/tabs; the container's per-agent tmux sessions keep
  persistence, so closing the terminal loses nothing. Composing
  `vibe agent` / `vibe shell` / `vibe review` panes manually in any
  terminal remains the no-tmux fallback.
  - Multi-project: one session per project on the shared socket.
    Server styling is first-owner-authoritative (a later project with an
    identical pinned conf joins silently; real skew warns and names the
    kill-server handover instead of silently restyling). "Switch project"
    lives in the palette and on `prefix+o` (tmux session tree,
    live-sessions-only by design — no registry). Quitting one project's
    tui exits that client (`detach-on-destroy on`) instead of teleporting
    it into another project's session.
  - **The sidebar** is the cross-project glance: every project with its
    agents' state dots, git branch, and a fleet-wide agent roster
    ("glyph name · project"), click-to-switch, fixed width
    (`@vibe_sidebar_w`), global toggle on `prefix+b`, one per window in
    lockstep. `prefix+t` collapses the host dock to a one-row strip.
  - Flags: `--kill` (stop the UI server — all projects' tui sessions;
    container agents unaffected), `--fresh` (kill + clean relaunch — the
    reset story), `--detach` (build/heal a project's session without
    attaching, to put it on a running tui's sidebar from any shell).
  - Internals hardening along the way: conf hooks resolve scripts through
    a launch-stamped harness dir instead of running the session-cwd's
    `./vibe` per event; palette + state→glyph map single-sourced in
    `src/config/theme.sh`; sidebar refresh is serial-gated (idle tick is
    one tmux round trip); nested-tmux footgun closed (container tmux under
    the tui drops its prefix + status bar so `C-b c` can't make invisible
    windows).
- **New: agent state at a glance — live dots + `vibe ps`.** Claude Code
  hook events map to `working`/`attention`/`idle`/`exited` per agent
  session and render as tab/sidebar dots in the tui, event-driven end to
  end (no polling anywhere): the in-container hook updates the inner tmux,
  which re-emits state as an OSC pane title through the existing
  `docker exec` tty; the host server's `pane-title-changed` hook renders
  it. Attention flashes the tab; a dead frontend pane renders `◌`;
  process death dominates semantic state. Identity rides an env prefix
  (`VIBE_AGENT_SESSION`/`INSTANCE`/`CARRIER`) minted by agent-entry, so
  background/daemon forks of an agent are tracked too, and
  `DEV_AGENT_TMUX=0` runs can't stomp another session's title. Hookless
  agents (grok) deliberately cap at running/exited.
  - **`vibe ps`** renders the glance anywhere: agents (state, liveness,
    age — read-time staleness only) plus the services-session windows.
  - **`vibe agent -s NAME`** runs a parallel instance of the same agent in
    its own session (`agent-NAME`); without it a second `vibe agent`
    reattaches the running one.
  - Hook registration merges idempotently into `.claude/settings.json`
    via `settings-merge.sh` on container create — additive-only, user
    placement wins; the rebuild after a pin bump IS the migration.
- **tmux 3.7b + chafa 1.18.2, built from source (rebuild required).**
  Debian pins tmux 3.5a / chafa 1.14.5; both moved to pinned, checksummed
  source builds (`--enable-sixel`). 3.7b retains sixel images through
  adjacent-pane TUI redraws (the 3.5a failure behind the old full-window
  review workaround); a resize still clears images (upstream reflow —
  rerun repaints). Inside tmux, `vibe show` now uses native sixel ingest
  (the container tmux declares `terminal-features ",*:sixel"`) — the only
  rendering that survives the tui's host-tmux→container-tmux nesting;
  chafa 1.18 also fixes yazi's `--probe` fallback (doctor's old NOTE).
- **Changed: image review is [yazi](https://yazi-rs.github.io/), locked
  read-only.** The ~500-line homegrown viewer is deleted; yazi (pinned by
  version + checksum per arch) is the review surface: `vibe review [DIR]`
  in the invoking terminal, `prefix+i` as the dedicated tmux preview
  window. The vibe plugin adds `A` approve / `R` reject-with-note and
  persistent ✓/✗ badges, appending to `.review-decisions.jsonl`. The
  harness keymap unbinds shell escape and every file operation, and
  openers are replaced wholesale (Enter views through `less -R`) — a
  project-owned `.vibe/yazi/` can still deliberately re-bind. The Claude
  Code image hook reveals into a LIVE `vibe review` first (toast + `g i`
  jump on demand — no cursor theft), falling back to the tmux preview
  window's auto-reveal; with a live reviewer it no longer requires tmux.
- **New: revdiff — read-only diff review trial (rebuild required).**
  [revdiff](https://github.com/umputun/revdiff) (pinned, checksummed) is
  the "review what the agent changed" surface: tree + diff panes, `v`
  toggles file text ↔ diff, annotations print to stdout on quit. Palette
  entry `r` (runs `--untracked`) or `vibe exec revdiff`; deliberately NOT
  a top-level command while it's a trial — the command surface is ABI.
- **Fix: per-checkout project identity — same-named checkouts no longer
  share a compose namespace.** The project name was derived from the
  workspace basename alone, so two checkouts named `app` collided on the
  entire compose project and could tear each other down. Identity is now
  `vibe-<basename>-<8-hex suffix>` seeded from the canonical path into
  `.vibe/.project-id` (per checkout, auto-ignored, worktree-friendly);
  pre-existing unsuffixed projects adopt automatically via compose's own
  labels. The agent-state volume still derives from the bare basename —
  documented ABI (docs/agent-state.md).
- **New: `vibe-svc` + compose-native lifecycle.** `vibe-svc NAME CMD...`
  idempotently runs a workspace process as a window in the shared
  services tmux session (safe on every start, logs in scrollback; no
  `.env` — wrap with env-run.sh); `vibe attach` defaults to `services`.
  `vibe down` is `compose down --remove-orphans` (named volumes survive),
  so project sidecars are no longer orphaned; `vibe status` lists every
  project service (the old status ANDed label filters and was always
  empty). docs/services.md is the sidecar/vibe-svc/host-program chooser.
- **New: `vibe update [TAG]`** — fetch, print the CHANGELOG delta + diff
  stat, and stage the pin move (never commits, never rebuilds); reports
  whether a rebuild is required and flags template changes for
  reconciliation. Works identically in-container. (Post root-of-trust it
  operates mirror-only — see that entry.) **`vibe doctor`** notes pin
  staleness offline (never touches the network).
- **Smaller changes:**
  - `vibe agent`/`vibe attach` logic moved container-side
    (agent-entry.sh receives real argv — no more quoted `bash -lc`
    payload smuggling).
  - Git-hook wiring is loud: doctor NOTEs whenever `core.hooksPath` is
    set (hooks also run host-side via the shared mount), post-create logs
    the wiring.
  - A failing project post-start hook now fails `vibe up` under
    `DEV_BOOTSTRAP_STRICT=1` (the default); the documented
    warn-and-continue path (`0`) actually works now, as does tool
    preflight under strictness 0. (2026-07 external review.)
  - `.gitmodules` migration residue fixed (devcontainer-era section name
    + SSH URL demanded credentials on fresh public clones);
    `install.sh --force` removes the legacy-named section.
  - One shared repo-root walk for host tools (repo-root.sh); the preview
    hook derives image-extension regexes from `VIBE_IMAGE_EXTS` instead
    of three hardcoded copies; positioning doc owns the terminal
    affordances (clip/show/review) — driving agents remains a non-goal.
  - Fixed: `/usr/local/lib/vibe` baked unreadable by the container user
    (`COPY --chmod` also chmods implicitly created parents), which
    silently launched stock yazi without review keys (rebuild required).
  - Removed the legacy `.devcontainer/dev` exec-bit self-heal.

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

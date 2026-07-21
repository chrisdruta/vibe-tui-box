# Backlog

Ideas accepted but not scheduled. Items graduate into a release when they get
designed; entries here are one paragraph of intent, not a spec.

- **RESOLVED (2026-07, both phases at once): the devcontainer-engine
  exit.** Implemented as the pre-v1.0 engine swap: `vibe` drives docker
  compose + docker exec directly, the consumer layout is `.vibe/` with a
  root `./vibe` symlink, and the devcontainer CLI/Node host dependency is
  gone (see CHANGELOG Unreleased). Remaining from this item graduated to
  its own entry below: renaming the repository.

- **Rename the repository — RESOLVED 2026-07-21: `vibe-tui-box`.** Chose the
  name with the TUI pivot settled (earlier candidate `vibe-harness` predated
  the TUI being the headline): keeps Chris's `vibe-tui` identity, suffixes a
  MECHANISM noun (box = sandbox/container/TUI box-drawing — and the chop-suey
  takeout box is the brand story), never a virtue adjective ("secure-vibe-tui"
  rejected: compound reads as a fork, unfalsifiable claim in a name ages
  badly; the GitHub description carries the security claim instead, right
  next to the name). CLI stays `vibe`. Harness-side references flipped in
  this commit (install.sh, README, seeded docs, .gitmodules); the GitHub
  rename itself is Chris's (container gh token has no admin scope — the
  blast-radius model working as designed). GitHub redirects old
  clone/submodule URLs indefinitely; walk known consumers' `.gitmodules`
  URLs forward at their next pin bump. v1.0 cut remains separate and
  pending. Explicitly out of
  scope (decided 2026-07-20): renaming the in-container `vscode` user — it comes
  from the devcontainers base images and is load-bearing ABI (extension
  `USER vscode` contract, `/home/vscode/.agents` paths).

- **RESOLVED (2026-07): interactive installer.** Implemented alongside the
  submodule-first install flow: `install.sh` with no arguments on a tty
  interviews (preset, extras via the new `--extras`
  codex/grok/node/playwright, confirm); any argument or no tty keeps exact
  flag behavior for scripted/CI use.

- **RESOLVED (2026-07): auto-symlink `vibe` into the project root.**
  install.sh now seeds a committed `vibe -> .vibe/vibe` symlink (skipped if
  a real file is in the way); existing projects get it during the compose
  migration. Harmless on Linux/WSL/macOS checkouts; Windows-native
  checkouts see a text file, which was accepted.

- **Reduced-trust profile for unattended runs.** Promised in
  docs/security.md ("planned but not implemented"): a config.env posture for
  long autonomous tasks — `DEV_AUTO_GIT_HOOKS=0` / `DEV_AUTO_INSTALL=0` and a
  doctor mode that verifies the reduced posture instead of the interactive
  one. Review and push stay host-side. The credential half landed early
  (2026-07-20): the `GH_TOKEN` create-time passthrough is gone entirely —
  GitHub auth is `gh auth login` with a fine-grained PAT, persisted in the
  per-project state volume, so the remaining scope is the DEV_AUTO_* posture.

- **RESOLVED (2026-07, differently): the "rewrite the preview subsystem in
  Go" item.** The trigger fired early — dogfooding judged the homegrown
  viewer clunky — and the resolution beat writing our own binary: the
  review viewer is now yazi (pinned release binary in the Dockerfile),
  which is the same class of solution maintained upstream. The remaining
  harness-owned render code is the small `vibe show` one-shot path
  (preview-lib.sh); if THAT grows or breaks repeatedly, fold it into yazi
  usage or revisit. Caveat RESOLVED 2026-07-21 (evening re-eval, three-way
  web survey — yazi ecosystem / tmux-fzf-composed / standalone TUIs): yazi
  KEEPS its seat but the job changed — image viewing no longer needs it
  (native ingest covers `vibe show`), so yazi is now the LOCKED READ-ONLY
  file browser (keymap noops + wholesale opener replacement; it was the
  only file manager that's provably lockable — broot/lazygit/gitui/mc/
  ranger/lf/nnn all failed the lock test, awesome-tmux sidebars are dead
  since 2022), and **revdiff** (umputun, pinned checksummed Go binary)
  was adopted as the diff-review surface (palette `r` /
  `vibe exec revdiff` — deliberately NOT a top-level command while a
  trial: it gets a verb only if it earns harness logic like annotation
  capture; content↔diff toggle, annotations to stdout). Trial: if revdiff
  holds up its annotation output may absorb the A/R verdict flow; the
  ~1-day yazi diff-toggle Lua plugin (existence proof:
  vscode-git-gutter.yazi on our exact 26.5.6) stays the fallback if
  revdiff disappoints. Spare parts if ever needed: fzf `change-preview`
  glue (~50 lines, exact toggle UX), diffnav (checksummed diff-tree
  pager). The host launcher, installer, and lifecycle scripts
  stay bash regardless — they are the bootstrap and must run on stock
  macOS bash 3.2 with nothing installed.

- **RESOLVED (2026-07): reorganize `scripts/` into subdirectories.**
  Superseded by the `src/` reorg that rode along with the devcontainer-engine
  exit (the breaking release made the path moves free): everything
  harness-internal lives under `src/`, entry points stay at the root, and
  `examples/` carries rendered per-preset seeds verified against real
  installs. `src/*` paths are the new public interface (AGENTS.md).

- **RETIRED (2026-07-21): `vibe open` — superseded by `vibe tui`** (the
  host-tmux front door; see the tmux-as-UI supersession record below).
  `open-terminal.sh`, the `open` dispatch, `VIBE_OPEN_LAYOUT/PROFILE`,
  and the planned `.vibe/open-layouts.conf` DSL are all gone — layouts
  are tmux's problem now. The per-pane commands it composed
  (`vibe agent` / `shell` / `review`) remain first-class and manually
  composable in any terminal, which stays the documented no-tmux
  fallback. Original design kept below for the record:
  One command that opens the workspace as native
  terminal panes, Windows Terminal first: each pane runs a stable `vibe`
  command (`vibe agent`, `vibe agent -a codex`, `vibe review`, `vibe shell`),
  so the terminal owns tabs/panes/rendering while the per-agent tmux sessions
  keep persistence. The adapter lives host-side (`src/scripts/host/`), knows
  nothing project-specific, and prints the per-pane commands when no supported
  terminal is found (that fallback IS the macOS story for now). A prototype
  with hardcoded layouts shipped 2026-07-20; graduation means config-driven
  layouts (project-declared panes) and, only after that stabilizes, maybe
  other frontends (WezTerm — see decision record below). Config format decided
  2026-07-21: an optional `.vibe/open-layouts.conf` line-DSL (`layout NAME` /
  `pane` / `split V|H [SIZE]` / `tab`; everything after the op is a `./vibe`
  command line), parsed adapter-neutrally in `open-terminal.sh`
  (parse_layout → emit_wt / emit_fallback, bash-3.2-safe). Built-in layouts
  remain the fallback, so the file is optional and install.sh seeds nothing;
  a WezTerm adapter later is one more `emit_*` function.

- **Agent state at a glance — PLANNED post-v1.0 (designed 2026-07-21).** The
  herdr headline ("every agent: blocked/working/done") built from machinery we
  already own — hooks, not scraping, no daemon, no polling. A
  `src/scripts/agent-state-hook.sh` (same contract as the preview hook)
  maps Claude Code events (SessionStart/UserPromptSubmit/Notification/Stop/
  SessionEnd) to `idle/working/blocked/done`, written atomically to
  `${XDG_RUNTIME_DIR:-/tmp}/vibe-agent-state-$(id -u)/<session>` (runtime
  tmpfs only — never the workspace or the agent-state volume) plus
  `tmux set-option @vibe_state` (event-driven status redraw) and a BEL on
  `blocked` (native tab flash under `vibe open`). Identity rides inside the
  pane command (`env VIBE_AGENT_SESSION=$session` prefixed at the
  agent-entry.sh cmd array), surviving the `%q` re-quote and covering
  `DEV_AGENT_TMUX=0`. `vibe ps` (baked `src/scripts/ps.sh`) renders the
  glance: agent sessions (naming convention `agent(-cmd)(-cold)` joined with
  state files; precedence fresh-state > pane_dead > running) plus the
  services-session windows per svc.sh's model. Hookless agents (grok) cap
  deliberately at `running/exited` + activity age. Registration is new hook
  blocks in `src/templates/claude-settings.json` (consumers merge on pin
  bump, same story as the preview hook). tmux.conf budget: ≤5 lines.
  Sequencing: this (A) → `vibe open` layout graduation (B) → opt-in Codex
  `notify` turn-complete seed (C, only after A proves out); A and B touch
  disjoint files. (2026-07-21 update: render target is now the vibe tui
  status line — tab state dots plus an agents strip on status line 2 of
  the HOST server, state files bind-mounted host-readable; the original
  per-WT-pane composition below described the retired vibe open world.)
  Under `vibe open agents`, each native pane carried its own
  tmux status bar, so the herdr glance emerged from composition.
  **REVIEWED 2026-07-21 (codex gpt-5.6-sol adversarial pass): NOT
  implementation-ready as written** — the design predates the tui pivot and
  the deltas don't close it. The killer: there is NO event path from the
  container hook to the HOST tmux server ("hook sets `@vibe_state`" targets
  the inner server; a bind-mounted file the host can read but is never told
  about cannot trigger a redraw under `status-interval 0` with `#()`
  banned). Redesign direction: the **title
  channel** — the hook updates inner-server state, inner tmux re-emits it
  as an OSC title through the existing `docker exec` TTY (`set-titles` +
  state-encoded `set-titles-string`), the outer pane's `pane_title` changes
  (host `allow-rename off` blocks window renames, not pane-title OSC), and
  the host's `pane-title-changed` hook renders dots/flash — event-driven,
  no daemon, no bind mount, no host-socket sharing, and the agent→pane
  mapping is intrinsic (the event arrives ON the owning pane; duplicate
  attachments each get their own dot). **SPIKE VALIDATED 2026-07-21**
  (nested 3.7b pair in-container; only the docker-exec pty link — already
  proven for escapes by sixel — and the WT host smoke remain, and both
  land free during implementation): inner option change → OSC re-emit is
  IMMEDIATE, no `refresh-client` needed, on attach and on every
  transition; per-SESSION `set-titles-string` scoping works (each outer
  pane renders its own attached agent session's state independently);
  outer window names stay untouched under `allow-rename off`. Gotchas the
  spike bakes into the design: (a) INJECTION — `pane_title` is
  container-controlled text; the host hook command must interpolate ONLY
  `#{pane_id}` (server-controlled `%N`) and the render script fetches the
  title out-of-band via `display -p -t "$1" '#{pane_title}'` — never let
  a format expand title text into host shell words (the naive
  `run-shell "echo ... #{pane_title} >> log"` pattern sh-parses the `|`
  delimiters — observed exploding into pipelines); the renderer also
  validates the `vibe1|` prefix before trusting structure; (b)
  `pane-title-changed` is a PANE-scope hook — `set-hook -g` registers and
  fires it fine, but it does NOT appear in `show-hooks -g` (session-hook
  list), so don't misread that as unregistered; (c) hook `run-shell` is
  asynchronous — a read immediately after a transition can still see the
  old value; harmless for rendering, but tests must allow a beat. This
  DROPS the "state files bind-mounted host-readable" delta above: files
  stay container-tmpfs for `vibe ps` only. Further review corrections now part
  of the design: identity splits into `VIBE_AGENT_SESSION` (stable logical
  name) + `VIBE_AGENT_INSTANCE` (unique per run; minted per process under
  `DEV_AGENT_TMUX=0`), both passed via an `env` prefix in the ONE
  agent-entry.sh cmd array so the `%q` and direct-exec paths can't diverge;
  liveness is layered (process-exit dominates semantic state — an
  agent-entry exit trap plus host `pane-died` hooks write it; outer-pane
  death means "frontend gone", not "agent dead") replacing the
  fresh-state > pane_dead > running chain, which let a stale `working`
  outlive a dead agent; NO wall-clock TTL in the live contract (staleness
  is evaluated only at read time — `vibe ps`, picker open, attach);
  raw BEL is dropped as the blocked mechanism (hook stdout is captured by
  Claude Code; nested bell propagation is unreliable) in favor of the host
  hook setting monitor/alert state itself; hook semantics stay conservative
  — `working/attention/idle/exited`, with `blocked` reserved for events
  that reliably mean user intervention (Notification ≠ blocked,
  Stop ≠ session done); and the settings-template hook blocks need an
  idempotent merge story, not "consumers merge on pin bump" hand-waving
  (RESOLVED with step 5: settings-merge.sh, run by post-create on every
  container create — additive-only, command-string identity, user
  placement wins, invalid JSON bails; the rebuild after a pin bump IS
  the migration, and `vibe update` says so in its handoff).
  Standing guardrail restated: inner tmux gets NO dashboard logic (≤5-line
  budget), host tmux renders only pushed precomputed state, hookless
  agents stay `running/exited`.

- **Productize worktrees.** Today parallel worktrees work but are manual:
  a differently-named worktree directory gets its own `agent-state-<basename>`
  volume, which means fresh agent logins per worktree unless the user edits
  the volume `source=` themselves (docs/agent-state.md). Add
  `vibe worktree create/list/remove` plus an explicit state-scope choice
  (default: today's per-workspace isolation; opt-in: worktrees of one repo
  share a volume via explicit `source=` in the seeded compose override).
  Constraint: the `agent-state-<workspace-basename>` default derivation is
  ABI (AGENTS.md) — sharing happens by writing an explicit `source=`, never
  by changing the derivation. Scheduled with the vibe tui spaces phase
  (worktrees are the natural in-project "spaces"). 2026-07-21 sol-review
  addition: a command CONTRACT must be written before coding — branch
  create vs attach, worktree placement/naming/collisions, whether `.vibe`
  config is inherited or regenerated, whether the shared-state choice edits
  committed compose.yaml or a local override, what `remove` does to running
  containers and host tmux sessions, and dirty/unmerged refusal rules.
  Hard lines already settled: state sharing is NEVER inferred from
  repository relationship (explicit `source=` opt-in only, recorded as
  project-owned config), and removal never deletes agent-state volumes
  automatically.

- **Decision records from the 2026-07-20 external design review** (so future
  reviews don't relitigate): **REJECTED — session-backend abstraction**
  (`VIBE_SESSION_BACKEND=tmux|shpool|none` and renaming the `DEV_AGENT_TMUX*`
  vars). tmux here is not just persistence: the `prefix+i` preview window,
  the Claude-hook DDS feed, and chafa passthrough rendering are tmux-specific,
  and `DEV_AGENT_TMUX=0` already is the "none" backend. An abstraction layer
  plus a config deprecation cycle buys nothing at the current consumer count.
  **DEFERRED — version-lock machinery** (`vibe versions lock`): Dockerfile
  ARGs already pin what upstream supports; Claude `stable` and the base-image
  tag float deliberately. If reproducibility ever bites, the cheapest step is
  a base-image digest pin, not a lockfile subsystem. **NOT NOW — WezTerm
  frontend**: revisit only as another `vibe open` adapter once layouts are
  config-driven.

- **Decision records from the 2026-07-21 terminal-UX review** (herdr
  comparison; so future "should we own the multiplexer?" rounds don't
  relitigate): **REJECTED — own terminal multiplexer (shell or Rust).**
  Sizing herdr (the best-in-class agent mux): ~193K LoC of Rust, and the
  genuinely hard core is vendored, not written (Ghostty `libghostty-vt` VT
  emulation, `portable-pty`); its hand-rolled ~20K LoC daemon/wire-protocol/
  snapshot layer buys detach/reattach that tmux gives us free. A shell
  multiplexer is definitionally impossible — PTY allocation plus a VT state
  machine is not expressible in shell; that program is tmux. No revisit
  trigger: if agent *orchestration* is ever wanted, run an orchestrator (or
  herdr itself) inside the container instead of growing one.
  **REJECTED — tmux as the one true UI** (heavy tmux-as-frontend
  customization). Image review renders best outside tmux; the `vibe open`
  thesis (terminal owns layout, tmux owns persistence) stands. tmux.conf
  stays minimal: no status-format dashboards, no popup farms, no
  `status-interval` polling — `vibe ps` is the dashboard.
  **Renderer-agnostic note:** the in-tmux image constraint is tmux's
  graphics *compositor*, not the emitting library — timg/chafa/img2sixel/
  yazi all emit the same sixel/kitty/iTerm2 sequences, and tmux ingests but
  does not reliably re-emit sixel on redraw (tmux #4499/#4639/#5126), which
  is exactly the "agent TUI redrawing in one pane, image in the split next
  to it" case. Swapping renderers changes nothing inside tmux. AMENDED
  2026-07-21, same day: the "upgrading tmux buys churn, not the unlock"
  reading of the open issues was WRONG empirically — a dogfood spike
  (source-built 3.7b `--enable-sixel` on an isolated socket, WT host)
  showed bare `chafa -f sixel` images PERSISTING through adjacent-pane TUI
  redraws, the exact 3.5a failure. Resizes (pane or host window) still
  clear images — upstream reflow behavior, acceptable. tmux 3.7b + chafa
  1.18.2 are now pinned source builds in the Dockerfile; in-image
  validation (2026-07-21, post-rebuild): `vibe show` in a native `vibe
  open` pane renders pixel-exact and SURVIVES RESIZE (the vibe-open thesis
  working as designed); prefix+i is correctly wired but yazi-inside-tmux
  does not render images on 3.7b (its terminal detection through tmux
  doesn't recognize the new ingest; deliberately NOT chased — the native
  pane is the documented best review surface, and upstream yazi is more
  likely to fix tmux detection than we are). RESOLVED 2026-07-21 (vibe
  ui host smoke): the in-tmux path DOES drop `--passthrough tmux` when
  the client is declared sixel-capable — native ingest is the only
  rendering that survives nesting (passthrough is one transient copy the
  outer tmux wipes on its next repaint of those cells), host-validated
  through host-tmux→container-tmux; the unlock was declaring
  `terminal-features ",*:sixel"` on the inner server (its client, the
  outer tmux, never advertises sixel in DA — the same missing
  declaration behind the historical "+" placeholders). Resize still
  clears (upstream reflow; rerun repaints). Still open, low priority:
  review-as-split. The devcontainer boundary is a
  non-issue
  (escapes pass through the `docker exec` TTY; DA1 probing already bails
  under `$TMUX` in preview-lib.sh).
  **NOT NOW — kitty-graphics Unicode-placeholder path.** The one real
  unlock for images-in-a-tmux-split: kitty-protocol placeholders anchor
  images to cells and survive multiplexer redraws, but the *outer* terminal
  must speak kitty graphics (kitty/WezTerm/ghostty — not Windows Terminal,
  which is sixel-only). Revisit trigger: when the WezTerm `vibe open`
  adapter lands, test `chafa -f kitty --passthrough tmux` (needs chafa
  ≥1.16 for placeholders; the 1.14.5 pin is already flagged too old in the
  preview stack) and if solid, the tmux review window may become a split on
  kitty-capable terminals.
  **Accepted ledger — what this path permanently cedes vs herdr:** one
  unified live dashboard of agent screens, a programmatic agent socket API
  (spawn/read/wait), real state fidelity for hookless agents, a
  cross-project fleet view, and dashboard-coupled worktrees. The trade: we
  keep ~2K LoC of bash and take the herdr headline (state at a glance,
  attention on blocked), not the herdr platform. AMENDED 2026-07-21
  (Chris's deliberate supersede at the spaces must-decide round — not a
  side effect): "cross-project fleet view" leaves the ceded list in its
  render-only form — a cross-project state GLANCE (the status-line-2
  agents strip, across the socket's sessions) is in scope for the gated
  spaces phase. The screens dashboard, control API, and hookless-agent
  fidelity stay ceded; nothing that drives, schedules, or controls agents
  entered scope.

- **SUPERSEDED (2026-07-21, later same day): "REJECTED — tmux as the one
  true UI" is overturned; `vibe ui` (host-side riced tmux) shipped as
  phase 1.** What changed since the morning record: (a) Chris's actual
  pre-`vibe open` workflow is one WT tab for `vibe agent` plus a SECOND WT
  tab for host-side work (git on the not-yet-moved side, `vibe clip`) —
  per-terminal wrappers can't fold that into one surface, tmux can;
  (b) the 3.7b sixel spike had already validated a source-built host tmux
  on the WT host, killing the "host tmux is 3.5a" objection; (c) the
  cross-project "spaces" ambition (herdr-style sidebar) needs a host-side
  server anyway — placing the UI server on the HOST from day one means
  spaces later is just more sessions on the same socket, never a third
  nesting layer. What did NOT change: the layout-vs-persistence split
  stands — the host tmux (`-L vibe` socket, `src/config/tmux-ui.conf`)
  replaces Windows Terminal as the layout owner, while the container's
  per-agent tmux sessions keep persistence untouched (`./vibe agent` in a
  pane, `VIBE_NESTED=1` drops the inner status bar). The no-polling stance
  also stands: `status-interval 0`, no `#()` segments; state lands
  event-driven when the agent-state hook item wires `@vibe_state` (its
  design gains one delta: state files must be host-readable — bind-mount
  the runtime state dir — for the host status bar to render them).
  Consequences for open items: `vibe open` stays as the non-tmux adapter
  but its layout-DSL graduation is likely superseded by tmux layout
  definitions — revisit before building the DSL; the "agent state at a
  glance" item now renders INTO the vibe socket's status line (tab dots +
  a second status line as the agents strip) instead of per-WT-pane bars;
  the container tmux.conf ≤5-line budget applies only to the INNER conf
  (unchanged) — the riced budget lives host-side in tmux-ui.conf. Phase 2
  (spaces): session-per-project already works (per-checkout identity
  falls back to the unique project name on basename collision —
  two-clone test green in-container 2026-07-21); a session picker popup
  (`choose-tree` or fzf) + `vibe ps` palette upgrade complete it.
  Host-smoke results (same day, Chris's WSL): theme/tabs/borders render
  in WT, shift+tab mode-cycling survives the nested pair, VIBE_NESTED
  plumbing works end-to-end. Distro tmux 3.6 does NOT ingest sixel at
  all (compile-time flag; the "images degrade below 3.7" warning was
  optimistic — it's no images, period), so the host 3.7b source build is
  effectively required for `vibe show` in the UI; 3.7b installed via
  install-tmux.sh. Two UX gaps found and fixed same day: reattaching
  into a dead-agent-pane corpse with no affordance (ui.sh now respawns a
  dead agent / rebuilds an all-dead session before attaching; prefix+Q =
  documented quit, prefix+d = detach, both in the palette), and the
  running-server-pins-the-old-binary trap after a tmux upgrade (ui.sh
  now warns on server/client version skew and prints the kill-server
  line). Nested sixel RESOLVED same day: native ingest at the inner
  server (see the amended image-stack record above) renders and
  persists through redraws on the 3.7b host server; resize clears
  (upstream, rerun repaints). The `,*:sixel` terminal-feature ships in
  the container tmux.conf — baked into the image, so consumers get it
  on their next rebuild (dogfood ran it live-set until then). Clickable
  "+" confirmed working (2026-07-21 evening) — phase 1 fully signed off.

- **Cleanup pass (2026-07-21, post-sign-off): `vibe open` retired,
  `vibe ui` renamed `vibe tui`.** Product framing settled with Chris:
  the TUI is the headline feature — the one surface a user lives in —
  and the command skeleton (up/rebuild/agent/run/exec/doctor + the
  compose engine, per-checkout identity, `.env` hygiene, agent-state
  volume) is the security-and-lifecycle foundation it stands on. The
  renamed pieces: `src/scripts/host/tui.sh`, `src/config/tmux-tui.conf`,
  `VIBE_TUI_TMUX`, `VIBE_TUI_CONF`; `vibe ui` remains an alias with a
  one-line notice until v1.0. Project rename candidate on the table:
  **"vibe-tui"** (Chris; supersedes the earlier "vibe-harness" working
  name) — repo rename + v1.0 cut remain an explicit release decision for
  Chris; GitHub auto-redirects old clone/submodule URLs, so consumer
  pins survive the rename.

- **Open decisions from the 2026-07-21 phase-2 sol review (codex
  gpt-5.6-sol; spaces side — must be settled before the spaces phase is
  coded).** The agent-state corrections are folded into that item above;
  these are the rest. (1) **Shared-server config ownership**: the first
  project to start `-L vibe` wins, `VIBE_TUI_CONF` is server-global and the
  last launcher overwrites it, so prefix+R can reload project A's pinned
  conf over project B's sessions — decide between a host-installed
  versioned tui conf (install-tmux.sh already exists as the vehicle) or
  first-owner-authoritative with a version-skew warning; until decided,
  the mutable global-reload path is the bug surface. (2) **Project
  registry**: `choose-tree` only shows live sessions and discovers
  nothing; a real picker needs a registry keyed by `.vibe/.project-id`
  (canonical path, display name, last-seen), registered on `vibe tui`,
  pruned user-triggered (no polling). Specify picker rows/actions (live
  session, dormant checkout, missing path, create/switch/close) BEFORE
  choosing choose-tree vs fzf — choose-tree suffices only if
  live-sessions-only is accepted. (3) **Launcher decomposition**: tui.sh
  currently refuses to run with `$TMUX` set; the picker needs idempotent
  ensure-session / attach / switch-client / list operations split out.
  (4) **Status-line-2 agents strip**: needs a static format spec (ordering,
  truncation, narrow widths, dead agents) — and note a CROSS-PROJECT strip
  contradicts the accepted "no fleet view" ledger line; scope it to the
  current project unless that ledger entry is deliberately superseded
  (Chris's call, not a side effect).
  **RESOLVED 2026-07-21 (Chris's calls; spaces implementation itself is
  DEMAND-GATED on roblox two-project dogfood — onboarding the roblox repo
  gives real multi-project use within days; if switching pain shows, the
  gated work builds, if choose-tree suffices, this closes as done-enough):**
  (1) conf ownership = FIRST-OWNER-AUTHORITATIVE, SHIPPED (tui.sh no
  longer overwrites VIBE_TUI_CONF: adopts only when unset or when the
  owner's conf file vanished; content-identical confs join silently via
  cmp so same-pin projects never warn; real skew warns with the owner
  path + kill-server handover; scratch-socket tested, all four paths).
  (2) picker = live-sessions-only `choose-tree` behind a palette entry
  when the gate triggers; NO registry — dormant-checkout discovery
  deliberately declined, so the rows/actions spec collapses to tmux's
  own. (3) launcher decomposition only as far as the gated work actually
  needs — not a refactor project. (4) agents strip = CROSS-PROJECT:
  Chris deliberately superseded the no-fleet-view ledger line (amended
  in place above) — render-only glance, control stays ceded. Still
  required before coding it: only the static format spec — the technical
  questions are SPIKE-ANSWERED 2026-07-21 (scratch-socket 3.7b pair,
  nested observer client, in-container): (a) `#{S:}` loops DO aggregate
  other sessions' user options, and nested `#{W:}`/`#{P:}` resolve the
  looped window/pane's own options — GOTCHA: a literal comma inside
  `#{W:...}`/`#{P:...}` is the current-window/pane ALTERNATE-format
  separator, so it silently splits the format (spike hit it: a
  one-window session rendered the empty alternate and looked like
  nested lookups were broken); use non-comma separators or `#,`.
  (b) under `status-interval 0`, `set-option` writes from an external
  client at ALL THREE scopes (session `-t`, window `-w`, pane `-p`)
  immediately redraw OTHER sessions' attached clients' status lines —
  state-render.sh's existing writes drive a cross-project strip with
  zero new plumbing. (c) the one real gap: session create/kill does NOT
  redraw other clients' status; validated event-driven fix is
  `session-created`/`session-closed` hooks running `refresh-client -S`
  over `list-clients` (xargs one-liner, no polling) — strip picked up
  the new and killed session immediately in the spike.
  **STRIP FORMAT SPEC (2026-07-21, scratch-validated end-to-end — the
  format string below rendered correctly under a nested observer:
  colors, branches, truncation, auto-show; only CODING remains and it
  stays demand-gated):**
  - *Placement*: `status-format[1]` + `status 2` in tmux-tui.conf. The
    line inherits `status-style` (do NOT lead with a bg token — every
    `#[default]` resets to status-style, so an explicit bg fragments).
  - *Visibility*: auto. Strip exists only while the socket has ≥2
    sessions; with one project the UI is byte-identical to today.
    Driven by the same `session-created`/`session-closed` hooks as the
    refresh nudge (below) — no polling, no launcher logic.
  - *Ordering*: tmux-native session order (alphabetical). Formats
    cannot reorder; instead the CURRENT project renders bold theme-fg,
    others dim. Stable order also makes narrow-width right-clipping
    (tmux's native behavior, accepted for v1) predictable.
  - *Per project*: `#{=12:session_name}` (truncation), then one dot
    per window that HAS `@vibe_glyph`, window-index order, no labels —
    identity/detail stays `vibe ps`'s job. Glyph vocabulary identical
    to the tabs (`●` by color, `✗` exited, `◌` frontend-dead; dead
    agents render as-is). EXCEPTION — attention: on tabs the dot_fg is
    deliberately @thm_bg (blends into the flashing tab); the strip has
    no flashing bg, so `#{?#{@vibe_attn},...}` overrides to a coral
    `●`. Windows without `@vibe_glyph` (host shells, diff windows)
    emit nothing; a stateful-window-less session renders name-only
    (presence signal — also how non-vibe squatter sessions surface).
  - *The format string* (validated; NOTE the spike comma gotcha — no
    top-level commas inside `#{W:}`/`#{P:}` loops, style commas as
    `#,`, and nested `#{==:a,b}`/`#{?a,b,c}` commas are safe):
    `set -g status-format[1] "#[align=left]#{S: #{?#{==:#{session_name},#{client_session}},#[fg=#{@thm_fg}#,bold],#[fg=#{@thm_dim}]}#{=12:session_name}#[default]#{W:#{?#{@vibe_glyph}, #{?#{@vibe_attn},#[fg=#{@thm_coral}]●,#[fg=#{@vibe_dot_fg}]#{@vibe_glyph}}#[default],}} #[fg=#{@thm_border}]│}"`
  - *The hooks* (visibility + create/kill redraw nudge in one; POSIX sh
    `for` loop, NOT `xargs -r` — macOS xargs lacks `-r` and the conf
    must stay mac-safe; plain `tmux` is correct inside run-shell, which
    inherits the server's context):
    `set-hook -g session-created 'run-shell -b "n=$(tmux list-sessions 2>/dev/null | wc -l); if [ \"$n\" -gt 1 ]; then tmux set -g status 2; else tmux set -g status on; fi; for c in $(tmux list-clients -F \"##{client_tty}\"); do tmux refresh-client -S -t \"$c\"; done"'`
    plus the identical `session-closed` hook.
  - *Acceptance* (rerun of the validation harness): nested observer on
    a scratch socket; assert green/coral/red dots + dim name branches
    via `capture-pane -e` SGR runs, 12-char truncation, no-glyph
    windows silent, strip absent at 1 session / present at 2, create
    and kill both propagate without any client action.

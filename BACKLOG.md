# Backlog

Ideas accepted but not scheduled. Items graduate into a release when they get
designed; entries here are one paragraph of intent, not a spec.

- **RESOLVED (2026-07, both phases at once): the devcontainer-engine
  exit.** Implemented as the pre-v1.0 engine swap: `vibe` drives docker
  compose + docker exec directly, the consumer layout is `.vibe/` with a
  root `./vibe` symlink, and the devcontainer CLI/Node host dependency is
  gone (see CHANGELOG Unreleased). Remaining from this item graduated to
  its own entry below: renaming the repository.

- **Rename the repository — SCHEDULED pre-v1.0 (decided 2026-07-20).** The `-devcontainer-` in
  `vibe-devcontainer-submodule` no longer describes the project post-engine-swap
  (candidate: `vibe-harness`). GitHub redirects old clone/submodule URLs
  indefinitely, so existing consumers keep working, but the name is embedded in
  places a redirect doesn't fix: the `install.sh` submodule-add URL and help
  text, README title/badges, seeded docs, and the onboarding prompt. Do it as
  its own release — ideally before v1.0 while the consumer count is small — and
  walk known consumers' `.gitmodules` URLs forward afterwards. Explicitly out of
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
  usage or revisit. Standing caveat (2026-07-21): yazi is the incumbent for
  dedicated image review, not a settled commitment — with tmux 3.7b's
  sixel retention, simpler viewers (or plain `vibe show` in a split)
  compete again; re-evaluate after the 3.7b rebuild validation. The host launcher, installer, and lifecycle scripts
  stay bash regardless — they are the bootstrap and must run on stock
  macOS bash 3.2 with nothing installed.

- **RESOLVED (2026-07): reorganize `scripts/` into subdirectories.**
  Superseded by the `src/` reorg that rode along with the devcontainer-engine
  exit (the breaking release made the path moves free): everything
  harness-internal lives under `src/`, entry points stay at the root, and
  `examples/` carries rendered per-preset seeds verified against real
  installs. `src/*` paths are the new public interface (AGENTS.md).

- **`vibe open`: host terminal-layout adapter — first feature after v1.0
  (decided 2026-07-20).** One command that opens the workspace as native
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
  disjoint files. Under `vibe open agents`, each native pane carries its own
  tmux status bar, so the herdr glance emerges from composition.

- **Productize worktrees.** Today parallel worktrees work but are manual:
  a differently-named worktree directory gets its own `agent-state-<basename>`
  volume, which means fresh agent logins per worktree unless the user edits
  the volume `source=` themselves (docs/agent-state.md). Add
  `vibe worktree create/list/remove` plus an explicit state-scope choice
  (default: today's per-workspace isolation; opt-in: worktrees of one repo
  share a volume via explicit `source=` in the seeded compose override).
  Constraint: the `agent-state-<workspace-basename>` default derivation is
  ABI (AGENTS.md) — sharing happens by writing an explicit `source=`, never
  by changing the derivation. Scheduled after `vibe open`.

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
  likely to fix tmux detection than we are). Still open, low priority:
  whether `vibe show`'s in-tmux path can drop `--passthrough tmux` for
  native ingest, and review-as-split. The devcontainer boundary is a
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
  attention on blocked), not the herdr platform.

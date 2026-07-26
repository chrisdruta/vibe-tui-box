# Backlog

Ideas accepted but not scheduled. Items graduate into [ROADMAP.md](ROADMAP.md)
when they get designed and sequenced; entries here are one paragraph of
intent, not a spec. Shipped work moves to the CHANGELOG (details live in git
history); settled calls worth not relitigating live in the decision-record
section at the bottom — as revisable records, not fences.

## Open

- **Reduced-trust profile for unattended runs (`vibe agent --jailed`).**
  A weaker-trust posture for letting an agent run without a human watching:
  read-only workspace bind (or a disposable worktree bind), a scratch
  agent-state volume so no OAuth tokens or session history ride along, no
  env file, and network `none` or routed through the egress sidecar (next
  entry). In engine terms this is a plan variant the engine compiles from a
  flag — no schema surface — and doctor verifies the reduced posture; per
  the 2026-07-22 security review it also rejects services, extensions, and
  imports. This is the "different boundary per trust level" answer to inner
  sandboxes (see the no-nested-sandboxes decision record). Demand-gated on
  the first real unattended run.

- **Per-project egress visibility (wanted 2026-07-22).** A per-project VIEW
  of what the container talks to — visibility first, enforcement later; the
  guardrail-not-jail philosophy applied to the one surface security.md
  admits is wide open. Sketch: (1) an engine-generated DNS-forwarder
  sidecar in the project plan whose query log IS the project's domain
  ledger — name-level, no MITM, no proxy env vars for tools to ignore;
  (2) an in-container live-socket sampler (`ss`/proc-net, works
  unprivileged; packet capture is off the table by design — cap_drop ALL
  removes NET_RAW) attributing current connections to processes;
  (3) surfaced in the tui — palette window / `vibe exec` trial first, not a
  top-level verb until it earns harness logic. Accepted blind spots:
  direct-to-IP and DoH skip the DNS log (the sampler still shows those
  IPs). Upgrade path: the sidecar seat is exactly where an L7 allowlist
  proxy would sit (2026-07 research: dynamic allowlists > static
  iptables) — that enforcement half is what `--jailed`'s network posture
  consumes.

- **Productize worktrees.** In v2 each worktree that registers becomes its
  own project with its own engine-named agent-state volume, so parallel
  worktrees work today but mean fresh agent logins per worktree and manual
  setup. Add `vibe worktree create/list/remove` plus an explicit
  state-scope choice (default: today's per-project isolation; opt-in:
  worktrees of one repo share agent state, recorded as project-owned
  config — the schema currently has, deliberately, no way to say this).
  Before coding, write the command CONTRACT (2026-07-21 review): branch
  create vs attach, worktree placement/naming/collisions, whether `.vibe`
  config is inherited or regenerated, what `remove` does to running
  containers and host tmux sessions, and dirty/unmerged refusal rules.
  Hard lines already settled: state sharing is NEVER inferred from
  repository relationship (explicit opt-in only), and removal never
  deletes agent-state volumes automatically.

- **Review/image stack revival.** The v1 image affordances did not survive
  the cutover: `vibe show` (sixel preview), `vibe review` (locked
  read-only yazi browser with A/R verdicts), the preview window, and the
  sixel pipeline (show-image.sh, preview-image-hook.sh, yazi plugin).
  The clipboard half is fully back (tui `prefix+v`, and the `vibe clip`
  verb restored 2026-07-24); the rest is gated on the
  revdiff trial verdict and the kitty-graphics trigger (entries below).
  Revival verdict (2026-07-24, Chris): a REDESIGN, not a port — the v1
  layering (bash wrapping yazi wrapping a Lua verdict plugin over
  layered configs) is explicitly not wanted back. The v1 git history
  remains the reference for the A/R verdict *flow* and the decisions
  JSONL contract; the next design should put verdict capture at an
  engine-owned layer and keep the viewer replaceable. (2026-07-26: the
  default *viewing* path is now decided — editor-as-surface nvim
  popups, docs/tui-layout.md "Editor surfaces"; this entry narrows to
  the verdict-capture and image halves.)

- **revdiff trial verdict (pending dogfood).** revdiff was the v1 trial
  diff-review surface; it gets a top-level verb only if it earns harness
  logic (annotation capture — its annotations-to-stdout channel may
  eventually absorb the `vibe review` A/R verdict flow). (2026-07-26:
  the fallback is no longer the yazi diff-toggle Lua plugin — the
  bundled review stack ships lazygit (`prefix+g`) as the default
  diff-review surface (dogfood-approved; the interim `prefix+G`
  difftool walk and its diffview successor both retired the same day
  they shipped), so revdiff now competes only for the
  annotation-capture harness role; other spare parts (fzf
  change-preview glue, diffnav) remain recorded in git history.)

- **Upstream a codex-plugin sandbox override.** The official
  codex-plugin-cc pins per-thread sandbox modes over the app-server API
  (`sandbox: "read-only"` / `"workspace-write"` in its scripts), which
  `$CODEX_HOME/config.toml` cannot override — so lifecycle.sh
  post-create rewrites the pins to `danger-full-access` inside the
  container, exact-matched against plugin v1.0.6 and a no-op on
  anything else. That patch should die: file/land an upstream option
  (env var or config key the plugin honors, e.g. a sandbox override for
  externally-sandboxed environments), then drop `codex_patch_plugin`.
  Until then, bump the sed patterns when the plugin pin moves.

- **Host conveniences.** v1's install-tmux.sh (pinned tmux source build
  for hosts below 3.4) and start-ollama.sh. Revive on demand.
  (2026-07-25: the CONTAINER side of that pin is back — the tools
  recipe builds v1's exact tmux version+checksum with --enable-sixel;
  the v2 cutover had silently regressed to distro tmux, reintroducing
  the sixel-drop v1 pinned against. The host-side installer stays
  retired.)

- **TUI: consume the engine renderers — SHIPPED 2026-07-24.** The
  sidebar now renders engine truth through the `@vibe_engine_serial`
  channel + cached background fetches (`_fleet` porcelain,
  `_sidebar` detail block; docs/architecture.md "TUI and agent
  sessions"). Surviving deferred bits, in order of pull: (1) a
  docker-events watcher for out-of-band container deaths — today the
  `@vibe_engine_refresh` slow tick (30s) covers them; (2) cold
  registered projects render as dim non-clickable rows — the product
  call whether their click dispatches `up` stays open (brushes the
  live-sessions-only picker record); (3) the richer roster —
  container-side agent sessions without host windows via `vibe ps`
  (deferred from agent-session slice 3) — can now ride the same fetch
  cache. (2026-07-25: (3) grew a destination — **tray phase 2**: the
  bottom bar's window cells should graduate into an agent-truth roster,
  windowless agents rendered as dim clickable cells whose click spawns
  a viewer (`vibe agent -s NAME` window). Needs the fetch-cache rows
  plus a `mouse_status_range` cell per agent; the bar itself, branding
  button, and range dispatch shipped with the bottom-bar move.)

- **Branch-review remainder (2026-07-25).** The pre-merge review's nine
  correctness findings were fixed (45eaa14); the verified-but-unfixed
  tail, in rough value order: (1) hot-path forks — fixed 2026-07-26:
  statusline.sh, subagent-statusline.sh, and agent-state-hook.sh went
  pure-bash (`$UID`, `read <file` behind `-r` guards, `${var//[^…]/}`
  scrubs, `printf -v`/`%(%s)T`, bash dirtrim/rounding);
  state-render.sh was already clean — the claim was stale. Cold-path
  `id`/`head` in agent-ps.sh deliberately left for item (7)'s dedup.
  This sub-item is done; (2) engine-verb
  popups single-sourced 2026-07-26 into `scripts/popup.sh` (which also
  fixed the real defect underneath: display-popup does not
  format-expand its shell-command, so bind u/D/p and the tray's `req`
  cell shipped a literal `#{@vibe_exe}` into bash — only the
  menu-expanded palette door worked); the stop/park popups now ride
  popup.sh's `-w/-h` and the Q/K confirm prompts are command aliases
  (`vibe-quit-ui`/`vibe-kill-server`) shared by binds and palette —
  this sub-item is done; (3) the bar's rule line was a 400-glyph
  literal — regenerated at 1000 with the 2026-07-26 polish pass, this
  sub-item is done; (4) stop/restart — fixed 2026-07-26 at pragmatic
  depth: one `AgentMode` on AgentRequest (invalid combos
  unrepresentable), wire format untouched, and agent-session.sh now
  REJECTS `--restart` in stop mode instead of swallowing it — this
  sub-item is done; (5) startApproved's
  Docker Ping is redundant with the Status call's own error; (6)
  a rebuild's agent refresh busts the engine-pinned bun/rokit layers
  too (they sit after the agent layers — reorder for warm refreshes);
  (7) agent-state dir derivation — fixed 2026-07-26:
  `payload/container/state-dir.sh` is the one sourced definition for
  statusline/hook/agent-ps (the copies had already drifted) — this
  sub-item is done; (8) palette.sh's empty-`"${target[@]}"` expansion
  violates the bash-3.2 pledge if ever invoked clientless (no shipped
  caller does); (9) `vibe down` self-HUP — fixed 2026-07-26: the CLI
  defers the UI-session kill to a post-render hook (App default
  unchanged for direct callers), and ignores SIGHUP around the kill so
  a fully-rendered `--json` exit stays clean; residue: an interactive
  shell inside the session still dies with its pane, inherent to
  parking from inside — this sub-item is done. The
  agent-state hooks, subagent statusline, and `/vibe:request` moved
  into `payload/container/claude-plugin/`, loaded per session with
  `--plugin-dir` from the read-only payload. Verified in-container:
  hooks fire from a write-protected dir, commands namespace as
  `/vibe:…`, and the only volume write is an empty per-plugin `data/`
  dir — no content copies. Marketplace-style install is REJECTED
  permanently: it copies plugin bytes onto the agent-state volume,
  recreating the unpinned-mutable-plugin-state defect class recorded
  above. A plugin cannot express `statusLine`, `autoMemoryEnabled`,
  `autoUpdates`, or `sandbox` — those stay in the thin `--settings`
  file; a pure-plugin end state is off the table upstream-permitting.
  Candidate future content: an environment skill distilled from the
  seeded `.vibe/AGENTS.md`.

- **TUI layout pass — SHIPPED 2026-07-24.** Spec written first as
  demanded ([docs/tui-layout.md](docs/tui-layout.md)), then wired:
  `_state` display form, `@vibe_dock_size` + `@vibe_engine_refresh`
  knobs, width-derived sidebar truncation, and the
  `~/.config/vibe/tui.conf` user hook instead of per-property options.
  Palette/glyphs single-source from `internal/tmuxui/theme.go` via
  payload generation. Layout disagreements start by editing the spec.

- **tui follow-ups (low priority).** Review-as-split
  — images in a tmux split survive redraws only via kitty-graphics Unicode
  placeholders, which need the OUTER terminal to speak kitty graphics
  (Windows Terminal is sixel-only), so the revisit trigger is a
  kitty-capable frontend becoming real (then test
  `chafa -f kitty --passthrough tmux`).

- **TUI polish pass — SHIPPED 2026-07-26.** Spec first
  (docs/tui-layout.md, same-day update with the embedded target
  frame), then wired: roster flows after the fleet section + footer
  hint row (frame.go), detail block display form `● role · hash` /
  `▲ n pending` (views.go Sidebar), ` · ` separators in the stamped
  status-right (app/tui.go), cheatsheet gains
  `z zoom · [ scroll · x close · f files · G review`,
  `prefix+f`/`prefix+G` editor popups via scripts/review.sh + the two
  palette items, and the bar-border rule literal generated at 1000
  cols instead of 400 (closed branch-review remainder item 3).
  Pending dogfood feedback — the layout calls are revisable once felt.

- **Bundled review stack — WIRED 2026-07-26 (pending rebuild proof).**
  All five work-list items landed same-day: install.go layers (nvim
  0.12.4, lazygit 0.63.1, tree-sitter CLI 0.26.11, five plugins at
  pinned SHAs, parser compile), the `payload/container/nvim` config
  tree + `edit.sh` launcher, the theme.lua/lazygit-yml generated
  renderings, and the `f`/`g`/`G` rewire through the
  `scripts/review.sh` router (repurposed from the host difftool gate
  it briefly was). What remains is empirical: a `vibe rebuild` proves
  the parser layer (headless nvim-treesitter main-branch install —
  the one build step engine tests cannot execute; if it fights back,
  the layer is independently droppable), and dogfood judges the
  keymap contract. Rebuild attempts (2026-07-26) settled the CLI pin:
  every tree-sitter 0.26.x release binary links glibc 2.39 against
  the base's 2.36, and the cargo-built-CLI detour was tried and
  rejected the same day (a Rust toolchain in the layer is exactly the
  heavyweight dep this image avoids). The pin landed on the OFFICIAL
  0.25.10 binary (glibc 2.34/2.29): the README's ">= 0.26.1" is a
  docs floor, not enforcement — install.lua only runs `tree-sitter
  build` for our parser set (none carry `generate = true`), verified
  by building a pinned grammar to a working .so with that exact
  binary. The `requires` names in parsers.lua (ecma/html_tags/jsx)
  are query-only pseudo-parsers shipped inside the plugin — never
  install-list entries. First dogfood verdicts (2026-07-26, Chris):
  lazygit approved and PROMOTED to the sole diff-review surface —
  `prefix+G`/diffview retired the day it shipped (a second diff
  surface was one too many); the files surface swapped mini.files →
  oil.nvim (floating columns in a full-screen popup read as broken;
  oil fills the window).

- **Host editor passthrough (parked 2026-07-26; recipe documented
  same day).** The original editor-as-surface idea — your own host
  nvim/config as the viewer — survives as a `~/.config/vibe/tui.conf`
  rebind of `prefix+f/g`, now documented with an example in
  usage.md's TUI section. First-classing it (e.g. a preferred-editor
  knob) stays demand-gated and against the knobs-stay-minimal record.

- **Open flag:** should the root AGENTS.md import a project-level
  `.vibe/AGENTS.md` the way the future preset template will tell consumer
  projects to?

## Decision records (settled calls — revisable with new evidence)

Records predating the 2026-07-23 v2 cutover cite v1 paths and mechanisms
(compose profiles, `src/`, `DEV_*` config keys, docs since retired to git
history). Read the mechanisms as historical; the calls stand.

- **Session-backend abstraction REJECTED (2026-07-20).** No
  `VIBE_SESSION_BACKEND=tmux|shpool|none`: tmux is load-bearing here
  (preview window, hook DDS feed, sixel handling), and `DEV_AGENT_TMUX=0`
  already is the "none" backend.
- **Version-lock machinery DEFERRED (2026-07-20).** Dockerfile ARGs pin
  what upstream supports; if reproducibility ever bites, the cheapest step
  is a base-image digest pin, not a lockfile subsystem. (v2 note: recipe
  pins now ship inside the engine and move with engine releases.)
  (2026-07-25: the *opposite* direction — the floating agents going
  stale because the layer cache froze them — is now handled by the
  refresh token: a per-rebuild cache-buster on just the unversioned
  agent layers, persisted as a generation token on the project record.
  Superseded the same day by manifest pins: `image.agents` accepts
  `claude@2.1.220`, unversioned entries re-pull to latest on every
  rebuild — the pin lives in the manifest, so the deferred lockfile
  subsystem stays unbuilt.)
- **Own terminal multiplexer REJECTED (2026-07-21).** Sizing herdr: ~193K
  LoC of Rust whose hard core is vendored (libghostty-vt, portable-pty);
  its daemon layer buys detach/reattach that tmux gives us free. A shell
  multiplexer is definitionally impossible. If agent *orchestration* is
  ever wanted, run an orchestrator inside the container instead of growing
  one.
- **Herdr ceded ledger (2026-07-21, amended same day).** Permanently ceded:
  a unified live dashboard of agent screens, a programmatic agent control
  API (spawn/read/wait), real state fidelity for hookless agents (they cap
  at running/exited). "Cross-project fleet view" was deliberately amended
  by Chris to a render-only glance (today: the sidebar) — nothing that
  drives, schedules, or controls agents is in scope.
- **`vibe open` RETIRED (2026-07-21).** The native-terminal-panes adapter
  (Windows Terminal layouts, planned layout DSL, WezTerm adapter) is
  superseded by `vibe tui`: host tmux owns layout/theme, container
  per-agent tmux sessions own persistence. Composing `vibe agent` /
  `shell` panes manually in any terminal remains the documented no-tmux
  fallback.
- **Command surface is ABI (Chris).** Trial tools ride the palette /
  `vibe exec`; a top-level verb must be earned with harness logic.
- **Container user stays `vscode`.** It comes from the devcontainers base
  images and is load-bearing ABI (`USER vscode` contract,
  `/home/vscode/.agents` paths).
- **Spaces machinery stays minimal (2026-07-21).** Project picker =
  live-sessions-only `choose-tree`; tui conf ownership =
  first-owner-authoritative with a skew warning. (v2 note: the engine
  registry now exists for trust records — the picker staying
  live-sessions-only is a UI call, not a registry ban.)
- **The manifest/repo author is trusted (2026-07-22).** The security
  boundary defends against a compromised container process, not a hostile
  project author: hardening invariants stay enforced as
  container-tampering guardrails, and setup warns to read a repo's
  `.vibe/` before first `vibe up`. Exhaustively containing a malicious
  project author is out of scope by design (docs/security.md).
- **No nested sandboxes (2026-07-22).** bwrap cannot create namespaces
  under cap_drop ALL, so inner agent sandboxes (Claude /sandbox, codex
  sandbox modes) don't run here; codex is seeded danger-full-access —
  the container is the isolation layer. Different trust levels get
  different OUTER containers (`--jailed`), never a nested inner sandbox
  (docs/security.md "Inner agent sandboxes" in the v1 line; the call
  carries to v2).
- **Editor-as-surface; yazi dropped (2026-07-26, Chris; half
  superseded same day).** The yazi half stands: never a vibe-shipped
  yazi config, and the viewer stays glue-level replaceable with
  verdict capture engine-owned. The zero-shipped-config half was
  falsified by the first dogfood (a stock WSL host has neither nvim
  nor lazygit — the "plugin-free floor" did not exist) and is
  superseded by the bundled review stack: an opinionated, pinned
  nvim/lazygit bundle in the CONTAINER image, config in the payload,
  theme generated from theme.go. That differs from the v1
  layered-config trap the revival verdict rejected in kind, not just
  degree — image-pinned bytes, one config layer, no runtime plugin
  manager. Full contract: docs/tui-layout.md "Editor surfaces
  (second call)"; customization stays a `tui.conf` rebind, not a
  knob.
- **Roster stays render-only — no dismiss affordance (2026-07-25).** A
  ctrl-c-quit agent left a ✗ viewer window needing manual reaping, and
  "add dismiss to the roster" was considered and rejected: it would put
  an interactive (and destructive) control on the render-only fleet
  glance (see the herdr ceded-ledger record), fight tmux's stock
  right-click pane menus, and institutionalize a cleanup chore instead
  of removing it. "Treat exit 130 as clean" was also rejected as
  unimplementable: a tmux client exits 0 regardless of the pane
  command's status (verified on 3.7b), so agent exit codes never cross
  the inner-tmux boundary and the host cannot see a 130. The fix that
  shipped keys on recorded truth instead: the pane-died hook self-cleans
  a dead viewer whose @vibe_state is exited* (the run's own end was
  recorded — the death is explained), and corpses keep to unexplained
  deaths, now with a close hint beside the respawn hint.

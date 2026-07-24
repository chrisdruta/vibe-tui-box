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
  The v1 implementations in git history are the reference — this is a
  revival against the v2 payload/toolchain model, not a design from
  scratch.

- **revdiff trial verdict (pending dogfood).** revdiff was the v1 trial
  diff-review surface; it gets a top-level verb only if it earns harness
  logic (annotation capture — its annotations-to-stdout channel may
  eventually absorb the `vibe review` A/R verdict flow). If revdiff
  disappoints, the fallback is the ~1-day yazi diff-toggle Lua plugin
  (existence proof: vscode-git-gutter.yazi on the v1-pinned 26.5.6);
  other spare parts (fzf change-preview glue, diffnav) are recorded in
  git history.

- **Host conveniences.** v1's install-tmux.sh (pinned tmux source build
  for hosts below 3.4) and start-ollama.sh. Revive on demand.

- **TUI: consume the engine renderers (reframed 2026-07-24).** Of the
  hidden renderers only `_state` has a consumer (the status line polls
  it). `_sidebar`/`_fleet` render engine truth the shell sidebar
  cannot see — container running/stale vs the approved candidate, the
  dev-mode marker, pending-request count, cold registered projects —
  and two pieces are missing before sidebar.sh can afford to call
  them: (1) an engine→tui event channel: state-mutating commands
  (up/down/rebuild/request decisions, broker poll) bump a serial option
  on the vibe socket the way state-render.sh bumps
  `@vibe_state_serial`, so engine data gets a freshness signal (a
  docker-events watcher can later cover out-of-band container deaths —
  this subsumes the old "event-driven sidebar refresh" item); (2) a
  refresh policy: engine calls run on serial change or the slow tick,
  never the 2s frame. (Session→project identity landed 2026-07-24:
  `vibe tui` stamps `@vibe_project` with the full ID, so the render
  loop can address `_sidebar --project` directly. `_statusline` was
  pruned the same day — the container-side statusline won its seat.)
  The same channel unlocks the richer roster — container-side agent
  sessions without host windows via `vibe ps` (deferred from
  agent-session slice 3). Product call still open: does the sidebar's
  fleet section list COLD registered projects (that is `_fleet`'s real
  consumer — a render-only glance whose click could dispatch `up`;
  brushes the live-sessions-only picker record).

- **TUI layout pass (wanted 2026-07-24; Chris).** A deliberate
  make-it-comfy pass over the whole tui chrome, with the status
  bars/statusline as first-class scope: move and remake them (where
  they live — top/bottom, per-window vs server-wide, whether the tab
  strip and the state/status information share a bar or split), grow
  their functionality, and make the result customizable — user-level
  knobs in the spirit of `@vibe_sidebar_w` (bar position, segment
  selection/order, theme accents) rather than a fork-the-conf story.
  Same pass covers the rest of the layout: default arrangement (agent
  pane / bottom dock / sidebar proportions, collapse behavior), what
  information lives where (agent dots and roster vs engine state such
  as stale-candidate and pending requests — decide together with the
  renderer-consumption entry above), and the resize story (fit-mode
  snapping vs proportional stretch). Write the layout spec first, mock
  it in tmux second, wire scripts last — layout arithmetic is where
  the sidebar bugs have lived (click-map skew, dot wrapping, resize
  ballooning).

- **tui follow-ups (low priority).** Review-as-split
  — images in a tmux split survive redraws only via kitty-graphics Unicode
  placeholders, which need the OUTER terminal to speak kitty graphics
  (Windows Terminal is sixel-only), so the revisit trigger is a
  kitty-capable frontend becoming real (then test
  `chafa -f kitty --passthrough tmux`).

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

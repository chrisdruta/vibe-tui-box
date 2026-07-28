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
  the verdict-capture and image halves.) (2026-07-27: the IMAGE half
  shipped as the redesign wanted — ctrl+click preview window,
  docs/tui-layout.md "Preview window": chafa re-pinned, show-image.sh
  revived host-pane-single-layer (no nesting, no passthrough), loud
  low-fi degradation below tmux 3.7. No yazi, no `vibe show` verb —
  the click IS the verb. This entry narrows to verdict capture.)
  (2026-07-27, Chris: the VERDICT half is DROPPED, not deferred — no
  A/R capture revival at all. The flow never earned its keep in v1
  dogfood the way clipboard and viewing did; if structured verdicts
  ever matter again, the decisions-JSONL contract stays readable in
  the v1 git history. ENTRY CLOSED — the revival is fully resolved:
  clipboard restored, viewing shipped, images shipped, verdicts
  dropped.)

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
  (2026-07-27: the A/R absorption target is gone — verdict capture
  dropped outright, revival entry above — so revdiff must earn a verb
  on annotation capture's own merits or retire at the trial verdict.)
  (2026-07-27, Chris: trial verdict rendered — RETIRED, same hour as
  the verdict drop and for the same reason: lazygit owns viewing,
  telling the agent what to change owns feedback, and line-anchored
  annotation never surfaced as a missing workflow in dogfood. Not in
  the v2 image, never gets a verb; the binary pin and palette glue
  stay readable in v1 history. ENTRY CLOSED.)

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

- **Agent-management follow-ups (recorded 2026-07-26 beside the
  launch-surface contract, tui-layout.md "Launch surfaces").** (1)
  Per-session stop/restart addressing: the palette's items address
  only the default `agent` session; with multiple CLIs live, the
  `vibe ps` popup is the likely door (sidebar rows stay render-only
  by decision record). (2026-07-28, Chris: the STOP half shipped as
  the right-click bar menu instead — agent tabs and ghost cells open
  agent-menu.sh (stop / open viewer / close viewer only), dispatching
  address-direct over `vibe _stop` → agent-session.sh `kill`; the ps
  popup stays read-only and sidebar rows stay render-only. Restart
  stays palette-default-only: per-address restart needs the
  address→flags reverse-map, deferred until dogfood asks.) (2) The
  awaiting-input dot upgrade: with N
  claude background sessions behind one pane, the hook-fed dot
  approximates "any session needs me" — if dogfood shows it idle
  while claude's agents screen says Needs input, feed the dot (and
  maybe `▲n`) from the statusline JSON's awaiting-input count.

- **Sidebar: event-driven refresh + the cold-project click call.**
  The two bits deferred from the consume-the-renderers work: (1) a
  docker-events watcher for out-of-band container deaths — the watch
  channel (`vibe _watch`, prototyped 2026-07-26, tui-layout.md "The
  watch channel") now covers INNER change classes (sessions, viewers,
  state records) in ~1-2s, but container-level death still rides the
  `@vibe_engine_refresh` slow tick (30s; also listed on the roadmap's
  after-v1.0 line) — a docker-events subscription in the same daemon
  is the natural next slice; (2) cold registered projects render
  as dim non-clickable rows — the product call whether their click
  dispatches `up` stays open (brushes the live-sessions-only picker
  record).

- **Watch channel follow-ups (prototyped 2026-07-26).** Upgrade
  the container sentinel from its 1s local fingerprint poll to a
  control-mode tmux client on the inner server — true push, plus
  window-level granularity — once the channel earns it; the sentinel
  protocol (E/H lines over one exec stream, stdin leash) was shaped so
  only the script body changes. (The signal-filter asymmetry this
  dogfood surfaced — hookless `running` outranking hooked `idle` — is
  RESOLVED by the roster decision, same day: every live agent is a
  row, idle renders dim, so both read as present.)

- **Claude-plugin future content.** The vibe claude-plugin
  (`payload/container/claude-plugin/`, loaded per session with
  `--plugin-dir` from the read-only payload) could grow an
  environment SKILL distilled from the seeded `.vibe/AGENTS.md` —
  teaching agents the container environment and broker protocol as a
  skill instead of (or beside) instruction-file prose. Demand-gated
  on agents visibly fumbling what AGENTS.md already says.
  (The branch-review remainder that used to live here closed 9/9 on
  2026-07-26 — see CHANGELOG; the plugin's marketplace-install
  rejection moved to the decision records.)

- **tui follow-ups (low priority).** Review-as-split
  — images in a tmux split survive redraws only via kitty-graphics Unicode
  placeholders, which need the OUTER terminal to speak kitty graphics
  (Windows Terminal is sixel-only), so the revisit trigger is a
  kitty-capable frontend becoming real (then test
  `chafa -f kitty --passthrough tmux`). (2026-07-27: premise
  weakened — the preview window proved native sixel ingest in a host
  pane survives adjacent-pane redraws on the 3.7 floor, and its WINCH
  repaint covers the resize-clear; a split variant could ride
  show-image.sh as-is. Kitty placeholders remain only a fidelity
  play, same trigger.)

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
- **Marketplace-style claude-plugin install REJECTED (2026-07-26).**
  The vibe claude-plugin loads per session with `--plugin-dir` from
  the read-only payload; a marketplace-style install would copy
  plugin bytes onto the agent-state volume, recreating the
  unpinned-mutable-plugin-state defect class. Verified in-container:
  hooks fire from a write-protected dir, commands namespace as
  `/vibe:…`, and the only volume write is an empty per-plugin `data/`
  dir. A plugin cannot express `statusLine`, `autoMemoryEnabled`,
  `autoUpdates`, or `sandbox` — those stay in the thin `--settings`
  file; a pure-plugin end state is off the table upstream-permitting.
- **Parallel agent instances stay inside the CLI (2026-07-26, Chris).**
  The tui's launch unit is the installed CLI, one per project — the
  `+` chooser never mints a second instance of a running CLI. The
  expectation behind "add another claude" (a separate task-shaped
  thing with its own lifecycle) is exactly Claude Code's built-in
  background-session manager (`←` at the prompt: describe a task →
  its own session, Needs input / Working / Completed triage,
  survives the terminal); a second instance at the tmux or container
  layer would reimplement that screen one level down with worse
  information — and hand two writers one working tree.
  `vibe agent -s NAME` stays a CLI-only power tool (deliberate,
  named, knowingly shares the checkout). A UI door for parallel
  instances returns only as container-per-instance with its OWN
  checkout (worktree/volume clone, branch, merge-back — see
  "Productize worktrees"), demand-gated on claude's own isolation
  visibly not covering a real dogfood need.

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

# Backlog

Ideas accepted but not scheduled. Items graduate into a release when they get
designed; entries here are one paragraph of intent, not a spec. Shipped work
moves to the CHANGELOG (details live in git history); settled calls worth not
relitigating live in the decision-record section at the bottom — as revisable
records, not fences.

## Open

- **v2: clean-slate Go engine.** Architecture in
  [docs/architecture-v2.md](docs/architecture-v2.md) (2026-07-23; awaiting
  sol adversarial review): single attested binary embedding the container
  payload; projects author a closed `vibe.yaml` schema and the binary
  *renders* compose (no user-authored compose, no scanner); release-based
  distribution replaces the git-mirror/submodule machinery; tmux stays the
  UI; container scripts stay bash. No migration — old installs reinstall,
  projects re-init. Rationale for the retired strangler approach:
  [docs/go-port-plan.md](docs/go-port-plan.md). Step 0 (Go toolchain,
  module skeleton, CI) is in the tree.

- **Reduced-trust profile for unattended runs (`vibe agent --jailed`).**
  Promised in docs/security.md ("planned but not implemented"). Design
  direction (2026-07-22): a compose-profile sibling service
  (`profiles: ["jailed"]`, same image, the existing `build`-profile pattern)
  launched by `vibe agent --jailed` — read-only workspace bind (or a
  disposable worktree bind), scratch agent-state volume so no OAuth
  tokens/session history ride along, no `.env` loading, network `none` or
  routed through the egress sidecar (next entry). One flag replaces the
  manual recipe in security.md; doctor verifies the reduced posture; per the
  2026-07-22 security review it should also reject new services, privileged,
  absolute binds, root, and added caps. This is the "different boundary per
  trust level" answer to inner sandboxes (rejected alternatives recorded in
  docs/security.md). The credential half landed early — the `GH_TOKEN`
  passthrough is gone (2026-07-20) — so remaining scope is the `DEV_AUTO_*`
  posture plus the compose profile. Demand-gated on the first real
  unattended run.

- **Per-project egress visibility (wanted 2026-07-22).** A per-project VIEW
  of what the container talks to — visibility first, enforcement later, the
  guardrail-not-jail philosophy applied to the one surface security.md
  admits is wide open. Sketch: (1) a DNS-forwarder sidecar per project
  (compose service + `dns:` on the dev service) whose query log IS the
  project's domain ledger — name-level, no MITM, no proxy env vars for tools
  to ignore; (2) an in-container live-socket sampler (`ss`/proc-net, works
  unprivileged; packet capture is off the table by design — cap_drop ALL
  removes NET_RAW) attributing current connections to processes; (3)
  surfaced in the tui — palette window / `vibe exec` trial first, not a
  top-level verb until it earns harness logic. Accepted blind spots:
  direct-to-IP and DoH skip the DNS log (the sampler still shows those
  IPs). Upgrade path: the sidecar seat is exactly where an L7 allowlist
  proxy would sit (2026-07 research: dynamic allowlists > static iptables) —
  that enforcement half is what `--jailed`'s network posture consumes.

- **Productize worktrees.** Parallel worktrees work but are manual: a
  differently-named worktree directory gets its own
  `agent-state-<basename>` volume, so fresh agent logins unless the user
  edits the volume `source=` themselves (docs/agent-state.md). Add
  `vibe worktree create/list/remove` plus an explicit state-scope choice
  (default: today's per-workspace isolation; opt-in: worktrees of one repo
  share a volume via explicit `source=` in the seeded compose override).
  Before coding, write the command CONTRACT (2026-07-21 review): branch
  create vs attach, worktree placement/naming/collisions, whether `.vibe`
  config is inherited or regenerated, whether the shared-state choice edits
  committed compose.yaml or a local override, what `remove` does to running
  containers and host tmux sessions, and dirty/unmerged refusal rules.
  Hard lines already settled: state sharing is NEVER inferred from
  repository relationship (explicit `source=` opt-in only, recorded as
  project-owned config), and removal never deletes agent-state volumes
  automatically. Constraint: the `agent-state-<workspace-basename>`
  derivation is ABI (AGENTS.md).

- **revdiff trial verdict (pending dogfood).** revdiff (palette `r` /
  `vibe exec revdiff`) is the trial diff-review surface; it gets a
  top-level verb only if it earns harness logic (annotation capture — its
  annotations-to-stdout channel may eventually absorb the `vibe review`
  A/R verdict flow). Post-trial consolidation question: `show` and
  `review` keep their seats meanwhile (show = image plumbing, review =
  locked read-only browser + verdicts + the prefix+i hook window). If
  revdiff disappoints, the fallback is the ~1-day yazi diff-toggle Lua
  plugin (existence proof: vscode-git-gutter.yazi on our pinned 26.5.6);
  other spare parts (fzf change-preview glue, diffnav) are recorded in git
  history.

- **tui follow-ups (low priority).** Event-driven sidebar refresh
  (replacing the serial-gated tick); a richer sidebar agent roster backed
  by `vibe ps` (covers container sessions without windows); review-as-split
  — images in a tmux split survive redraws only via kitty-graphics Unicode
  placeholders, which need the OUTER terminal to speak kitty graphics
  (Windows Terminal is sixel-only), so the revisit trigger is a
  kitty-capable frontend becoming real (then test
  `chafa -f kitty --passthrough tmux`).

- **Open flag:** should the root AGENTS.md import `@.vibe/AGENTS.md` the
  way install.sh tells consumers to?

## Decision records (settled calls — revisable with new evidence)

- **Session-backend abstraction REJECTED (2026-07-20).** No
  `VIBE_SESSION_BACKEND=tmux|shpool|none`: tmux is load-bearing here
  (preview window, hook DDS feed, sixel handling), and `DEV_AGENT_TMUX=0`
  already is the "none" backend.
- **Version-lock machinery DEFERRED (2026-07-20).** Dockerfile ARGs pin
  what upstream supports; if reproducibility ever bites, the cheapest step
  is a base-image digest pin, not a lockfile subsystem.
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
  `shell` / `review` panes manually in any terminal remains the documented
  no-tmux fallback.
- **Command surface is ABI (Chris).** Trial tools ride the palette /
  `vibe exec`; a top-level verb must be earned with harness logic.
- **Container user stays `vscode`.** It comes from the devcontainers base
  images and is load-bearing ABI (`USER vscode` contract,
  `/home/vscode/.agents` paths).
- **Spaces machinery stays minimal (2026-07-21).** Project picker =
  live-sessions-only `choose-tree` — NO project registry (dormant-checkout
  discovery deliberately declined); tui conf ownership =
  first-owner-authoritative with a skew warning.
- **The compose/repo author is trusted (2026-07-22).** The security
  boundary defends against a compromised container process, not a hostile
  project author: hardening invariants stay enforced as
  container-tampering guardrails, host-read/exec compose features are
  refused best-effort, and setup warns to read a repo's `.vibe/` before
  first `vibe up`. Exhaustively containing a malicious compose author is
  out of scope by design (docs/security.md "What is trusted" — Docker
  itself does not treat compose as a security boundary).
- **No nested sandboxes (2026-07-22).** bwrap cannot create namespaces
  under cap_drop ALL, so inner agent sandboxes (Claude /sandbox, codex
  sandbox modes) don't run here; codex is seeded danger-full-access —
  the container is the isolation layer. Different trust levels get
  different OUTER containers (`--jailed`), never a nested inner sandbox
  (docs/security.md "Inner agent sandboxes").

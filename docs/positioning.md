# Positioning and non-goals

What this project is, what it deliberately is not, and why — relative to the
broader agent-tooling landscape. Tools are named only as examples of a
category; the categories are durable, the products churn.

## The layer this harness occupies

Agent tooling has settled into three layers:

1. **Agent harnesses / loops** — programs that talk to a model API and run
   tools: Claude Code, Codex CLI, Grok Build, and first-party open harnesses
   in the pi / Hermes mold. Each owns its conversation loop, tool set, and
   credential store.
2. **Orchestrators / fleet UIs** — apps that run several of those CLIs in
   parallel and add review UX, typically one git worktree per task
   (Conductor, Orca, and kin). They isolate *work*, not *trust*: every
   workspace runs as the host user and inherits the machine-level logins.
3. **The environment** — where the agent process actually executes and what
   it can reach.

This project is layer 3: a hardened, reproducible container that agent
CLIs run *inside*, pinned into each project as a git submodule. It does not
compete with the layers above. Minimal harnesses explicitly tell users to
bring their own container for boundaries — this is that container — and an
orchestrator could drive containers like this instead of bare worktrees.

"The environment" includes the terminal the agent lives in, so the harness
also owns the affordances that make an agent workable *inside* that
environment: getting a host clipboard image through the boundary
(`vibe clip`), rendering pixels a TUI cannot (`vibe show`, the tmux preview
window), and looking at what an agent produced without leaving the terminal
(`vibe review` — yazi with project-owned config; verdict recording is a
seeded keybinding the project owns, not harness code). The line stays where
it always was:
anything that *drives* the agent — loops, schedulers, task state machines,
multi-agent review pipelines — belongs to layers 1–2 and is a non-goal;
project skills may build such flows *on top of* these affordances.

## Principles

- **Isolate trust, not just work.** One container per project: non-root, all
  capabilities dropped, no Docker socket, no host home. Worktrees organize
  parallel work; they do not contain a misbehaving process.
- **Explicit secret loading.** Secrets enter one process through
  `env-run.sh`; nothing auto-sources `.env` into shells.
- **Agent-native, per-project auth.** Each CLI manages its own login,
  persisted in that project's state volume. The harness points config dirs
  at the volume and otherwise stays out of credential handling.
- **Repo-agnostic harness, thin project ownership.** Shared scripts carry no
  project specifics; projects own their `compose.yaml`, `config.env`,
  and hooks.
- **Opt-in over baked-in.** Codex, Grok, Features, `--cold`: additive and
  inert unless enabled.
- **Reviewable supply chain.** Consumers pin a commit; updating the harness
  is an explicit, diffable step.

## Non-goals

- No orchestration UI, scheduler, or fleet manager.
- No first-party agent loop or model API client.
- No centralized credential store, auth broker, or token proxy.
- No bind-mounting host credentials (`~/.claude`, SSH keys, keychains) into
  containers.

## Why logins are per-project

Orchestrator-class tools advertise "log in once, use everywhere". That is not
a feature they built — it is a consequence of running every agent as the host
user, so all workspaces share the machine-level credential state. This
harness's per-project logins are likewise not a missing feature: they are the
price of the per-project trust boundary that is its whole point.

Each project's [state volume](agent-state.md) is a blast-radius cell. A
compromised or misbehaving agent run in one project cannot read another
project's OAuth tokens or session history. Centralizing would trade that
away: one token, valid everywhere, readable from any project; cross-project
history exposure; concurrent containers doing last-write-wins on shared JSON
state and racing token refreshes; a logout in one project logging out all.

Mechanisms considered and rejected:

- **Bind-mounting host credential dirs** breaks the no-host-home invariant,
  and macOS Keychain-stored tokens do not travel into a Linux container
  anyway.
- **A credential vault or token proxy** re-implements what the CLIs already
  do, and third-party brokering of subscription OAuth is the ToS-fragile
  lane — running the real CLI with your own login is the sanctioned pattern.
- **Credential-only sharing** (symlinking just the token files into a shared
  volume) is fragile: a CLI that writes credentials via atomic rename
  silently replaces the symlink and forks the state.

The escape hatch already exists and is deliberately manual: a project can
point its volume `source=` at a shared name to pool logins across projects —
see [agent-state.md](agent-state.md) and the trade-off note in
[security.md](security.md).

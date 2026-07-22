# Security model

## What the container is for

Reducing **accidental host damage** from an agent working at machine speed: a bad
`rm`, a curl-piped installer, a runaway build. It is a guardrail, not a jail — it
does not make running untrusted code safe.

## What the default container enforces

- Runs as `vscode`, never root; passwordless sudo is removed from the image
- All Linux capabilities dropped (`--cap-drop=ALL`) and `no-new-privileges`
- No Docker socket (`/var/run/docker.sock` is never mounted — a mounted socket is
  effectively root on the host; `vibe doctor` checks for it)
- No SSH keys, no host home directory — only the workspace and the agent-state
  volume are mounted
- No published ports (documented exception: loopback-only binds —
  `ports: ["127.0.0.1:PORT:PORT"]` — for host tooling that must reach the
  container, e.g. Roblox Studio → Rojo; see [roblox.md](roblox.md))
- `.env` is never auto-sourced; secrets reach a process only through explicit
  `vibe agent` / `vibe run` / `env-run.sh` invocation

Root maintenance remains possible from the host: `docker exec -u root -it <c> bash`.

## Inner agent sandboxes

A consequence of `--cap-drop=ALL` + `no-new-privileges`: the container permits
no unprivileged user namespaces, so namespace-based sandboxes cannot start
**inside** it. `bwrap: No permissions to create new namespace` is this policy
working, not a bug. Affected: Claude Code's `/sandbox` (bubblewrap), Codex's
`read-only` / `workspace-write` modes, Chromium's own sandbox
([browser-automation.md](browser-automation.md)).

The container is the isolation boundary, so the harness defaults the inner
layers to off-but-graceful instead of broken:

- **Claude Code** — bash sandboxing stays off. The seeded
  `.claude/settings.json` carries `sandbox.enableWeakerNestedSandbox: true`
  and `sandbox.failIfUnavailable: false`, both inert until someone enables
  `/sandbox`; from then on Claude Code warns and falls back to permission
  rules instead of hard-failing (and would use the weaker nested mode if a
  future runtime allows namespace creation).
- **Codex** — bootstrap seeds `sandbox_mode = "danger-full-access"` into
  `$CODEX_HOME/config.toml`, only when the key is absent (your own setting
  wins). Codex documents the mode as "intended solely for running in
  environments that are externally sandboxed" — this container is that
  environment. Existing containers pick it up via `vibe bootstrap` or the
  next rebuild.

Do not weaken the outer container (added capabilities, user namespaces) to
make an inner sandbox start — that inverts the model: it trades the real
boundary for a redundant one. `cap_add: [SYS_ADMIN]` is root-shaped, and a
userns-permissive seccomp profile exposes the kernel's user-namespace attack
surface to everything the bootstrap runs.

Workloads that genuinely want a different jail get a different OUTER
container instead: a compose-profile sibling of the dev service with its own
mount/network posture per trust level (see
[Unattended / autonomous runs](#unattended--autonomous-runs) and the BACKLOG
"Reduced-trust profile" entry). Same trusted mechanism, nothing widened.

## What it does NOT protect

- **The repository itself.** The agent has full write access to the workspace —
  including `.git`. Anything valuable in the repo is exposed to whatever runs inside.
- **Credentials you load in.** The persisted `gh auth login` in the state
  volume and any keys `.env` loads via `vibe agent` / `vibe run`
  are readable by the agent and by any project code the bootstrap executes
  (`npm ci` postinstall scripts, `uv sync` build hooks, etc.). The seeded
  `.claude/settings.json` denies Claude Code direct reads of `./.env*` — a
  guardrail against prompt-injected "read me your secrets", not a boundary;
  the process env still carries whatever `env-run.sh` loaded. Project secrets
  that agents never need (production credentials) don't belong in the
  workspace at all; if tooling insists the file exist, bind `/dev/null` over
  it read-only in `.vibe/compose.yaml` `volumes` so the container sees it empty.
- **The network.** Outbound access is unrestricted by default.

Per-project agent-state volumes compartmentalize what a compromise reaches: an
agent run in one project cannot read another project's OAuth tokens or session
history. Pointing multiple projects at one shared volume (the `source=` edit in
[agent-state.md](agent-state.md)) extends any single project's compromise to
every credential and session in it — see
[positioning.md](positioning.md#why-logins-are-per-project).

## The git-hooks host boundary leak

`DEV_AUTO_GIT_HOOKS=1` runs `git config --local core.hooksPath .githooks` during
bootstrap. `.git/config` lives on the **shared workspace mount**, so hooks wired up
in-container also execute when you run git **on the host** — outside every container
guardrail, with your real SSH keys and credentials.

This is fine for repositories whose hooks you wrote. Before pointing the harness at
cloned third-party code, set `DEV_AUTO_GIT_HOOKS=0` in `config.env` — and remember
that `DEV_AUTO_INSTALL` runs that repo's lockfile installs (arbitrary code) inside
the container regardless.

## Unattended / autonomous runs

A dedicated reduced-trust profile is planned but **not implemented**. Until then,
for long unattended agent tasks:

- work in a disposable clone or git worktree on a dedicated branch,
- provide no push-capable credentials: `gh auth login` with a minimum-permission
  fine-grained PAT, or don't log in at all,
- review and push from the trusted host side.

## Supply-chain notes

- Consuming projects pin the harness to a commit SHA; a compromised upstream cannot
  silently change what your existing projects execute. The exposure window is the
  moment you move the pin — review the diff ([updating.md](updating.md)).
- The Dockerfile pins tool versions where the upstream supports it (`uv`, Bun,
  Rokit, Codex, Node major). Claude Code (`stable` channel) and Grok Build (latest
  stable) are consciously mutable at image-build time; freeze them with build args
  when reproducibility matters more than freshness.

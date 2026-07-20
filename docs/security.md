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

## What it does NOT protect

- **The repository itself.** The agent has full write access to the workspace —
  including `.git`. Anything valuable in the repo is exposed to whatever runs inside.
- **Credentials you pass in.** `GH_TOKEN` (forwarded at container create) and any keys in `.env`
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
- provide no push-capable token and no cloud credentials (omit `GH_TOKEN` or use a
  minimum-permission fine-grained token),
- review and push from the trusted host side.

## Supply-chain notes

- Consuming projects pin the harness to a commit SHA; a compromised upstream cannot
  silently change what your existing projects execute. The exposure window is the
  moment you move the pin — review the diff ([updating.md](updating.md)).
- The Dockerfile pins tool versions where the upstream supports it (`uv`, Bun,
  Rokit, Codex, Node major). Claude Code (`stable` channel) and Grok Build (latest
  stable) are consciously mutable at image-build time; freeze them with build args
  when reproducibility matters more than freshness.

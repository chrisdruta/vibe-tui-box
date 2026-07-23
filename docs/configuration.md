# Configuration reference

Project-owned configuration lives next to the harness submodule and is seeded by
`install.sh`; edit it freely, it is never overwritten by harness updates.

When changes apply (diagram: [README → How it works](../README.md#how-it-works)):

| File                  | Read at                                            | Changes apply on            |
| --------------------- | -------------------------------------------------- | --------------------------- |
| `.vibe/compose.yaml`  | image build & container create                     | `vibe rebuild` (or down/up) |
| `.vibe/config.env`    | every container-side script run (via `lib.sh`)     | the next `vibe` command     |
| `.env`                | process launch by `env-run.sh`                     | the next `vibe agent`/`run` |
| `.vibe/project/*.sh`  | lifecycle (post-create once, post-start per start) | the next container (re)start |
| `.vibe/yazi/`         | each `vibe review` / preview-window launch         | the next review session     |

## `config.env`

Non-secret behavior toggles, sourced by the lifecycle scripts and `vibe agent`:

| Variable                | Default                       | Meaning                                            |
| ----------------------- | ----------------------------- | -------------------------------------------------- |
| `DEV_AGENT_CMD`         | `claude`                      | What `vibe agent` runs (may include arguments)      |
| `DEV_AGENT_TMUX`        | `1` (seeded; unset = `0`)     | `1`: `vibe agent` runs in a persistent tmux session — rerunning attaches, detaching (`Ctrl-b d`) keeps it alive |
| `DEV_AGENT_TMUX_SESSION`| `agent`                       | tmux session name used by `vibe agent`              |
| `DEV_ATTACH_TMUX_SESSION`| `services` (seeded commented-out) | Session `vibe attach` opens with no argument, and the session `vibe-svc` populates ([services.md](services.md)) |
| `DEV_BOOTSTRAP_STRICT`  | `1`                           | `1`: bootstrap steps and the project post-start hook fail loudly; `0`: warn and continue (a step whose tool is missing is skipped) |
| `DEV_AUTO_INSTALL`      | `1`                           | Enable lockfile-detected dependency installation   |
| `DEV_AUTO_GIT_HOOKS`    | `1`                           | Wire `.githooks/` into `core.hooksPath` (see [security.md](security.md)) |
| `DEV_AUTO_GIT_LFS`      | `1`                           | Repo-local `git lfs install` when LFS attributes exist |
| `DEV_ENV_FILE`          | `.env`                        | File loaded by `vibe agent` / `vibe run` / `env-run.sh` |
| `DEV_REQUIRED_COMMANDS` | `git gh jq rg uv claude` (+preset) | Commands `vibe doctor` requires                |
| `VIBE_PREVIEW_DIR`      | `/tmp`                        | Where `vibe show` (no argument) looks for the newest image |
| `VIBE_PREVIEW_GLOB`     | `*.png *.jpg *.jpeg *.gif *.bmp *.webp *.avif` | Space-separated glob list for that search (search filter only; the renderer sniffs the real format from file content) |
| `VIBE_REVIEW_DECISIONS` | unset                         | Send all `vibe-verdict` output to one fixed JSONL path instead of `.review-decisions.jsonl` beside the reviewed images. The review keybindings themselves live in project-owned `.vibe/yazi/` (see [usage.md](usage.md)). |

`DEV_AGENT_CMD` is split on spaces (no shell quoting) — fine for
`codex --model o4` but not for arguments containing spaces or env-var
wrappers. The escape hatch is a project script:
`DEV_AGENT_CMD=.vibe/project/run-my-agent.sh` runs whatever the script
execs, with full shell expressiveness inside the script instead of in
`config.env`.

## Bootstrap behavior

`post-create.sh` (also rerunnable as `vibe bootstrap`) detects manifests in the
repository root, in this order:

- `pyproject.toml` + `uv.lock` → `uv sync --frozen`
- `bun.lock` / `bun.lockb` → `bun install --frozen-lockfile`
- `pnpm-lock.yaml` → `pnpm install --frozen-lockfile`
- `package-lock.json` → `npm ci`
- `yarn.lock` → `yarn install --immutable`
- `rokit.toml` → `rokit install`
- `wally.toml` → `wally install`
- `.githooks/` → repo-local `core.hooksPath` (if `DEV_AUTO_GIT_HOOKS=1`)
- LFS attributes in `.gitattributes` → `git lfs install --local`

Only one JavaScript package manager runs (first match wins). A detected manifest
whose tool is missing is an **error** under `DEV_BOOTSTRAP_STRICT=1` — install the
tool via build args or project hooks, or set strictness to `0`. All steps are
idempotent and safe to rerun.

## Project lifecycle hooks

```text
.vibe/project/post-create.sh   # once per container creation, after bootstrap
.vibe/project/post-start.sh    # every container start; keep idempotent
```

Put migrations, code generation, MCP setup, or service startup here. The harness
itself never starts project services and does not assume a process manager;
long-running dependencies are either compose sidecars in the project's
`compose.yaml` (they start with `vibe up`, appear in `vibe status`, and are
removed by `vibe down`; their named volumes survive) or workspace processes
`post-start.sh` stands up with `vibe-svc` (`vibe attach` is the door in) —
the full chooser is [services.md](services.md).

## The compose override (`.vibe/compose.yaml`)

The container is defined by the harness base
(`.vibe/harness/src/compose/base.yaml` — workspace mount, agent-state volume,
hardening, environment) with the project's `.vibe/compose.yaml` merged on
top. `vibe config` prints the merged result. Two services: `base` is the
build-only recipe for the shared harness image (tagged
`${VIBE_PROJECT_NAME}-base`); `dev` is the container that runs it. Build
args live under `base` and merge per key:

```yaml
services:
  base:
    build:
      args:
        BASE_IMAGE: mcr.microsoft.com/devcontainers/base:debian
        INSTALL_CLAUDE_CODE: "true"  # the default agent
        INSTALL_CODEX: "false"   # OpenAI Codex CLI — "true" also installs Node
                                 # (npm-distributed; bundles the Codex plugin for Claude Code)
        INSTALL_GROK: "false"    # xAI Grok Build (standalone binary, no Node)
        INSTALL_NODE: "false"
        INSTALL_BUN: "false"
        INSTALL_ROKIT: "false"
        INSTALL_GO: "false"      # Go toolchain (official tarball, /usr/local/go)
```

The seeded `compose.yaml` carries every toggle as a live line at its
current value — flip and `vibe rebuild`. Each toggle is its own cached
Docker layer, so a flip rebuilds only from that layer down.

`BASE_IMAGE` accepts any image satisfying the base-image contract
(Debian-family apt, a `vscode` user, amd64+arm64) — see
[extending.md](extending.md#swapping-the-bottom-of-the-chain-base_image).

Versions are pinned as Dockerfile ARGs (`UV_VERSION`, `BUN_VERSION`, `ROKIT_VERSION`,
`CODEX_VERSION`, `NODE_MAJOR`, `YAZI_VERSION`, `GO_VERSION`) and
overridable per project without touching the submodule. `CLAUDE_CODE_VERSION`
(default `stable`) and `GROK_VERSION` (default latest stable) are the
consciously mutable components — set concrete versions to freeze them.

Policy: small CLI tools may be build arguments; everything else — apt
packages, Blender, browser libraries — is a **project image extension**: an
optional `.vibe/Dockerfile` chained onto the shared image (`dev` then sets
`image: ${VIBE_PROJECT_NAME}-dev` + a `build:` block). Mechanism, contract,
and rebuild semantics: [extending.md](extending.md); worked examples:
`examples/extensions/`. Long-running service dependencies (databases) are
project compose services, not image content.

Interpolation variables exported by the launcher for compose files:
`VIBE_PROJECT_NAME` (the per-checkout identity `vibe-<basename>-<suffix>`
from the host trust record — see [agent-state.md](agent-state.md)),
`VIBE_WORKSPACE_BASENAME`, `VIBE_REPO_ROOT`, `VIBE_USER_UID`.

## Claude Code project settings

`install.sh` seeds `<project>/.claude/settings.json` (only when the project has
none) wiring a statusline — `user ➜ dir (branch ✗) · model (effort) · context%` —
plus a per-subagent statusline, the image-preview hooks, and a `sudo`/`su`
permission deny (sudo does not exist in the container; denying skips doomed
attempts). The scripts live in the harness (`src/scripts/statusline.sh`,
`src/scripts/subagent-statusline.sh`), so they update with the submodule. To adopt
them in a project with existing settings, merge the keys from
`src/templates/claude-settings.json`.

`install.sh` also seeds `.vibe/AGENTS.md` — container rules for agents;
import it from the project's root `CLAUDE.md`/`AGENTS.md` with a
`@.vibe/AGENTS.md` line (see [onboarding.md](onboarding.md)).

## Secrets

Nothing auto-sources `.env`; `.bashrc` is never modified. Load explicitly:

```bash
vibe agent            # loads DEV_ENV_FILE, then runs DEV_AGENT_CMD
vibe agent --cold     # same, but without repo instruction files (see usage.md)
vibe run codex        # same, for any command
.vibe/harness/src/scripts/env-run.sh some-command   # inside the container
```

Agent API keys (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `XAI_API_KEY`) belong in the
project `.env`. For unattended runs, use minimum-permission tokens or none.

### GitHub access

Preferred: a **per-project fine-grained PAT** — single-repository access, an
expiry, and only the permissions below — pasted into `gh auth login` inside
the container (choose HTTPS; SSH never uses the PAT and the container has no
SSH keys).
`GH_CONFIG_DIR` points into the state volume, so the login persists across
rebuilds and stays compartmentalized per project, like the agent logins.

The login is also the opt-in for git wiring: on every container start,
`post-start.sh` checks whether gh is logged in and only then wires gh as
git's credential helper and rewrites `git@github.com:` remotes to HTTPS
(container-side only — the container has no SSH keys, so an SSH-cloned repo
shared with the host would otherwise be push-dead in here; host git is
untouched). Never logged in → nothing is wired. `vibe doctor` reports the
state either way.

#### Fine-grained PAT quick reference

Create at GitHub → Settings → Developer settings → Fine-grained tokens
(<https://github.com/settings/personal-access-tokens/new>). Repository
access: **Only select repositories** → the one project repo. Set an
expiration. Repository permissions:

| Permission      | Access         | Enables                                             |
| --------------- | -------------- | --------------------------------------------------- |
| Contents        | Read and write | clone, pull, push, branches, merges, releases       |
| Pull requests   | Read and write | `gh pr create/view/comment/merge`                   |
| Actions         | Read-only      | `gh run list/view/watch` — following CI runs        |
| Commit statuses | Read-only      | `gh pr checks`, commit status on PRs                |
| Workflows       | Read and write | pushes that touch `.github/workflows/` (see below)  |
| Metadata        | Read-only      | added automatically (required)                      |

**Workflows is the conscious trade in this set.** Without it, GitHub rejects
any push containing changes under `.github/workflows/` — annoying in repos
where CI files are part of normal development. With it, whatever runs in the
container can modify CI, which is a privilege-escalation path (a malicious
change to a workflow file executes with the repository's Actions
credentials). Grant it for interactive work on repos whose CI you edit; leave
it off for unattended runs and low-trust projects, where a rejected
workflow-file push is the guardrail working.

`gh auth login` (paste the PAT when prompted) is the **only** supported path —
the login persists in the per-project state volume, so it is a one-time step
per project. There is deliberately no `GH_TOKEN` passthrough from the host:
container env is baked at create time and visible to every process, which is
exactly what the explicit-secret-loading rule exists to avoid (removed
2026-07; rotation is just `gh auth login` again).

If you keep a reference copy of the PAT in the project `.env`, store it under
a **neutral name** (e.g. `GITHUB_PAT=...`), never `GH_TOKEN` or
`GITHUB_TOKEN`: `gh` gives those env vars precedence over the stored login,
and `vibe agent` / `vibe run` source `.env` into the process — so the stock
names would silently override your `gh auth login` identity in every agent
session (and `gh auth login` refuses to run while they are set).

## Ports and host networking

No ports are published by default. To let the container reach services on the host
(e.g. a local LLM server), add to the project's `compose.yaml`:

```yaml
services:
  dev:
    extra_hosts:
      - "host.docker.internal:host-gateway"
```

This is deliberately opt-in per project — see [local-models.md](local-models.md).
Publishing a port for host tooling that must reach the container stays
loopback-only (`ports: ["127.0.0.1:X:Y"]` — see [roblox.md](roblox.md)).

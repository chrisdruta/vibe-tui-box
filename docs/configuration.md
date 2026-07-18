# Configuration reference

Project-owned configuration lives next to the harness submodule and is seeded by
`install.sh`; edit it freely, it is never overwritten by harness updates.

## `config.env`

Non-secret behavior toggles, sourced by the lifecycle scripts and `vibe agent`:

| Variable                | Default                       | Meaning                                            |
| ----------------------- | ----------------------------- | -------------------------------------------------- |
| `DEV_AGENT_CMD`         | `claude`                      | What `vibe agent` runs (may include arguments)      |
| `DEV_AGENT_TMUX`        | `1` (seeded; unset = `0`)     | `1`: `vibe agent` runs in a persistent tmux session — rerunning attaches, detaching (`Ctrl-b d`) keeps it alive |
| `DEV_AGENT_TMUX_SESSION`| `agent`                       | tmux session name used by `vibe agent`              |
| `DEV_BOOTSTRAP_STRICT`  | `1`                           | `1`: bootstrap steps fail loudly; `0`: warn and continue |
| `DEV_AUTO_INSTALL`      | `1`                           | Enable lockfile-detected dependency installation   |
| `DEV_AUTO_GIT_HOOKS`    | `1`                           | Wire `.githooks/` into `core.hooksPath` (see [security.md](security.md)) |
| `DEV_AUTO_GIT_LFS`      | `1`                           | Repo-local `git lfs install` when LFS attributes exist |
| `DEV_ENV_FILE`          | `.env`                        | File loaded by `vibe agent` / `vibe run` / `env-run.sh` |
| `DEV_REQUIRED_COMMANDS` | `git gh jq rg uv claude` (+preset) | Commands `vibe doctor` requires                |
| `VIBE_PREVIEW_DIR`      | `/tmp`                        | Directory the image viewer watches (see [usage.md](usage.md)) |
| `VIBE_PREVIEW_GLOB`     | `*.png *.jpg *.jpeg *.webp`   | Space-separated glob list for the watch directory  |
| `VIBE_PREVIEW_DECISIONS`| unset (viewer is passive)     | Set to a JSONL path to enable review mode: approve/reject (+ reject note) verdicts append there. `vibe review DIR` enables it per batch without config. |

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
.devcontainer/project/post-create.sh   # once per container creation, after bootstrap
.devcontainer/project/post-start.sh    # every container start; keep idempotent
```

Put migrations, code generation, MCP setup, or service startup here. The harness
itself never starts services and does not assume a process manager; prefer Compose
sidecars for databases and long-running dependencies.

## Image build arguments (`devcontainer.json`)

```jsonc
"args": {
  "BASE_IMAGE": "mcr.microsoft.com/devcontainers/base:debian",
  "INSTALL_CLAUDE_CODE": "true",
  "INSTALL_CODEX": "false",   // OpenAI Codex CLI (pulls in Node; also bundles the Codex plugin for Claude Code)
  "INSTALL_GROK": "false",    // xAI Grok Build
  "INSTALL_NODE": "false",
  "INSTALL_BUN": "false",
  "INSTALL_ROKIT": "false"
}
```

Versions are pinned as Dockerfile ARGs (`UV_VERSION`, `BUN_VERSION`, `ROKIT_VERSION`,
`CODEX_VERSION`, `NODE_MAJOR`) and overridable per project without touching the
submodule. `CLAUDE_CODE_VERSION` (default `stable`) and `GROK_VERSION` (default
latest stable) are the consciously mutable components — set concrete versions to
freeze them.

Policy: small CLI tools may be build arguments; large ecosystems and service
dependencies (Blender, databases, browsers) belong in Dev Container Features or
project-owned layers, not the shared Dockerfile. The harness ships one such
feature: `features/playwright-deps` for headless-browser automation — see
[browser-automation.md](browser-automation.md).

## Claude Code project settings

`install.sh` seeds `<project>/.claude/settings.json` (only when the project has
none) wiring a statusline — `user ➜ dir (branch ✗) · model (effort) · context%` —
plus a per-subagent statusline and a `sudo`/`su` permission deny (sudo does not
exist in the container; denying skips doomed attempts). The scripts live in the
harness (`scripts/statusline.sh`, `scripts/subagent-statusline.sh`), so they
update with the submodule. To adopt them in a project with existing settings,
merge the keys from `templates/claude-settings.json`.

`install.sh` also seeds `.devcontainer/AGENTS.md` — container rules for agents;
import it from the project's root `CLAUDE.md`/`AGENTS.md` with a
`@.devcontainer/AGENTS.md` line (see [onboarding.md](onboarding.md)).

## Secrets

Nothing auto-sources `.env`; `.bashrc` is never modified. Load explicitly:

```bash
./.devcontainer/vibe agent            # loads DEV_ENV_FILE, then runs DEV_AGENT_CMD
./.devcontainer/vibe agent --cold     # same, but without repo instruction files (see usage.md)
./.devcontainer/vibe run codex        # same, for any command
./.devcontainer/harness/scripts/env-run.sh some-command   # inside the container
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

Alternative: `GH_TOKEN` is forwarded from the host via `remoteEnv` (never baked
into the image). Note the trade: a host-level token is one token for **every**
project's container, and while it is set `gh auth login` refuses to run —
unset it on the host to use per-project logins.

## Ports and host networking

No ports are published by default. To let the container reach services on the host
(e.g. a local LLM server), add to the project's `runArgs`:

```jsonc
"--add-host=host.docker.internal:host-gateway"
```

This is deliberately opt-in per project — see [local-models.md](local-models.md).

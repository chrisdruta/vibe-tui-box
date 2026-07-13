# Configuration reference

Project-owned configuration lives next to the harness submodule and is seeded by
`install.sh`; edit it freely, it is never overwritten by harness updates.

## `config.env`

Non-secret behavior toggles, sourced by the lifecycle scripts and `dev agent`:

| Variable                | Default                       | Meaning                                            |
| ----------------------- | ----------------------------- | -------------------------------------------------- |
| `DEV_AGENT_CMD`         | `claude`                      | What `dev agent` runs (may include arguments)      |
| `DEV_AGENT_TMUX`        | `1` (seeded; unset = `0`)     | `1`: `dev agent` runs in a persistent tmux session — rerunning attaches, detaching (`Ctrl-b d`) keeps it alive |
| `DEV_AGENT_TMUX_SESSION`| `agent`                       | tmux session name used by `dev agent`              |
| `DEV_BOOTSTRAP_STRICT`  | `1`                           | `1`: bootstrap steps fail loudly; `0`: warn and continue |
| `DEV_AUTO_INSTALL`      | `1`                           | Enable lockfile-detected dependency installation   |
| `DEV_AUTO_GIT_HOOKS`    | `1`                           | Wire `.githooks/` into `core.hooksPath` (see [security.md](security.md)) |
| `DEV_AUTO_GIT_LFS`      | `1`                           | Repo-local `git lfs install` when LFS attributes exist |
| `DEV_ENV_FILE`          | `.env`                        | File loaded by `dev agent` / `dev run` / `env-run.sh` |
| `DEV_REQUIRED_COMMANDS` | `git gh jq rg uv claude` (+preset) | Commands `dev doctor` requires                |

## Bootstrap behavior

`post-create.sh` (also rerunnable as `dev bootstrap`) detects manifests in the
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
  "INSTALL_CODEX": "false",   // OpenAI Codex CLI (pulls in Node)
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
./.devcontainer/dev agent            # loads DEV_ENV_FILE, then runs DEV_AGENT_CMD
./.devcontainer/dev run codex        # same, for any command
./.devcontainer/harness/scripts/env-run.sh some-command   # inside the container
```

`GH_TOKEN` is forwarded from the host via `remoteEnv` (never baked into the image).
Agent API keys (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `XAI_API_KEY`) belong in the
project `.env`. For unattended runs, use minimum-permission tokens or none.

## Ports and host networking

No ports are published by default. To let the container reach services on the host
(e.g. a local LLM server), add to the project's `runArgs`:

```jsonc
"--add-host=host.docker.internal:host-gateway"
```

This is deliberately opt-in per project — see [local-models.md](local-models.md).

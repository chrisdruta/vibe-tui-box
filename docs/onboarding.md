# Onboarding a project (agent-driven)

`install.sh` seeds the files; the judgment calls — which toolchain args to flip,
what goes in the lifecycle hooks, what to salvage from an old setup — are
exactly the kind of reconciliation a coding agent does well. This page is
the checklist, and a prompt you can paste to have an agent do it.

## Checklist

1. **Preset** — pick from the repo's lockfiles: `uv.lock` → `python`,
   `bun.lock`/`bun.lockb` → `bun`, `rokit.toml` → `roblox`, else `minimal`.
   Run `install.sh --preset <preset>` (use `--force` if a `.vibe`
   already exists; it is backed up first). A project on the legacy
   `.devcontainer/` layout migrates via
   [updating.md → Migrating to the compose engine](updating.md) instead.
2. **Build args** — reconcile `.vibe/compose.yaml` with the actual toolchain:
   `INSTALL_NODE` (npm/pnpm/yarn lockfiles, or anything using `npx`),
   `INSTALL_BUN`, `INSTALL_CODEX`/`INSTALL_GROK` if those agents are used.
   Pin versions where the project cares (see [configuration.md](configuration.md)).
3. **Migrate any old container setup** (an old `.devcontainer`, a
   hand-rolled Dockerfile/compose file): carry over env vars, extra mounts,
   and build steps into `compose.yaml` and the hooks. Do **not** carry over
   sudo, docker-socket mounts, or published ports without a reason — the
   hardening is the point ([security.md](security.md)). One reason that
   qualifies: loopback-only port publishes (`127.0.0.1:PORT:PORT`) for host
   tooling that must reach the container (Studio → Rojo being the canonical
   case).
4. **Lifecycle hooks** — translate the repo's README/setup steps into
   `project/post-create.sh` (one-time: codegen, migrations, MCP setup) and
   `project/post-start.sh` (every start; keep idempotent). Dependency installs
   are already automatic for detected lockfiles.
5. **`config.env`** — set `DEV_REQUIRED_COMMANDS` to what the project actually
   needs (`vibe doctor` enforces it); adjust `DEV_AGENT_CMD` / `DEV_AGENT_TMUX`.
6. **Secrets** — ensure `.env` is gitignored; move API keys there.
7. **Agent rules** — add `@.vibe/AGENTS.md` to the project's root
   `CLAUDE.md`/`AGENTS.md` so agents inherit the container rules. If the
   project already had `.claude/settings.json`, merge in the statusline and
   hook keys from `src/templates/claude-settings.json` (fresh installs get them
   seeded). Project-specific agent behavior (hooks, skills, subagents) lives
   in the project's `.claude/`, not the harness.
8. **Recipes** — apply what fits: [browser-automation.md](browser-automation.md)
   (headless Chromium for web projects), [roblox.md](roblox.md),
   [local-models.md](local-models.md).
9. **Verify** — `vibe up`, `vibe doctor`, exercise a build/test command via
   `vibe exec`, then `vibe rebuild` to confirm everything survives recreation.
10. **Commit** — the seeded files plus the submodule pin.

## Agent prompt

Paste into an agent running at the project root — the flow is
submodule-first, so nothing is fetched outside the project and the installer
runs non-interactively via flags:

```text
Onboard this repository onto the vibe-devcontainer-submodule harness
(https://github.com/chrisdruta/vibe-devcontainer-submodule) and reconcile it
with the project.

1. Inspect the repo (lockfiles, README setup steps, any existing container
   config or CI) and pick the preset: python (uv.lock), bun (bun.lock),
   roblox (rokit.toml), else minimal — plus any extras the toolchain needs
   (codex, grok, node, playwright). If the repo has a legacy .devcontainer/
   harness layout, follow the harness docs/updating.md -> "Migrating to the
   compose engine" instead of steps 2-3.
2. Run, from the repository top level:
   git submodule add https://github.com/chrisdruta/vibe-devcontainer-submodule.git .vibe/harness
   .vibe/harness/install.sh --preset <preset> [--extras <list>]
3. Reconcile .vibe/compose.yaml build args with the toolchain
   (INSTALL_NODE, INSTALL_BUN, ...) and migrate anything still valuable from
   any old container setup: env vars, mounts, build steps. Never reintroduce
   sudo or docker-socket mounts. Published ports: keep loopback-only binds
   ("127.0.0.1:PORT:PORT") that host tooling needs to reach the container
   (e.g. Roblox Studio -> Rojo 34872). Never publish bare ports (they bind
   0.0.0.0); see docs/roblox.md.
4. Fill .vibe/project/post-create.sh and post-start.sh from the
   project's documented setup steps (dependency installs for detected
   lockfiles are automatic — don't duplicate them). Keep hooks idempotent.
5. Set DEV_REQUIRED_COMMANDS in .vibe/config.env to the commands this
   project needs; ensure .env is gitignored.
6. Add the line "@.vibe/AGENTS.md" to the root CLAUDE.md (create it if
   missing). If the repo already had .claude/settings.json, merge in the keys
   from the harness src/templates/claude-settings.json without clobbering existing
   settings.
7. Read .vibe/harness/docs/ and apply relevant recipes
   (browser-automation for web projects, roblox, local-models).
8. Verify: ./vibe up, then vibe doctor, then run the project's
   build/test via vibe exec, then vibe rebuild. Fix what fails; constraints are
   explained in .vibe/AGENTS.md (no sudo — OS packages need build args
   plus a rebuild).
9. Stage everything and report: preset chosen, args flipped, what was migrated
   or dropped from the old setup, and any setup steps you could not automate.
```

Review the diff before committing — the agent is reconciling, but the
`.vibe` files are project-owned and yours to keep opinionated.

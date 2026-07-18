# Onboarding a project (agent-driven)

`install.sh` seeds the files; the judgment calls — which toolchain args to flip,
what goes in the lifecycle hooks, what to salvage from an old `.devcontainer` —
are exactly the kind of reconciliation a coding agent does well. This page is
the checklist, and a prompt you can paste to have an agent do it.

## Checklist

1. **Preset** — pick from the repo's lockfiles: `uv.lock` → `python`,
   `bun.lock`/`bun.lockb` → `bun`, `rokit.toml` → `roblox`, else `minimal`.
   Run `install.sh --preset <preset>` (use `--force` if a `.devcontainer`
   already exists; it is backed up first).
2. **Build args** — reconcile `devcontainer.json` with the actual toolchain:
   `INSTALL_NODE` (npm/pnpm/yarn lockfiles, or anything using `npx`),
   `INSTALL_BUN`, `INSTALL_CODEX`/`INSTALL_GROK` if those agents are used.
   Pin versions where the project cares (see [configuration.md](configuration.md)).
3. **Migrate the old `.devcontainer`** (in the `.devcontainer.backup.*` dir
   after `--force`): carry over extensions into `customizations.vscode`,
   `containerEnv`, extra mounts, and features. Do **not** carry over sudo,
   docker-socket mounts, or published ports without a reason — the hardening
   is the point ([security.md](security.md)).
4. **Lifecycle hooks** — translate the repo's README/setup steps into
   `project/post-create.sh` (one-time: codegen, migrations, MCP setup) and
   `project/post-start.sh` (every start; keep idempotent). Dependency installs
   are already automatic for detected lockfiles.
5. **`config.env`** — set `DEV_REQUIRED_COMMANDS` to what the project actually
   needs (`vibe doctor` enforces it); adjust `DEV_AGENT_CMD` / `DEV_AGENT_TMUX`.
6. **Secrets** — ensure `.env` is gitignored; move API keys there.
7. **Agent rules** — add `@.devcontainer/AGENTS.md` to the project's root
   `CLAUDE.md`/`AGENTS.md` so agents inherit the container rules. If the
   project already had `.claude/settings.json`, merge in the statusline keys
   from `templates/claude-settings.json` (fresh installs get them seeded).
   Project-specific agent behavior (hooks, skills, subagents) lives in the
   project's `.claude/`, not the harness.
8. **Recipes** — apply what fits: [browser-automation.md](browser-automation.md)
   (headless Chromium for web projects), [roblox.md](roblox.md),
   [local-models.md](local-models.md).
9. **Verify** — `vibe up`, `vibe doctor`, exercise a build/test command via
   `vibe exec`, then `vibe rebuild` to confirm everything survives recreation.
10. **Commit** — the seeded files plus the submodule pin.

## Agent prompt

Paste into an agent running at the project root — the agent fetches a fresh
scaffold clone every run, so results never depend on the state of some local
copy (the clone is throwaway: install.sh reads templates from it and adds the
submodule from GitHub, so it can be deleted afterwards):

```text
Onboard this repository onto the vibe-devcontainer-submodule harness
(https://github.com/chrisdruta/vibe-devcontainer-submodule) and reconcile it
with the project.

0. Clone the latest harness scaffold (always fresh — do not look for or reuse
   an existing local copy):
   rm -rf /tmp/vibe-harness && git clone --depth 1 \
     https://github.com/chrisdruta/vibe-devcontainer-submodule.git /tmp/vibe-harness
1. Inspect the repo (lockfiles, README setup steps, any existing .devcontainer
   or CI config) and pick the preset: python (uv.lock), bun (bun.lock),
   roblox (rokit.toml), else minimal.
2. Run: /tmp/vibe-harness/install.sh --preset <preset> .
   (add --force if .devcontainer exists — it gets backed up automatically).
3. Reconcile .devcontainer/devcontainer.json build args with the toolchain
   (INSTALL_NODE, INSTALL_BUN, ...) and migrate anything still valuable from
   the backup dir: extensions, containerEnv, mounts, features. Never reintroduce
   sudo, docker-socket mounts, or published ports.
4. Fill .devcontainer/project/post-create.sh and post-start.sh from the
   project's documented setup steps (dependency installs for detected
   lockfiles are automatic — don't duplicate them). Keep hooks idempotent.
5. Set DEV_REQUIRED_COMMANDS in .devcontainer/config.env to the commands this
   project needs; ensure .env is gitignored.
6. Add the line "@.devcontainer/AGENTS.md" to the root CLAUDE.md (create it if
   missing). If the repo already had .claude/settings.json, merge in the keys
   from the harness templates/claude-settings.json without clobbering existing
   settings.
7. Read .devcontainer/harness/docs/ and apply relevant recipes
   (browser-automation for web projects, roblox, local-models).
8. Verify: ./.devcontainer/vibe up, then vibe doctor, then run the project's
   build/test via vibe exec, then vibe rebuild. Fix what fails; constraints are
   explained in .devcontainer/AGENTS.md (no sudo — OS packages need build args
   or features plus a rebuild).
9. Stage everything and report: preset chosen, args flipped, what was migrated
   or dropped from the old setup, and any setup steps you could not automate.
```

Review the diff before committing — the agent is reconciling, but the
`.devcontainer` files are project-owned and yours to keep opinionated.

# Updating the harness

Every consuming project pins the harness submodule to a **commit SHA** — nothing
changes underneath you until you deliberately move the pin. Treat updates as a
review-the-diff moment, not a blind pull.

## Fastest: `vibe update`

```bash
./vibe update          # newest release tag
./vibe update v0.7.3   # a specific tag — same command rolls back
```

One command for the recommended flow below: fetches tags, prints the
CHANGELOG sections between the two pins and the diff stat, checks out the
tag, and **stages** the pin move. It never commits and never rebuilds — you
still review `git diff --cached --submodule`, commit, and rebuild when it
reports that the `Dockerfile` changed. It also works from inside the
container (agents can run `bash .vibe/harness/src/scripts/update.sh`
directly); the rebuild step it prints still belongs to the host. It runs on
both the current `.vibe/` layout and the legacy `.devcontainer/` layout —
updating across the engine swap is exactly when that matters.

## Recommended: update to a tagged release

```bash
git -C .vibe/harness fetch --tags
git -C .vibe/harness tag --list          # see what's available
git -C .vibe/harness checkout v1.0.0
git diff --submodule .vibe/harness       # review what changed
git add .vibe/harness
git commit -m "Update vibe harness to v1.0.0"
./vibe rebuild                            # if the Dockerfile changed
```

Tags mark intentional upgrade points and rollback targets; rolling back is the same
flow with an older tag.

## Convenience: follow the branch

```bash
git submodule update --remote --merge .vibe/harness
git add .vibe/harness && git commit -m "Update vibe harness"
```

This moves the pin to the tip of the configured branch (`--ref` at install time,
default `main`) — fine for your own repositories, but it takes whatever is on the
branch without a version decision. Prefer tags for anything you care about.

## Updating many repositories

The pin lives in each consuming repo, so each repo updates independently. A quick
sweep over everything under `~/dev`:

```bash
for repo in ~/dev/*/.vibe/harness; do
  git -C "${repo%/.vibe/harness}" submodule update --remote --merge .vibe/harness
done
```

…then review and commit per repo.

## After updating

- Run `./vibe doctor`.
- Run `./vibe rebuild` when the update touched the `Dockerfile` or
  anything under `templates/` you want re-rendered (template changes only affect
  newly installed projects; your project-owned files are never rewritten —
  re-run `install.sh --force` if you want a fresh seed, your old files are backed up).

## Migrating to the compose engine (from `.devcontainer/`)

Projects installed before the engine swap use a `.devcontainer/` directory,
`devcontainer.json`, and the devcontainer CLI. The current harness drives
docker compose directly and lays projects out under `.vibe/`. The migration
is one commit; agent logins survive it (the state volume is named by the
project folder, which does not change).

```bash
# 0. From the project root, with the pin already moved to the new release
#    (vibe update works on the old layout; any ref works — fetch and
#    checkout it inside the submodule, tags are just the usual targets).

# 1. Rename the directory; git mv keeps history and updates .gitmodules.
git mv .devcontainer .vibe

# 2. Replace devcontainer.json with the compose override. Seed from the
#    pre-rendered preset matching your project — the raw template in
#    src/templates/ carries @PLACEHOLDER@s that only install.sh renders:
cp .vibe/harness/examples/minimal/compose.yaml .vibe/compose.yaml   # or python/bun/roblox
git rm .vibe/devcontainer.json
#    Then port over your customizations:
#    - build.args: copy your INSTALL_* / BASE_IMAGE values in — under
#      services.base.build.args (the base service builds the shared image;
#      dev runs it)
#    - appPort  ->  ports: ["127.0.0.1:X:Y"]
#    - mounts   ->  volumes: (workspace + agent-state come from the base)
#    - a devcontainer "features" entry for playwright-deps becomes a
#      project image extension: copy examples/extensions/playwright/
#      Dockerfile to .vibe/Dockerfile (+ INSTALL_NODE in base args) —
#      see docs/extending.md

# 3. Refresh the seeded files whose contents the engine swap rewrote — the
#    wrapper and the agent instructions both reference retired paths, so
#    stale copies actively mislead agents (merge first if you customized):
cp .vibe/harness/src/templates/vibe .vibe/vibe
cp .vibe/harness/src/templates/agents.md .vibe/AGENTS.md
#    (config.env keeps working as-is; refresh comments from
#     examples/<preset>/config.env if you want them current)

# 4. Link the root entry point (skip if you have a real ./vibe file) and
#    fix hook paths (sed -i.bak form works on both GNU and macOS sed):
[ -e vibe ] || { ln -s .vibe/vibe vibe && git add vibe; }
sed -i.bak 's|\.devcontainer/harness/scripts|.vibe/harness/src/scripts|g' \
  .claude/settings.json && rm .claude/settings.json.bak
#    (also update any @.devcontainer/AGENTS.md import in CLAUDE.md/AGENTS.md,
#     and paths in project/ hooks or CI that mention .devcontainer/)

# 5. Retire the devcontainer-CLI-era container, then bring the new engine
#    up. Down FIRST — vibe down matches the old container's label too, and
#    skipping it leaves two containers sharing the workspace:
./vibe down && ./vibe up && ./vibe doctor

# 6. Commit everything together.
git add -A && git status   # review, then commit
```

VS Code's "Reopen in Container" is gone with the layout (that was the
devcontainer CLI's feature). The terminal workflow is unchanged, and VS Code
still works against the checkout via WSL Remote / normal local editing — or
"Attach to Running Container" against a `vibe up`'d container if you want
editor-in-container.

## Agent-driven update

Moving the pin is mechanical; reconciling the project-owned seeded files with
what new versions expect (new build args, settings denies, wrapper changes)
is the judgment-call part — like [onboarding](onboarding.md), a good job for
an agent. Steps 1–2 are `vibe update` (in-container:
`bash .vibe/harness/src/scripts/update.sh`); the rest is the reconciliation
judgment. Paste into an agent at the project root:

```text
Update this project's vibe-devcontainer-submodule harness pin and reconcile
the project-owned files with the new version.

1. In the harness submodule (.vibe/harness, or .devcontainer/harness on the
   old layout): git fetch --tags, then report the current pin
   (git describe --tags) and the latest tag. Before touching anything, read
   the harness CHANGELOG.md entries between those two versions and
   docs/updating.md — especially the "Migrating to the compose engine"
   section if this project still has a .devcontainer/ directory, and any
   "Crossing ..." sections that apply to this jump.
2. Check out the latest tag in the submodule and stage the pin move.
3. If the project is on the legacy .devcontainer/ layout, perform the
   migration from docs/updating.md -> "Migrating to the compose engine"
   (git mv to .vibe, devcontainer.json -> compose.yaml, wrapper + hook
   paths, root ./vibe symlink) — carrying every project customization over.
4. Reconcile the project-owned files against the new templates in
   .vibe/harness/src/templates/:
   - compose.yaml: adopt new build args from the template, keeping the
     project's current values;
   - config.env: adopt new toggles, keeping the project's current values;
   - the wrapper: copy templates/vibe over .vibe/vibe;
   - .claude/settings.json: merge new permission denies, hooks, and
     statusline keys from src/templates/claude-settings.json without clobbering
     existing entries.
   Reconcile, don't reset: where the template and the project disagree, keep
   the project's value and flag it in your report.
5. Never weaken hardening while reconciling: no sudo, no docker-socket
   mounts, no published ports (loopback-only exceptions need a documented
   reason, like Roblox Studio -> Rojo).
6. Verify with vibe doctor. If the Dockerfile or compose files changed, a
   rebuild is required — run ./vibe rebuild on the host; if you are running
   inside the container, stage everything and ask the user to rebuild
   instead.
7. Commit the pin move and the reconciliations together. Report: old -> new
   version, each file changed and why, and anything in the changelog that
   needs a human decision.
8. End with a FRICTION REPORT: every gap, ambiguity, missing doc, wrong doc,
   or workaround you needed during this update, as a paste-ready list — the
   human feeds it back to the harness repo. An empty list is a finding too;
   say so explicitly.
```

Review the diff before pushing — the seeded files are project-owned, and the
agent is instructed to prefer your values over the templates on conflict.

## Older migrations

**Crossing the `GH_TOKEN` passthrough removal (2026-07, pre-v1.0):** the
harness no longer forwards a host `GH_TOKEN` into the container environment.
The first container recreate after this pin move drops it; GitHub auth is
`gh auth login` inside the container (a fine-grained PAT pasted once — it
persists in the agent-state volume). If you keep a PAT in `.env` for
reference, store it under a neutral name, never `GH_TOKEN`/`GITHUB_TOKEN` —
see [configuration.md](configuration.md#fine-grained-pat-quick-reference).

**Crossing the v0.4.0 rename (pre-v0.4.0 installs):** the launcher was
renamed `dev` → `vibe` in v0.4.0 and the back-compat shim dropped in v0.5.0.
A project that old should jump straight to the compose migration above; while
still on the legacy layout, replace `.devcontainer/dev` with the wrapper from
`templates/vibe` in the same commit as any pin move to ≥ v0.5.0, and merge
`GH_CONFIG_DIR` into `containerEnv` plus the `Read(./.env)` denies into
`.claude/settings.json` (v0.4.0 features; see that release's notes).

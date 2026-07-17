# Updating the harness

Every consuming project pins the harness submodule to a **commit SHA** — nothing
changes underneath you until you deliberately move the pin. Treat updates as a
review-the-diff moment, not a blind pull.

## Recommended: update to a tagged release

```bash
git -C .devcontainer/harness fetch --tags
git -C .devcontainer/harness tag --list          # see what's available
git -C .devcontainer/harness checkout v0.1.0
git diff --submodule .devcontainer/harness       # review what changed
git add .devcontainer/harness
git commit -m "Update devcontainer harness to v0.1.0"
./.devcontainer/vibe rebuild                      # if the Dockerfile changed
```

Tags mark intentional upgrade points and rollback targets; rolling back is the same
flow with an older tag.

## Convenience: follow the branch

```bash
git submodule update --remote --merge .devcontainer/harness
git add .devcontainer/harness && git commit -m "Update devcontainer harness"
```

This moves the pin to the tip of the configured branch (`--ref` at install time,
default `main`) — fine for your own repositories, but it takes whatever is on the
branch without a version decision. Prefer tags for anything you care about.

## Updating many repositories

The pin lives in each consuming repo, so each repo updates independently. A quick
sweep over everything under `~/dev`:

```bash
for repo in ~/dev/*/.devcontainer/harness; do
  git -C "${repo%/.devcontainer/harness}" submodule update --remote --merge .devcontainer/harness
done
```

…then review and commit per repo.

## After updating

- Run `./.devcontainer/vibe doctor`.
- Run `./.devcontainer/vibe rebuild` when the update touched the `Dockerfile` or
  anything under `templates/` you want re-rendered (template changes only affect
  newly installed projects; your project-owned files are never rewritten —
  re-run `install.sh --force` if you want a fresh seed, your old files are backed up).

## Crossing the v0.4.0 rename from an older install

The launcher was renamed `dev` → `vibe` in v0.4.0 and the back-compat shim was
dropped in v0.5.0, so a pre-v0.4.0 project's seeded `.devcontainer/dev`
wrapper stops working when the pin moves to ≥ v0.5.0 (it execs
`harness/dev`, which no longer exists — the failure message misleadingly
suggests a missing submodule). **In the same commit as the pin bump**, replace
the wrapper:

```bash
git mv .devcontainer/dev .devcontainer/vibe
cp .devcontainer/harness/templates/vibe .devcontainer/vibe
```

Two more seeded files are worth reconciling by hand (or with
`install.sh --force`) to pick up the v0.4.0 features:

- `devcontainer.json`: add `"GH_CONFIG_DIR": "/home/vscode/.agents/gh"` to
  `containerEnv` — without it, `gh auth login` lands in the container
  filesystem and dies on every rebuild instead of persisting in the state
  volume ([configuration.md → GitHub access](configuration.md)).
- `.claude/settings.json`: merge the `Read(./.env)` / `Read(./.env.*)` denies
  (and, pre-v0.3.0, the statusline) from `templates/claude-settings.json`.

Optionally rename the wrapper to match the docs: `git mv .devcontainer/dev
.devcontainer/vibe`, then copy the current `templates/vibe` over it (the new
wrapper tries both launcher names, so it also works if the pin ever moves back).

# Installation

## Prerequisites

- The target must be an **existing git repository**, and you must run the installer
  against its top level — the harness is added as a git submodule.
- Git and Docker on the host (Docker Desktop bundles the compose plugin the
  launcher uses). Nothing else — no Node, no devcontainer CLI.
- On Windows, keep the repository in the WSL filesystem (`~/dev/...`); `/mnt/c`
  paths suffer severe filesystem-performance and permission problems in containers.
- On macOS, Docker Desktop works out of the box on Apple Silicon and Intel
  (OrbStack is a faster drop-in alternative if bind-mount performance bites).
  All host-side scripts run on the stock macOS bash 3.2.

## Install (submodule-first — the default flow)

From the top level of the project repository:

```bash
git submodule add https://github.com/chrisdruta/vibe-devcontainer-submodule.git .vibe/harness
.vibe/harness/install.sh
```

The submodule is the delivery mechanism — no separate clone, no `curl | sh`,
no npx; everything arrives over git and is pinnable/diffable like any other
dependency. Run on a terminal with no arguments, the installer is
**interactive**: pick the preset, optionally enable extras (`codex`, `grok`,
`node`, `playwright`), confirm. Any argument switches to plain flag mode
(`--preset python --extras codex`) for scripts and agents.

The installer:

1. seeds the project-owned files (`compose.yaml`, `config.env`, `vibe` wrapper,
   `AGENTS.md`, `project/` hooks, `yazi/` review preferences) rendered for the
   chosen preset — selected extras get their build args enabled in the seeded
   `compose.yaml` — plus `.claude/settings.json` (statusline, image-preview
   hooks, sudo/`.env`-read deny) unless the project already has one,
2. links `./vibe -> .vibe/vibe` at the repository root,
3. registers the submodule if needed (already done in the flow above; a plain
   `git clone` into `.vibe/harness` gets absorbed),
4. stages everything — nothing is committed; review with `git status` and commit.

The pin is whatever `git submodule add` cloned (branch tip); pin a tagged
release afterwards with `./vibe update vX.Y.Z` — it stages the move for
review like everything else.

To have an agent make the judgment calls (build args, hooks, migrating an old
setup), see [onboarding.md](onboarding.md). A project still on the legacy
`.devcontainer/` layout migrates via
[updating.md → Migrating to the compose engine](updating.md) instead of
reinstalling.

## Install from a scaffold clone

Handy when setting up many projects, or when developing the harness itself:

```bash
git clone https://github.com/chrisdruta/vibe-devcontainer-submodule.git \
  ~/dev/vibe-devcontainer-submodule

~/dev/vibe-devcontainer-submodule/install.sh --preset python ~/dev/my-project
```

Same result; the installer adds the submodule itself (from the clone's
`origin` URL) instead of finding one already in place.

## Options

| Flag             | Meaning                                                        |
| ---------------- | -------------------------------------------------------------- |
| `--preset NAME`  | `minimal` (default), `python`, `bun`, or `roblox`               |
| `--extras LIST`  | Comma-separated: `codex`, `grok`, `node`, `playwright` — enables the build args in the seeded `compose.yaml` (`playwright` implies `node`) |
| `--url URL`      | Submodule URL (default: the clone's `origin` remote)            |
| `--ref BRANCH`   | Submodule branch to track (default: `main`; scaffold mode only) |
| `--force`        | Back up an existing `.vibe` and replace it (scaffold mode only) |

With `--force`, the existing `.vibe` is moved to a timestamped
`.vibe.backup.*` directory and any previous harness submodule registration
is scrubbed before reinstalling.

If the scaffold clone has no `origin` remote (e.g. you are hacking on a local copy),
its local path is used as the submodule URL; switch later with:

```bash
git submodule set-url .vibe/harness https://github.com/chrisdruta/vibe-devcontainer-submodule.git
```

## First start

```bash
cd ~/dev/my-project
./vibe up        # builds the image, starts the container, bootstraps
./vibe agent     # launches the default agent (Claude Code)
```

On first `vibe agent`, Claude Code walks through its login. Logins persist in a
named volume per project — see [agent-state.md](agent-state.md).

For `git push` / `gh` from inside the container, mint a per-project
fine-grained PAT and `gh auth login` once — the installer prints the
permission set in its next-steps output, and
[configuration.md → GitHub access](configuration.md) has the full reference.

## Cloning a project that already uses the harness

```bash
git clone --recurse-submodules <project-url>
# or, after a plain clone / in a new git worktree:
git submodule update --init
```

The seeded `.vibe/vibe` wrapper prints exactly that hint if the submodule
is missing.

## Uninstall from a project

```bash
git submodule deinit -f .vibe/harness
git rm -f .vibe/harness
rm -rf "$(git rev-parse --git-common-dir)/modules/.vibe/harness"
git rm -r .vibe          # also remove the project-owned files, if desired
git rm vibe              # the root symlink
docker volume rm agent-state-<folder-name>   # discard persisted agent logins
```

The container and image can be removed with `./vibe down` (before removing
`.vibe`) and `docker rmi` (`docker images | grep vibe-`).

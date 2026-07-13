# Installation

## Prerequisites

- The target must be an **existing git repository**, and you must run the installer
  against its top level — the harness is added as a git submodule.
- Docker and either the Dev Container CLI or VS Code Dev Containers on the host.
- On Windows, keep the repository in the WSL filesystem (`~/dev/...`); `/mnt/c`
  paths suffer severe filesystem-performance and permission problems in containers.
- On macOS, Docker Desktop works out of the box on Apple Silicon and Intel
  (OrbStack is a faster drop-in alternative if bind-mount performance bites).
  All host-side scripts run on the stock macOS bash 3.2.

## Install

```bash
git clone https://github.com/chrisdruta/vibe-devcontainer-submodule.git \
  ~/dev/vibe-devcontainer-submodule

~/dev/vibe-devcontainer-submodule/install.sh --preset python ~/dev/my-project
```

The installer:

1. seeds the project-owned files (`devcontainer.json`, `config.env`, `dev` wrapper,
   `AGENTS.md`, `project/` hooks) rendered for the chosen preset, plus
   `.claude/settings.json` (statusline + sudo-deny) unless the project already
   has one,
2. runs `git submodule add` for `.devcontainer/harness`,
3. stages everything — nothing is committed; review with `git status` and commit.

To have an agent make the judgment calls (build args, hooks, migrating an old
`.devcontainer`), see [onboarding.md](onboarding.md).

## Options

| Flag             | Meaning                                                        |
| ---------------- | -------------------------------------------------------------- |
| `--preset NAME`  | `minimal` (default), `python`, `bun`, or `roblox`               |
| `--url URL`      | Submodule URL (default: the clone's `origin` remote)            |
| `--ref BRANCH`   | Submodule branch to track (default: `main`)                     |
| `--force`        | Back up an existing `.devcontainer` and replace it              |

With `--force`, the existing `.devcontainer` is moved to a timestamped
`.devcontainer.backup.*` directory and any previous harness submodule registration
is scrubbed before reinstalling.

If the scaffold clone has no `origin` remote (e.g. you are hacking on a local copy),
its local path is used as the submodule URL; switch later with:

```bash
git submodule set-url .devcontainer/harness https://github.com/chrisdruta/vibe-devcontainer-submodule.git
```

## First start

```bash
cd ~/dev/my-project
./.devcontainer/dev up        # builds the image, starts the container, bootstraps
./.devcontainer/dev agent     # launches the default agent (Claude Code)
```

VS Code users can instead **Reopen in Container**; the same lifecycle hooks run.

On first `dev agent`, Claude Code walks through its login. Logins persist in a
named volume per project — see [agent-state.md](agent-state.md).

## Cloning a project that already uses the harness

```bash
git clone --recurse-submodules <project-url>
# or, after a plain clone / in a new git worktree:
git submodule update --init
```

The seeded `.devcontainer/dev` wrapper prints exactly that hint if the submodule
is missing.

## Uninstall from a project

```bash
git submodule deinit -f .devcontainer/harness
git rm -f .devcontainer/harness
rm -rf "$(git rev-parse --git-common-dir)/modules/.devcontainer/harness"
git rm -r .devcontainer          # also remove the project-owned files, if desired
docker volume rm agent-state-<folder-name>   # discard persisted agent logins
```

The container image can be removed with `docker rmi` (`docker images | grep vsc-`).

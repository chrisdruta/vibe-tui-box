# Architecture

## The layering

```text
Windows 11 (or macOS / any Docker host)
└── WSL2 Ubuntu — canonical git repos, credentials, git + docker
    (on macOS: the Mac itself plays this role — no VM layer to think about)
    └── project container  ← the agent harness IS the project container
        ├── coding agents (Claude Code; Codex/Grok opt-in)
        ├── project runtime & tooling
        └── (optional compose services: databases, browsers, Blender, …)
```

One deliberate principle: the project's container is itself the agent harness.
There is no separate "agent container" controlling a "project container" — the agent
runs as a process inside the same reproducible environment that builds and tests
the project.

The engine is docker compose + docker exec, driven entirely by the `vibe`
launcher: the container is defined by the harness base compose file
(`src/compose/base.yaml` — workspace mount, agent-state volume, hardening,
environment) with the project's `.vibe/compose.yaml` merged on top via `-f`
stacking (`--project-directory` is the project root, so the one harness file
serves every consumer). The image side is a two-link chain: a build-only
`base` service produces the shared harness image
(`${VIBE_PROJECT_NAME}-base` from `src/Dockerfile`), and the `dev` service
runs it — or, when the project declares an image extension
(`.vibe/Dockerfile`, [extending.md](extending.md)), runs
`${VIBE_PROJECT_NAME}-dev` built FROM the base; the launcher sequences the
builds. `vibe up` builds missing images, runs compose, then execs
`post-create.sh` once per container (marker file at
`/var/tmp/.vibe-post-created`) and `post-start.sh` on every actual start.
Containers are discovered by the `vibe.project=<repo-root>` label.

## The ownership boundary

```text
my-project/
├── vibe                # PROJECT-owned: root symlink → .vibe/vibe
└── .vibe/
    ├── compose.yaml    # PROJECT-owned: image args, mounts, ports (merged over the base)
    ├── config.env      # PROJECT-owned: behavior toggles
    ├── vibe            # PROJECT-owned: thin wrapper → harness/vibe
    ├── project/        # PROJECT-owned: lifecycle hooks
    ├── yazi/           # PROJECT-owned: image-review preferences
    └── harness/        # SHARED: this repo, pinned as a git submodule
        ├── vibe            # launcher (repo-agnostic; entry point at the root)
        ├── install.sh      # consumer installation
        ├── src/
        │   ├── Dockerfile      # image recipe (base + optional tool args)
        │   ├── compose/        # base compose file (the container definition)
        │   ├── scripts/        # lifecycle: lib, post-create, post-start, doctor, env-run
        │   ├── scripts/host/   # WSL-host helpers (start-ollama.sh, clip-image.sh)
        │   ├── config/         # container config baked by the Dockerfile (tmux.conf, yazi)
        │   └── templates/      # seeds for the project-owned files (install-time only)
        ├── examples/       # rendered per-preset seeds (verify.sh keeps them fresh)
        └── docs/

`vibe`, `install.sh`, and everything under `src/` are effectively the
harness's public interface: seeded consumer files reference them by path
(`harness/src/Dockerfile`, `harness/src/scripts/post-create.sh`, …), so renaming or moving
them breaks every consumer on its next submodule update. Anything else may be
reorganized freely.
```

Everything above `harness/` belongs to the project and is seeded once by
`install.sh` (placeholder-rendered per preset — plain `sed`, no host dependencies).
Everything inside `harness/` must remain repo-agnostic: no project names, paths,
services, or ports.

Distribution is a pinned git submodule: consuming repos reference an exact commit,
updates are explicit and reviewable ([updating.md](updating.md)), and one clone
serves every project. Presets are render-time deltas over a single template — not
four diverging config files.

## Path discovery subtleties

- Inside the submodule, `git rev-parse --show-toplevel` returns the **submodule's**
  root, not the project's. `src/scripts/lib.sh` therefore anchors on the directory
  the harness lives under (three levels up from `src/scripts/`) to find the
  project root — positional, so it works on both `.vibe/` and the legacy
  `.devcontainer/` layout.
- Host tools (`vibe`, `update.sh`) instead anchor on `$PWD` via
  `src/scripts/repo-root.sh` (nearest ancestor with `.vibe/compose.yaml`, legacy
  `.devcontainer/devcontainer.json` recognized for migration).
- The state volume mountpoint (`~/.agents`) is pre-created in the image as `vscode`;
  a root-owned volume could never be repaired at runtime (no sudo, no capabilities).
- Grok Build's installer symlinks its binary into `~/.grok/downloads`, which the
  state volume shadows at runtime — the Dockerfile materializes the real binary
  into `~/.local/bin` instead.

## Contributing / developing the harness

```bash
./verify.sh
```

Checks shell syntax on every script, runs ShellCheck when available, installs every
preset into scratch git repositories (asserting submodule wiring, rendered
placeholders, preset-specific content, and — when docker is available — that
the merged compose config renders), and exercises `--force` reinstall.
Note: the install test clones **committed HEAD** — commit before verifying.

For runtime changes, do a real end-to-end pass: install into a scratch repo,
`vibe up`, `vibe doctor`, exercise the changed behavior, and `vibe rebuild` to confirm
state survives recreation.

Ground rules (see also [AGENTS.md](../AGENTS.md)):

- The container must end non-root with no Docker socket, host home, or SSH mounts.
- Lifecycle scripts stay idempotent; failures are loud under strict mode.
- Small CLI tools may be Dockerfile build args; large ecosystems become
  project-owned compose services or base images — the shared image must not
  grow into a kitchen sink.
- Shell style: `set -euo pipefail`, ShellCheck-clean, no auto-sourcing into
  interactive shells.

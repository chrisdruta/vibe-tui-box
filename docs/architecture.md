# Architecture

## The layering

```text
Windows 11 (or macOS / any Docker host)
└── WSL2 Ubuntu — canonical git repos, credentials, Dev Container CLI
    (on macOS: the Mac itself plays this role — no VM layer to think about)
    └── project Dev Container  ← the agent harness IS the project container
        ├── coding agents (Claude Code; Codex/Grok opt-in)
        ├── project runtime & tooling
        └── (optional Compose sidecars: databases, browsers, Blender, …)
```

One deliberate principle: the project's Dev Container is itself the agent harness.
There is no separate "agent container" controlling a "project container" — the agent
runs as a process inside the same reproducible environment that builds and tests
the project.

## The ownership boundary

```text
my-project/.devcontainer/
├── devcontainer.json   # PROJECT-owned: image args, mounts, runArgs, extensions
├── config.env          # PROJECT-owned: behavior toggles
├── dev                 # PROJECT-owned: 3-line wrapper → harness/dev
├── project/            # PROJECT-owned: lifecycle hooks
└── harness/            # SHARED: this repo, pinned as a git submodule
    ├── Dockerfile      # image recipe (base + optional tool args)
    ├── dev             # launcher (repo-agnostic)
    ├── scripts/        # lifecycle: lib, post-create, post-start, doctor, env-run
    ├── scripts/host/   # WSL-host helpers (start-ollama.sh)
    ├── features/       # opt-in Dev Container Features (build-time apt installs)
    ├── config/         # container config baked by the Dockerfile (tmux.conf)
    └── templates/      # seeds for the project-owned files (install-time only)

`Dockerfile`, `dev`, `scripts/`, `features/`, and `templates/` are effectively the
harness's public interface: seeded consumer files reference them by path
(`harness/Dockerfile`, `harness/scripts/post-create.sh`, …), so renaming or moving
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
  root, not the project's. `scripts/lib.sh` therefore anchors on the `.devcontainer`
  directory (two levels up from `scripts/`) to find the project root.
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
placeholders, and preset-specific content), and exercises `--force` reinstall.
Note: the install test clones **committed HEAD** — commit before verifying.

For runtime changes, do a real end-to-end pass: install into a scratch repo,
`dev up`, `dev doctor`, exercise the changed behavior, and `dev rebuild` to confirm
state survives recreation.

Ground rules (see also [AGENTS.md](../AGENTS.md)):

- The container must end non-root with no Docker socket, host home, or SSH mounts.
- Lifecycle scripts stay idempotent; failures are loud under strict mode.
- Small CLI tools may be Dockerfile build args; large ecosystems become Dev
  Container Features or project-owned layers — the shared image must not grow
  into a kitchen sink.
- Shell style: `set -euo pipefail`, ShellCheck-clean, no auto-sourcing into
  interactive shells.

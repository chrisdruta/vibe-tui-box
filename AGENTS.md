# Agent instructions

## Purpose

This repository is a shared container harness consumed by other repositories as
a pinned git submodule at `.vibe/harness`. Changes here can affect every
consuming project — bias toward small, reviewable, backward-compatible commits.

## Invariants — do not break these

- The runtime container ends as the non-root `vscode` user; passwordless sudo stays
  removed; `cap_drop: [ALL]` and `no-new-privileges` stay in the base compose file.
- Never mount the Docker socket, host home, or SSH directory by default.
- Never auto-source project `.env` files into shells; secrets load only through
  `env-run.sh` / `vibe agent` / `vibe run`.
- Everything under `src/` and the root `vibe`/`install.sh` must remain
  repo-agnostic: no project names, hardcoded paths, services, or published ports.
- Project-specific behavior belongs in `src/templates/` (seeded once, project-owned
  afterwards) or in project hooks — never in shared scripts.
- The engine is docker compose + docker exec, driven by `vibe` — no devcontainer
  CLI, no Node on the host. The container definition is `src/compose/base.yaml`
  with the project's `.vibe/compose.yaml` merged on top; git + docker are the
  entire host requirement. The image chain is build-only `base` service →
  optional project extension (`.vibe/Dockerfile` FROM the base tag), sequenced
  by the launcher — heavyweight optional tooling becomes an extension
  (docs/extending.md), never another shared-Dockerfile flag.
- The agent-state volume name (`agent-state-<workspace-basename>`) is an ABI:
  changing it logs every consumer out of every agent. So are the `src/*` paths
  seeded consumer files reference (`.vibe/harness/src/scripts/...` in
  .claude/settings.json and seeded AGENTS.md). The compose project identity is
  deliberately NOT basename-derived: it is per checkout
  (`vibe-<basename>-<suffix>`, persisted in `.vibe/.project-id`,
  git-info/exclude'd) — the id FILE is the identity, and checkouts that
  predate it adopt their legacy unsuffixed name via the label probe in `vibe`.
- Lifecycle scripts stay idempotent and honor `DEV_BOOTSTRAP_STRICT`; `vibe up`
  runs post-create once per container (`/var/tmp/.vibe-post-created` marker)
  and post-start on every actual start.
- `install.sh` never overwrites an existing `.vibe` without `--force`, and
  `--force` always backs up first.
- The `~/.agents` volume mountpoint must exist in the image owned by `vscode`
  (nothing in the running container can fix a root-owned volume).
- Anything installed for an agent CLI must survive the runtime volume mount at
  `~/.agents` — binaries go to `~/.local/bin`, never under `~/.agents` or `~/.grok`.
- Agents own their auth natively: the harness only points each CLI's config dir
  at the per-project state volume — it never centralizes, brokers, or
  bind-mounts credentials (see `docs/positioning.md`).
- Pin tool versions in Dockerfile ARGs where the upstream supports it; only
  Claude Code (`stable`) and Grok Build (latest stable) are deliberately
  mutable. Small CLI tools may be `INSTALL_*` build args; large ecosystems
  become project image extensions or project base images. Extension images
  must end `USER vscode` (runtime hardening additionally enforces it).
- Host-side scripts (`vibe`, `src/templates/vibe`, `install.sh`, `verify.sh`,
  `examples/render.sh`, `src/scripts/host/`, and the dual-side
  `src/scripts/update.sh` / `src/scripts/repo-root.sh`)
  must stay bash-3.2 compatible and avoid GNU-only flags — they
  run on stock macOS as well as WSL (`verify.sh` gates this under `bash:3.2`).
- The container image must build for both linux/amd64 and linux/arm64
  (Apple Silicon); new installers must handle `aarch64`.
- The legacy `.devcontainer/` layout stays recognized (read-only) by
  `repo-root.sh`, `lib.sh`, `update.sh`, and the container-side walks until
  every known consumer has migrated — `vibe update` running on the old layout
  is how projects cross the engine swap.

## Shell conventions

- Shebang is `#!/usr/bin/env bash` everywhere (no `#!/bin/bash`).
- `set` flags are policy, not entropy — four sanctioned tiers, pick by role:
  - `set -euo pipefail` — the default for every executable script. `lib.sh`
    asserts this tier on behalf of the lifecycle scripts that source it.
  - `set -uo pipefail` (no errexit) — deliberate best-effort paths where one
    failing step must not abort the rest or pollute a caller: agent hooks
    (`agent-state-hook.sh`, `preview-image-hook.sh`), `review.sh`, `doctor.sh`.
  - `set -u` only — host tui renderers driven by tmux `run-shell`, where every
    command is individually `|| true`/exit-0 guarded (`state-render.sh`,
    `sidebar.sh`, `dock.sh`).
  - none — sourced libraries that must not change the caller's options
    (`preview-lib.sh`, `repo-root.sh`) and the Claude statuslines (cosmetic
    hot path: an abort blanks the status line, a soft failure just renders
    less).
- `printf '%q '` command assembly is allowed ONLY at the tmux shell-string
  boundary — the three existing sites (two in `agent-entry.sh`, one in
  `svc.sh`). No command string crossing the docker boundary interpolates
  data; pass values via `docker exec -e` instead.

## Path discovery — three sanctioned idioms

How a script finds the harness/project is one of exactly three shapes; do
not invent a fourth:

1. **Positional anchor** (container lifecycle scripts): source
   `src/scripts/lib-core.sh` — HARNESS_DIR/VIBE_DIR from the script's own
   location, subprocess-light, hook-safe — or `lib.sh` on top of it when
   REPO_ROOT/config.env/DEV_* defaults are needed (never from hooks: lib.sh
   runs git and sources config).
2. **Host $PWD walk** (`repo-root.sh`): PATH-installed entry points (`vibe`,
   `update.sh`) resolve whichever project you stand in. The readlink
   self-canonicalization loops (stock macOS readlink has no `-f`) and the
   tmux conf's tui.sh-stamped `@vibe_harness_dir` option are this idiom's
   plumbing.
3. **Baked two-home self-resolution** (`review.sh`, `show-image.sh`,
   `svc.sh`, `preview-lib.sh`): these also run as baked `/usr/local` copies
   with no harness checkout, so they self-canonicalize, fall back from
   checkout to `/usr/local/lib/vibe`, and read config via their own $PWD
   walk. They can never source lib.sh.

## Before changing code

- Read `docs/architecture.md` and `docs/security.md`.
- Never edit files under the pinned self-submodule copy (`.vibe/harness/`) —
  it is the copy the container runs
  from; changes there land in the nested clone, not this repository. Edit the
  real files at the repository root and sync the submodule forward to test
  (see "Dogfooding" below).
- If touching `src/templates/` or `install.sh`, check every preset delta in
  `install.sh` (mirrored in `examples/render.sh` — verify.sh diffs the rendered
  `examples/` against real installs) and the placeholder set (`@PRESET_NAME@`, `@BASE_IMAGE@`,
  `@INSTALL_CLAUDE_CODE@`, `@INSTALL_CODEX@`, `@INSTALL_GROK@`, `@INSTALL_NODE@`,
  `@INSTALL_BUN@`, `@INSTALL_ROKIT@`, `@EXTRA_COMMANDS@`).
- `install.sh` must not depend on tools absent from a stock WSL Ubuntu host
  (no `jq`; `sed`/`python3` are acceptable).

## Dogfooding — updating and testing the harness on itself

This repository consumes itself: it carries its own project config with the
harness as a **self-submodule** at `.vibe/harness`, so `./vibe up` and
`vibe agent` work here like in any consumer.

- The container runs the **pinned submodule copy** at `.vibe/harness`, not the
  working tree. An uncommitted or unsynced change is invisible to the running
  container — do not conclude a change "doesn't work" before syncing the copy
  forward.
- To test a harness change through the harness itself:

  ```bash
  git commit ...                                # your change, in the outer repo
  git -C .vibe/harness fetch "$PWD" my-branch   # or HEAD's branch
  git -C .vibe/harness checkout FETCH_HEAD
  ./vibe rebuild   # only if Dockerfile/compose files changed
  ```

- Never edit files under `.vibe/harness/` — that is the nested clone; changes
  there do not land in this repository (see "Before changing code").
- The self-submodule is marked `update = none` so recursive clones skip it;
  after a fresh clone, initialize it explicitly with
  `git submodule update --init --checkout .vibe/harness`.
- Syncing the pin as above only moves the local nested clone for testing; the
  committed submodule pin (what consumers and fresh clones get) is a release
  step the maintainer performs — never bump/commit it on your own.

## Required verification

1. `./verify.sh` — must pass. It clones **committed HEAD** for the submodule test,
   so commit before running it.
2. ShellCheck every modified shell file (host may lack it; run it inside a built
   container, which has it).
3. For runtime-visible changes: install a preset into a scratch git repo,
   `vibe up` with a real Docker build, `vibe doctor`, exercise the change, and
   `vibe rebuild` to confirm agent state survives recreation. Clean up scratch
   containers, images, and `agent-state-*` volumes afterwards.
4. Update the affected docs (`README.md` stays task-oriented and short; reference
   material lives in `docs/`).

## Important files

- `src/Dockerfile` — shared image; optional tooling behind `INSTALL_*` build args
- `src/compose/base.yaml` — the container definition (mounts, hardening, env, label)
- `vibe` — host-side launcher (runs from `.vibe/harness/vibe` in consumers)
- `install.sh` — consumer installation: seeds templates, adds the submodule
- `src/scripts/` — shared runtime lifecycle (`lib.sh` holds path discovery: the
  project root is anchored on the directory the harness lives under, NOT
  `git rev-parse` inside the submodule)
- `src/scripts/host/` — WSL-host helpers, not container code
- `src/templates/` — seeds for project-owned files; placeholder-rendered by `install.sh`
- `examples/` — rendered per-preset seeds; `examples/render.sh` + verify.sh keep them fresh
- `verify.sh` — regression checks

## Documentation rules

- Human setup and tasks: `README.md` (short) and `docs/`.
- Agent-facing repository rules: this file only.
- Do not duplicate configuration reference across files — `docs/configuration.md`
  is the single source.

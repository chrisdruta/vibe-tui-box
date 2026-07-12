# Agent instructions

## Purpose

This repository is a shared Dev Container harness consumed by other repositories as
a pinned git submodule at `.devcontainer/harness`. Changes here can affect every
consuming project — bias toward small, reviewable, backward-compatible commits.

## Invariants — do not break these

- The runtime container ends as the non-root `vscode` user; passwordless sudo stays
  removed; `--cap-drop=ALL` and `no-new-privileges` stay in the seeded runArgs.
- Never mount the Docker socket, host home, or SSH directory by default.
- Never auto-source project `.env` files into shells; secrets load only through
  `env-run.sh` / `dev agent` / `dev run`.
- Everything under `scripts/`, `Dockerfile`, and `dev` must remain repo-agnostic:
  no project names, hardcoded paths, services, or published ports.
- Project-specific behavior belongs in `templates/` (seeded once, project-owned
  afterwards) or in project hooks — never in shared scripts.
- Lifecycle scripts stay idempotent and honor `DEV_BOOTSTRAP_STRICT`.
- `install.sh` never overwrites an existing `.devcontainer` without `--force`, and
  `--force` always backs up first.
- The `~/.agents` volume mountpoint must exist in the image owned by `vscode`
  (nothing in the running container can fix a root-owned volume).
- Anything installed for an agent CLI must survive the runtime volume mount at
  `~/.agents` — binaries go to `~/.local/bin`, never under `~/.agents` or `~/.grok`.
- Pin tool versions in Dockerfile ARGs where the upstream supports it; only
  Claude Code (`stable`) and Grok Build (latest stable) are deliberately mutable.
- Host-side scripts (`dev`, `templates/dev`, `install.sh`, `verify.sh`,
  `scripts/host/`) must stay bash-3.2 compatible and avoid GNU-only flags — they
  run on stock macOS as well as WSL (`verify.sh` gates this under `bash:3.2`).
- The container image must build for both linux/amd64 and linux/arm64
  (Apple Silicon); new installers must handle `aarch64`.

## Before changing code

- Read `docs/architecture.md` and `docs/security.md`.
- If touching `templates/` or `install.sh`, check every preset delta in
  `install.sh` and the placeholder set (`@PRESET_NAME@`, `@BASE_IMAGE@`,
  `@INSTALL_BUN@`, `@INSTALL_ROKIT@`, `@EXTRA_EXTENSIONS@`, `@EXTRA_COMMANDS@`).
- `install.sh` must not depend on tools absent from a stock WSL Ubuntu host
  (no `jq`; `sed`/`python3` are acceptable).

## Required verification

1. `./verify.sh` — must pass. It clones **committed HEAD** for the submodule test,
   so commit before running it.
2. ShellCheck every modified shell file (host may lack it; run it inside a built
   container, which has it).
3. For runtime-visible changes: install a preset into a scratch git repo,
   `dev up` with a real Docker build, `dev doctor`, exercise the change, and
   `dev rebuild` to confirm agent state survives recreation. Clean up scratch
   containers, images, and `agent-state-*` volumes afterwards.
4. Update the affected docs (`README.md` stays task-oriented and short; reference
   material lives in `docs/`).

## Important files

- `Dockerfile` — shared image; optional tooling behind `INSTALL_*` build args
- `dev` — host-side launcher (runs from `.devcontainer/harness/dev` in consumers)
- `install.sh` — consumer installation: seeds templates, adds the submodule
- `scripts/` — shared runtime lifecycle (`lib.sh` holds path discovery: the project
  root is anchored on `.devcontainer/`, NOT `git rev-parse` inside the submodule)
- `scripts/host/` — WSL-host helpers, not container code
- `templates/` — seeds for project-owned files; placeholder-rendered by `install.sh`
- `verify.sh` — regression checks

## Documentation rules

- Human setup and tasks: `README.md` (short) and `docs/`.
- Agent-facing repository rules: this file only.
- Do not duplicate configuration reference across files — `docs/configuration.md`
  is the single source.

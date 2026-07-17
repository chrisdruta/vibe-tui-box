# Changelog

Consumers pin a commit; tags mark intentional upgrade points
(see [docs/updating.md](docs/updating.md)).

## Unreleased

- **Login-gated GitHub git wiring**: when (and only when) `gh` is logged in,
  `post-start.sh` wires gh as git's credential helper and rewrites
  `git@github.com:` remotes to HTTPS inside the container — restoring the
  container-local `~/.gitconfig` after every rebuild, so an SSH-cloned repo
  shared with the host stays pushable in-container. The `gh auth login` is the
  opt-in; never logging in leaves git untouched. `vibe doctor` reports the
  state (logged in + wired / not wired / not logged in). Also: post-start's
  exec-bit self-heal now covers the renamed `vibe` wrapper.

## v0.4.0 — 2026-07-17

- **Per-project `gh` logins**: `GH_CONFIG_DIR` now points into the agent-state
  volume, so `gh auth login` (recommended: paste a per-project fine-grained
  PAT — single repo, Contents read/write, no `workflow` scope) persists across
  rebuilds and stays compartmentalized per project. Host-level `GH_TOKEN`
  forwarding is unchanged but documented as the one-token-everywhere trade;
  `gh auth login` refuses while it is set. See configuration.md → GitHub access.
- **Seeded Claude settings deny `.env` reads**: `Read(./.env)` /
  `Read(./.env.*)` join the sudo/su denies in the seeded
  `.claude/settings.json` — an agent-level guardrail against prompt-injected
  secret reads, not a boundary (see security.md, which also documents the
  `/dev/null`-over-secret-file mount recipe for project secrets agents never
  need). Existing projects keep their own settings file; merge manually.
- **The launcher is now `vibe`** (was `dev`): seeded as `.devcontainer/vibe`,
  real script at `harness/vibe`. `harness/dev` remains as a back-compat shim so
  existing consumer wrappers keep working across a pin bump, and the seeded
  wrapper tries `vibe` then `dev` so it also works against older pins.
  Entries below predate the rename; read their `dev` commands as `vibe`.
- **Global launcher**: `vibe` resolves the target project by walking up from
  the current directory to the nearest `.devcontainer/devcontainer.json`
  (falling back to the project the script lives in) and survives being
  symlinked (`readlink -f`) — one `~/.local/bin/vibe` symlink now serves every
  harness project from any subdirectory. The previously documented host-wide
  symlink was broken.
- **Auto-up**: container commands (`agent`, `shell`, `run`, `exec`, `doctor`,
  `bootstrap`, `clip`) start the container when it isn't running (detected via
  the devcontainer CLI's `devcontainer.local_folder` label, or an exec probe
  when no docker client is present). Start-up progress goes to stderr so
  `vibe run` stdout stays pipeable; a cold `vibe agent` is the whole morning
  routine.
- **Docs: [positioning.md](docs/positioning.md)** — the layer this harness
  occupies vs. agent loops and orchestrator UIs, its principles and non-goals,
  and the recorded decision to keep auth agent-native and per-project (no
  centralized credential store); cross-linked from agent-state and security
  docs.
- **`dev agent --cold`**: fresh-perspective agent session without repo instruction
  files — Claude via `--safe-mode`, Codex via `-c project_doc_max_bytes=0`; agents
  without a known skip mechanism refuse. Cold runs get their own tmux session
  (`<session>-cold`) so they never reattach to a warm one.
- **`dev agent -a/--agent CMD`**: per-invocation agent override (e.g.
  `dev agent -a codex`, composable with `--cold`) without touching
  `DEV_AGENT_CMD`; each override gets its own tmux session (`<session>-codex`).
- **`dev clip [DIR]`**: save the host clipboard image into the container's `/tmp`
  (or a workspace-relative `DIR` to keep it) and put the container path on the
  clipboard — the workaround for Ctrl-V image paste being unreachable from
  inside the container. WSL (PowerShell) and macOS (AppleScript); new host
  helper `scripts/host/clip-image.sh`.
- **Codex plugin for Claude Code auto-installed** when `INSTALL_CODEX=true`:
  post-create adds [openai/codex-plugin-cc](https://github.com/openai/codex-plugin-cc)
  (user scope, persisted in the agent-state volume), giving Claude sessions
  `/codex:review`, `/codex:rescue`, and friends without switching panes. Warns
  instead of failing bootstrap when offline.

## v0.3.0 — 2026-07-12

- **playwright-deps Dev Container Feature** + [browser-automation recipe](docs/browser-automation.md):
  headless Chromium for shell-driven agent browsing (`@playwright/cli`). Feature
  option `version` pins the playwright release that resolves the apt dependency list.
- **`dev agent` can run in a persistent tmux session** (`DEV_AGENT_TMUX`, seeded on
  for new installs; unset = previous behavior). Rerunning attaches; detaching keeps
  the agent alive.
- **Onboarding scaffolding**: seeded `.devcontainer/AGENTS.md` (container rules for
  agents; import via `@.devcontainer/AGENTS.md`), [docs/onboarding.md](docs/onboarding.md)
  with an agent reconcile prompt.
- **Default Claude statusline**: harness-shipped `scripts/statusline.sh` /
  `scripts/subagent-statusline.sh`; `install.sh` seeds `.claude/settings.json`
  (statusline + sudo/su deny) when the project has none.
- **Hardening**: Dockerfile builds with `pipefail` and asserts installed binaries
  (a failed `curl | bash` previously produced a cached layer with the tool
  missing); launcher exec bits are recorded in the git index and self-healed at
  post-start (survives `core.fileMode=false` checkouts).
- verify.sh covers `features/`, seeded files, and index modes; CI workflow added.

## v0.2.0 — 2026-07-12

- macOS support: cross-platform Ollama host helper, arm64-verified image, docs.

## v0.1.0 — 2026-07-12

- Initial release: generic agent dev-container harness (hardened non-root image,
  preset installer, lifecycle scripts, agent-state volume, docs).

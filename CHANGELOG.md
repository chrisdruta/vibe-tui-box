# Changelog

Consumers pin a commit; tags mark intentional upgrade points
(see [docs/updating.md](docs/updating.md)).

## v0.7.1 — 2026-07-18

- **Changed: the image viewer is passive by default.** Verdict keys and the
  per-image verdict label now exist only when a decisions target is
  configured; without one the viewer just views — the right behavior for the
  everyday case of glancing at a `vibe clip` capture or a prompt paste, where
  "undecided" demanded a decision nobody owed. Review mode activates via
  `VIBE_PREVIEW_DECISIONS` in `config.env` (every instance, including the
  `prefix+i` window) or the new per-batch form below. Rebuild required to
  bake the new viewer.
- **New: `vibe review DIR`** reviews one directory as a batch: watches `DIR`
  (workspace-relative), records verdicts to `DIR/vibe-decisions.jsonl`. Built
  for staged generation pipelines — one directory and one `vibe review` per
  approval gate; stage semantics (regenerate vs refine on reject) stay in the
  project's agent skills.
- **New: reject notes.** In review mode `n`/`x` prompts for an optional
  one-line reason (Enter skips) recorded as a `"note"` field in the verdict
  JSONL — turns reject-and-redo loops from rerolling into steering.
- **Changed: agent onboarding prompt clones fresh.** The paste-prompt in
  [docs/onboarding.md](docs/onboarding.md) no longer looks for (or reuses) a
  local `~/dev` scaffold clone; it always shallow-clones the latest harness
  to a throwaway `/tmp` directory.

## v0.7.0 — 2026-07-18

- **New: image review — `vibe review` and the tmux `preview` window.**
  `scripts/preview-viewer.sh` watches a directory for image batches
  (`VIBE_PREVIEW_DIR` / `VIBE_PREVIEW_GLOB` in `config.env`), renders them
  newest-first with single-key navigation, and appends approve/reject
  verdicts to a JSONL file (`VIBE_PREVIEW_DECISIONS`; append-only, last line
  per path wins) for a pipeline or agent to consume. Run it as `vibe review`
  in any host terminal — chafa renders straight to it, no tmux in the pixel
  path (the reliable mode) — or as a dedicated `preview` tmux window via
  `prefix + i`. Baked into the image as `/usr/local/bin/vibe-preview` —
  rebuild required.
- **Changed: Claude Code image hooks feed the review window** instead of
  popping preview splits — transient splits cannot reliably hold a sixel
  render on tmux 3.5a (client redraws replace images with placeholders;
  passthrough smears next to a busy TUI). The hook ensures the window
  exists (detached, never steals focus) and enqueues the path; the window
  name lights up via `monitor-activity` when unfocused. A prompt paste the
  TUI converts to an `[Image #N]` attachment carries no path in the hook
  payload; the hook falls back to the newest `/tmp/clip-*.png` under 10
  minutes old. `VIBE_PREVIEW_SECONDS` and the 30s debounce are retired.
- **Changed: in-tmux sixel rendering hardened** — the viewer sizes images by
  measuring the emitted sixel raster (chafa's captured-output cell metrics
  are unreliable), centers with margins so the header stays visible, ships
  the image as a self-positioning anchored passthrough envelope, and heals
  redraw-wiped pixels flicker-free a tick later. `vibe show` with no
  argument now also considers the watch directory.

## v0.6.0 — 2026-07-18

- **New: auto image preview in Claude Code sessions** — hooks in
  `templates/claude-settings.json` (`UserPromptSubmit` + `PostToolUse: Read`
  → `scripts/preview-image-hook.sh`) pop a self-closing tmux split whenever
  an image path appears in your prompt or the agent reads an image file
  (focused only for the instant the sixel renders, then focus returns). Tune the duration with `VIBE_PREVIEW_SECONDS` in `config.env`.
  Existing projects adopt the hooks by merging the template block at their
  next pin update.
- **New: `vibe show [PATH]`** — sixel image preview in the terminal, the
  companion to `vibe clip`: with no argument it renders the newest
  `/tmp/clip-*.png` so you can see what an agent is about to look at (agent
  TUIs only show `[Image 1]` placeholders). Also `prefix + i` inside the agent
  tmux session opens the same preview in a transient split pane. Adds `chafa`
  and `libsixel-bin` to the image — rebuild required.

## v0.5.2 — 2026-07-17

- **Fix: `vibe clip` broken on WSL** (v0.5.1 regression) — WSL only shares
  environment variables listed in `WSLENV` with Windows processes, so
  `CLIP_WIN_PATH` (introduced by v0.5.1's injection hardening) was `$null`
  inside `powershell.exe` and the clipboard save crashed — then falsely
  reported success, cascading into a missing-file error. The variable is now
  forwarded via `WSLENV`, the PowerShell step only reports `SAVED` after an
  actual save (real errors are surfaced instead of "No image on the
  clipboard"), and the script verifies the file exists before streaming it
  into the container.

## v0.5.1 — 2026-07-17

- **Security fixes from a code review** (host-boundary hardening):
  - `clip-image.sh` no longer interpolates the destination path into the
    PowerShell or AppleScript it runs — a path containing a quote could break
    out into **host** command execution. The path now travels as an
    environment variable (PowerShell) / run-handler argument (AppleScript).
  - `clip-image.sh` confines workspace-mode writes: the destination is
    resolved with `pwd -P` and rejected if it escapes the real repo root
    (defeating a repo-planted symlink like `.captures -> ../../.ssh`), and an
    existing symlink at the target file is refused.
  - `vibe clip DIR` (workspace mode) no longer auto-starts the container — it
    writes straight to the bind mount, so nothing needs to be running.
  - The agent-command split (`DEV_AGENT_CMD`, `-a`) runs under `set -f`, so a
    value containing `*` can no longer glob-expand repo filenames into agent
    arguments.
  - Launcher symlink resolution replaces GNU-only `readlink -f` with a portable
    loop (restores the stock-macOS bash-3.2 host invariant).
  - post-start's GitHub rewrite now also covers `ssh://git@github.com/` remotes,
    set idempotently (unset-all then add) so restarts don't accumulate values.
  - The `npx @devcontainers/cli` fallback is version-pinned (`@0.87.0`) instead
    of resolving mutable `latest` on the host; override per run with
    `DEVCONTAINER_CLI_SPEC`.
- **Agent-driven update prompt** in [updating.md](docs/updating.md): paste-ready
  prompt that moves the pin, reads the changelog between versions, reconciles
  the project-owned seeded files against the new templates (project values win
  on conflict), and reports what needs a human decision. Companion to the
  onboarding prompt; linked from the README.

## v0.5.0 — 2026-07-17

- **`dev` back-compat shim removed**: `harness/dev` is gone and the seeded
  wrapper execs `harness/vibe` directly. Pre-v0.4.0 installs must replace
  their `.devcontainer/dev` wrapper in the same commit that moves the pin to
  ≥ v0.5.0 — see [updating.md](docs/updating.md) → Crossing the v0.4.0 rename.
- **Login-gated GitHub git wiring**: when (and only when) `gh` is logged in,
  `post-start.sh` wires gh as git's credential helper and rewrites
  `git@github.com:` remotes to HTTPS inside the container — restoring the
  container-local `~/.gitconfig` after every rebuild, so an SSH-cloned repo
  shared with the host stays pushable in-container. The `gh auth login` is the
  opt-in; never logging in leaves git untouched. `vibe doctor` reports the
  state (logged in + wired / not wired / not logged in). configuration.md
  gains a fine-grained-PAT permission quick reference, install.sh prints the
  permission set in its next-steps output, and updating.md documents crossing
  v0.4.0 from older installs (GH_CONFIG_DIR, settings merge, wrapper rename).
  Also: post-start's
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

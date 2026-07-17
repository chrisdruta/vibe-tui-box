# Daily usage

All commands run via the seeded wrapper — or as plain `vibe` with the
[global install](#global-install) below:

```bash
./.devcontainer/vibe COMMAND [ARGS...]
```

| Command     | Does                                                                  |
| ----------- | --------------------------------------------------------------------- |
| `up`        | Build (if needed) and start the Dev Container                          |
| `rebuild`   | Recreate the container — required after editing `devcontainer.json` or the Dockerfile |
| `build`     | Build the image only                                                   |
| `shell`     | Open a Bash shell in the container                                     |
| `agent [--cold] [-a CMD]` | Run the configured default agent (`DEV_AGENT_CMD`) with explicit `.env` loading; with `DEV_AGENT_TMUX=1`, inside a persistent tmux session. `--cold`: fresh-perspective session without repo instruction files. `-a`/`--agent`: run `CMD` instead of `DEV_AGENT_CMD` for this invocation |
| `run CMD`   | Run any command with explicit `.env` loading (e.g. `vibe run codex`)    |
| `exec CMD`  | Run any command **without** `.env` loading                             |
| `doctor`    | Check the environment; prints OK/MISS per requirement                  |
| `bootstrap` | Rerun create-time dependency setup (idempotent)                        |
| `clip [DIR]` | Save the host clipboard image into container `/tmp`, or `DIR` in the workspace (image-paste workaround) |
| `show [PATH]` | Preview an image in the terminal via sixel (default: newest `vibe clip` capture) |

The launcher uses a locally installed `devcontainer` CLI, falling back to a
**version-pinned** `npx -y @devcontainers/cli@<pinned>` (unpinned `npx` would
run whatever `latest` resolves to as the host user). Override the pin for one
run with `DEVCONTAINER_CLI_SPEC=@devcontainers/cli@X.Y.Z vibe ...`, or just
install the CLI globally to skip the fallback entirely.

## Global install

The launcher targets the nearest ancestor of the current directory with a
`.devcontainer/devcontainer.json`, so one symlink on the host `PATH` serves
every harness project:

```bash
ln -s ~/dev/any-project/.devcontainer/harness/vibe ~/.local/bin/vibe
cd ~/dev/other-project && vibe agent    # targets other-project
```

Only when run from outside any project does it fall back to the project the
script itself lives in.

Container commands (`agent`, `shell`, `run`, `exec`, `doctor`, `bootstrap`,
`clip`, `show`) start the container automatically when it isn't running — a cold
`vibe agent` is the whole morning routine. Start-up progress goes to stderr,
so `vibe run` output stays pipeable.

## Typical day

```bash
vibe agent                       # starts the container if needed, attaches Claude
vibe exec uv run pytest
```

## tmux

With `DEV_AGENT_TMUX=1` in `config.env` (the seeded default for new installs),
`vibe agent` runs inside a tmux session named `agent` (`DEV_AGENT_TMUX_SESSION`):

- **Detach** with `Ctrl-b d` — the agent keeps running; closing the terminal or
  losing the connection also leaves it running.
- **Reattach** by rerunning `./.devcontainer/vibe agent` (arguments are ignored
  when an existing session is attached; the session ends when the agent exits).
- **One-off plain run**: `vibe run claude`, or set `DEV_AGENT_TMUX=0`.

Run several agents side by side in tmux (installed in the image):

```bash
./.devcontainer/vibe shell
tmux
# pane 1: claude    pane 2: codex    pane 3: grok
```

## Cold sessions (fresh perspective)

`vibe agent --cold` starts the agent without the repo's instruction files, for an
unbiased second opinion — reviewing a design without the repo's conventions
arguing back, or checking whether docs stand on their own:

- **Claude Code** runs with `--safe-mode`: no CLAUDE.md/AGENTS.md memory, and all
  `.claude/` customizations (skills, plugins, hooks, MCP servers, statusline) are
  off for the session. Auth, model, built-in tools, and permissions are normal.
- **Codex** (`DEV_AGENT_CMD=codex`) runs with `-c project_doc_max_bytes=0`, which
  drops all AGENTS.md loading.
- Agents with no known instruction-skip mechanism (e.g. Grok) refuse with an
  error instead of silently running warm.

Remaining arguments pass through (`vibe agent --cold --continue`), and it
composes with the per-invocation agent selector:

```bash
./.devcontainer/vibe agent -a codex          # Codex session (DEV_AGENT_CMD untouched)
./.devcontainer/vibe agent --cold -a codex   # Codex without AGENTS.md
./.devcontainer/vibe agent -a "codex --model gpt-5"   # override may carry arguments
```

With `DEV_AGENT_TMUX=1` each variant uses its own tmux session — `agent`,
`agent-cold`, `agent-codex`, `agent-codex-cold` — so runs never reattach to the
wrong session and can happily run side by side.

## Pasting images to an agent

Ctrl-V image paste cannot work inside the container: the agent reads the OS
clipboard from its own process, and the container has no WSL interop or display
server to reach it (the terminal only ever sends text down the pty). Instead,
with an image on the host clipboard, run on the host:

```bash
./.devcontainer/vibe clip
# In the container: /tmp/clip-20260715-093042.png
# (path copied to clipboard)
```

The image lands in the container's `/tmp` (nothing is written to the repo), and
the printed path replaces the image on the host clipboard — paste it straight
into the agent prompt. Works on WSL (PowerShell) and macOS (AppleScript); the
container must be running. Files vanish on rebuild, as `/tmp` is
container-local.

To keep captures instead, pass a workspace-relative directory — the image is
written straight through the bind mount (no running container required):

```bash
./.devcontainer/vibe clip .captures
# Saved: .captures/clip-20260715-093042.png
```

Gitignore the directory if you use this mode routinely.

### Seeing what you clipped

Agent TUIs show attached images only as a `[Image 1]` placeholder — the Claude
Code terminal UI cannot render images inline (upstream: not planned). To eyeball
an image, render it with sixel graphics instead:

- `vibe show` (host or container) previews the newest `/tmp/clip-*.png`;
  `vibe show PATH` previews any image.
- Inside the agent tmux session, **prefix + `i`** opens the same preview in a
  transient split pane; Enter closes it.

Both use `chafa`. Inside tmux the sixel data travels in a passthrough envelope
(tmux's own sixel engine doesn't reliably re-emit images to the client), so the
outer terminal must render sixel itself — Windows Terminal ≥ 1.22 qualifies.
Outside tmux, `chafa` probes the terminal and falls back to unicode blocks
where sixel is unavailable.

## Troubleshooting

- **Start with `vibe doctor`.** It verifies non-root execution, workspace
  writability, required commands (`DEV_REQUIRED_COMMANDS`), the agent command,
  and the absence of the Docker socket and passwordless sudo. Its output is also
  logged to `/tmp/dev-doctor.log` inside the container on every start.
- **Changed `devcontainer.json` or the Dockerfile and nothing happened?**
  `vibe up` reuses an existing container; run `vibe rebuild`.
- **`Harness submodule is missing`** — run `git submodule update --init`.
- **Build warning `InvalidDefaultArgInFrom: Default value for ARG $BASE_IMAGE
  ... (line 4)`** — harmless, and not from the harness Dockerfile (whose
  `BASE_IMAGE` has a valid default). With `updateRemoteUserUID` the
  devcontainer CLI runs a second UID-sync build using its own bundled
  `updateUID.Dockerfile`, which declares `ARG BASE_IMAGE` without a default;
  Docker ≥ 4.33 lints that file. Fix belongs upstream in devcontainers/cli.
- **Bootstrap fails loudly** — that is `DEV_BOOTSTRAP_STRICT=1` doing its job:
  a detected manifest's tool is missing. Install the tool via build args or set
  `DEV_BOOTSTRAP_STRICT=0` to degrade to warnings
  (see [configuration.md](configuration.md)).
- **Agent asks to log in again after a rebuild** — the state volume persists
  across rebuilds but is per project folder name; see [agent-state.md](agent-state.md).
- **Slow file operations on macOS** — Docker Desktop bind mounts (virtiofs) are
  slower than WSL's native ext4; if a heavy directory (e.g. `node_modules`) hurts,
  move it to a named volume in the project's `devcontainer.json`.
- **Root shell for maintenance**: `docker exec -u root -it <container> bash`
  (deliberately outside the normal flow).

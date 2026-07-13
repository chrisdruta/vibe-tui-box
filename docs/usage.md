# Daily usage

All commands run from the project root via the seeded wrapper:

```bash
./.devcontainer/dev COMMAND [ARGS...]
```

| Command     | Does                                                                  |
| ----------- | --------------------------------------------------------------------- |
| `up`        | Build (if needed) and start the Dev Container                          |
| `rebuild`   | Recreate the container — required after editing `devcontainer.json` or the Dockerfile |
| `build`     | Build the image only                                                   |
| `shell`     | Open a Bash shell in the container                                     |
| `agent`     | Run the configured default agent (`DEV_AGENT_CMD`) with explicit `.env` loading; with `DEV_AGENT_TMUX=1`, inside a persistent tmux session |
| `run CMD`   | Run any command with explicit `.env` loading (e.g. `dev run codex`)    |
| `exec CMD`  | Run any command **without** `.env` loading                             |
| `doctor`    | Check the environment; prints OK/MISS per requirement                  |
| `bootstrap` | Rerun create-time dependency setup (idempotent)                        |

The launcher uses a locally installed `devcontainer` CLI, falling back to
`npx -y @devcontainers/cli`. It is repo-agnostic — a host-wide symlink also works
(`ln -s ~/dev/my-project/.devcontainer/harness/dev ~/.local/bin/dev`), resolving
the project from its own location.

## Typical day

```bash
./.devcontainer/dev up          # morning: container resumes, doctor runs
./.devcontainer/dev agent       # interactive Claude session
./.devcontainer/dev exec uv run pytest
```

## tmux

With `DEV_AGENT_TMUX=1` in `config.env` (the seeded default for new installs),
`dev agent` runs inside a tmux session named `agent` (`DEV_AGENT_TMUX_SESSION`):

- **Detach** with `Ctrl-b d` — the agent keeps running; closing the terminal or
  losing the connection also leaves it running.
- **Reattach** by rerunning `./.devcontainer/dev agent` (arguments are ignored
  when an existing session is attached; the session ends when the agent exits).
- **One-off plain run**: `dev run claude`, or set `DEV_AGENT_TMUX=0`.

Run several agents side by side in tmux (installed in the image):

```bash
./.devcontainer/dev shell
tmux
# pane 1: claude    pane 2: codex    pane 3: grok
```

## Troubleshooting

- **Start with `dev doctor`.** It verifies non-root execution, workspace
  writability, required commands (`DEV_REQUIRED_COMMANDS`), the agent command,
  and the absence of the Docker socket and passwordless sudo. Its output is also
  logged to `/tmp/dev-doctor.log` inside the container on every start.
- **Changed `devcontainer.json` or the Dockerfile and nothing happened?**
  `dev up` reuses an existing container; run `dev rebuild`.
- **`Harness submodule is missing`** — run `git submodule update --init`.
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

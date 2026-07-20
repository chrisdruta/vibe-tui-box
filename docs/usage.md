# Daily usage

All commands run via the seeded root symlink (or `.vibe/vibe`, the same
thing) — or as plain `vibe` with the [global install](#global-install) below:

```bash
./vibe COMMAND [ARGS...]
```

| Command     | Does                                                                  |
| ----------- | --------------------------------------------------------------------- |
| `up`        | Build (if needed) and start the container, then run the lifecycle hooks (post-create once per container, post-start per start) |
| `rebuild`   | Fresh image + fresh container — required after editing `compose.yaml` or the Dockerfile |
| `build`     | Build the image only                                                   |
| `config`    | Print the merged compose config (harness base + project override)      |
| `status`    | Show this project's container(s) — name, state, image, ports           |
| `down`      | Stop & remove this project's container; named volumes (agent state) are kept — `vibe up` recreates it |
| `shell`     | Open a Bash shell in the container                                     |
| `attach [SESSION]` | Attach (or create) a tmux session in the container — the door into a services session your `project/post-start.sh` stands up. Name: argument > `DEV_ATTACH_TMUX_SESSION` > `main` |
| `agent [--cold] [-a CMD]` | Run the configured default agent (`DEV_AGENT_CMD`) with explicit `.env` loading; with `DEV_AGENT_TMUX=1`, inside a persistent tmux session. `--cold`: fresh-perspective session without repo instruction files. `-a`/`--agent`: run `CMD` instead of `DEV_AGENT_CMD` for this invocation |
| `run CMD`   | Run any command with explicit `.env` loading (e.g. `vibe run codex`)    |
| `exec CMD`  | Run any command **without** `.env` loading                             |
| `doctor`    | Check the environment; prints OK/MISS per requirement                  |
| `bootstrap` | Rerun create-time dependency setup (idempotent)                        |
| `update [TAG]` | Move the harness pin (fetch, changelog delta, checkout, stage)      |
| `clip [DIR]` | Save the host clipboard image into container `/tmp`, or `DIR` in the workspace (image-paste workaround) |
| `show [PATH]` | Preview an image in the terminal via sixel (default: newest `vibe clip` capture) |
| `review [DIR]` | Browse/review images with yazi (verdict keys, badges — below)       |

The launcher drives docker directly: `docker compose` for the container
lifecycle (the harness base compose file with the project's
`.vibe/compose.yaml` merged on top — `vibe config` prints the result) and
`docker exec` for everything that runs inside. There is no devcontainer CLI
and no Node dependency; git and Docker are the whole host requirement.

## Global install

The launcher targets the nearest ancestor of the current directory with a
`.vibe/compose.yaml`, so one symlink on the host `PATH` serves every harness
project:

```bash
ln -s ~/dev/any-project/.vibe/harness/vibe ~/.local/bin/vibe
cd ~/dev/other-project && vibe agent    # targets other-project
```

Only when run from outside any project does it fall back to the project the
script itself lives in.

Container commands (`agent`, `shell`, `run`, `exec`, `doctor`, `bootstrap`,
`clip`, `show`, `review`) start the container automatically when it isn't
running — a cold `vibe agent` is the whole morning routine. Start-up progress
goes to stderr, so `vibe run` output stays pipeable.

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
- **Reattach** by rerunning `./vibe agent` (arguments are ignored
  when an existing session is attached; the session ends when the agent exits).
- **One-off plain run**: `vibe run claude`, or set `DEV_AGENT_TMUX=0`.

Run several agents side by side in tmux (installed in the image):

```bash
./vibe shell
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
./vibe agent -a codex          # Codex session (DEV_AGENT_CMD untouched)
./vibe agent --cold -a codex   # Codex without AGENTS.md
./vibe agent -a "codex --model gpt-5"   # override may carry arguments
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
./vibe clip
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
./vibe clip .captures
# Saved: .captures/clip-20260715-093042.png
```

Gitignore the directory if you use this mode routinely.

### Reviewing images

Agent TUIs show attached images only as a `[Image 1]` placeholder — the Claude
Code terminal UI cannot render images inline (upstream: not planned). The
harness fills the gap with [yazi](https://yazi-rs.github.io/) (baked into the
image, pinned by checksum) plus a one-shot renderer:

- **`vibe review [DIR]`** (host, any terminal tab) opens yazi in the invoking
  terminal at `DIR` (workspace-relative) or the current directory — browse,
  search, and preview images with yazi's native rendering (sixel on Windows
  Terminal ≥ 1.22; protocol auto-detected).
- Inside the agent tmux session, **prefix + `i`** jumps to (or creates) the
  **`preview` window** — a yazi instance in a dedicated window (tmux 3.5a
  drops sixel on redraws of inactive panes, so review gets a full window, not
  a split).
- `vibe show [PATH]` renders one image in the invoking terminal and returns —
  the no-TUI path; with no argument, the newest clip or
  `VIBE_PREVIEW_DIR` image. `vibe show --diag PATH` explains a failing
  render (format vs extension, renderer, stderr) instead of drawing.

Both entry points run a **layered config**: the harness supplies the review
machinery (the `vibe.yazi` plugin, the verdict keybindings, a ✓/✗ badge
column), and the project-owned `.vibe/yazi/` (seeded once from
`templates/yazi/`) supplies preferences on top — its `yazi.toml`/
`theme.toml` replace the harness's, its `keymap.toml` entries merge in
front (so they win on conflict), and its `init.lua` runs after the
harness's. Review keys — chosen because yazi's defaults leave them unbound,
so `a`/`r` keep their create/rename meanings:

- `A` — approve the hovered image (toast confirms)
- `R` — reject it; an input box asks for an optional one-line note (Enter
  skips, Esc cancels the reject)

Judged files get a persistent `✓`/`✗` badge in the list (the `verdict`
linemode — existing verdicts load as soon as a directory is entered).
Verdicts append as
`{"ts":…,"path":…,"verdict":"approve"|"reject"[,"note":…]}` JSONL lines to
`.review-decisions.jsonl` **in the directory being browsed** — beside the
images they judge, hidden from the listing as a dotfile. Set
`VIBE_REVIEW_DECISIONS` in `config.env` to send every verdict to one fixed
file instead. Append-only; the last line per path wins, so re-deciding an
image just works. A pipeline or agent consumes it with e.g.
`jq -s 'group_by(.path) | map(last)' .review-decisions.jsonl`. For staged
pipelines (concept art → angle sheets → renders), give each batch its own
directory and run `vibe review <dir>` per gate. The raw helper is also
scriptable: `vibe-verdict reject PATH note words…`.

Claude Code sessions feed the preview window automatically: hooks in the
seeded `.claude/settings.json` (from `src/templates/claude-settings.json`) fire
when you submit a prompt containing an image path and whenever the agent
`Read`s an image file. The hook ensures the `preview` window exists and
tells its yazi to reveal the image over DDS (`ya emit-to`); if the window
isn't focused, its name lights up in the status bar instead of anything
stealing your screen. When Claude Code converts a pasted path into an
`[Image #N]` *attachment* the path never reaches the hook (the payload only
carries the placeholder), so the hook falls back to the newest
`/tmp/clip-*.png` captured in the last 10 minutes — right for the
`vibe clip` → paste flow. Existing projects adopt the hooks by merging the
template block during a [pin update](updating.md).

Rendering notes: yazi detects the terminal's image protocol itself (sixel on
Windows Terminal ≥ 1.22; kitty/iTerm2 protocols elsewhere; chafa cell-art
fallback) — check `tmux display -p '#{client_termfeatures}'` when previews
degrade inside tmux. `vibe show` keeps the harness's own render path for
one-shot use: small `png`/`jpeg`/`gif`/`bmp` render through `img2sixel` with
integer nearest-neighbor upscaling (actual pixels, no blending),
`webp`/`avif`/`svg` via `chafa`, and the real format is always sniffed from
file content, never the extension.

## Troubleshooting

- **Start with `vibe doctor`.** It verifies non-root execution, workspace
  writability, required commands (`DEV_REQUIRED_COMMANDS`), the agent command,
  and the absence of the Docker socket and passwordless sudo. Its output is also
  logged to `/tmp/dev-doctor.log` inside the container on every start.
- **Changed `compose.yaml` or the Dockerfile and nothing happened?**
  `vibe up` recreates on compose-config changes, but image contents need
  `vibe rebuild`. `vibe config` shows what the merged config actually says.
- **`Harness launcher not found`** — run `git submodule update --init`.
- **Bootstrap fails loudly** — that is `DEV_BOOTSTRAP_STRICT=1` doing its job:
  a detected manifest's tool is missing. Install the tool via build args or set
  `DEV_BOOTSTRAP_STRICT=0` to degrade to warnings
  (see [configuration.md](configuration.md)).
- **Agent asks to log in again after a rebuild** — the state volume persists
  across rebuilds but is per project folder name; see [agent-state.md](agent-state.md).
- **Rotated `GH_TOKEN` on the host not visible in the container** — container
  environment is baked at create time; `vibe down && vibe up` re-reads it.
- **Slow file operations on macOS** — Docker Desktop bind mounts (virtiofs) are
  slower than WSL's native ext4; if a heavy directory (e.g. `node_modules`) hurts,
  move it to a named volume in the project's `compose.yaml`.
- **Root shell for maintenance**: `docker exec -u root -it <container> bash`
  (deliberately outside the normal flow).

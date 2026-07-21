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
| `status`    | Show **all** of this project's service containers — dev and any sidecars declared in `.vibe/compose.yaml` — running or stopped |
| `down`      | Stop & remove **all** of this project's service containers (dev + sidecars) plus the project network; named volumes (agent state, sidecar data) are kept — `vibe up` recreates the rest |
| `shell`     | Open a Bash shell in the container                                     |
| `attach [SESSION]` | Attach (or create) a tmux session in the container — the door into the services session `vibe-svc` populates from `project/post-start.sh` ([services.md](services.md)). Name: argument > `DEV_ATTACH_TMUX_SESSION` > `services` |
| `agent [--cold] [-a CMD]` | Run the configured default agent (`DEV_AGENT_CMD`) with explicit `.env` loading; with `DEV_AGENT_TMUX=1`, inside a persistent tmux session. `--cold`: fresh-perspective session without repo instruction files. `-a`/`--agent`: run `CMD` instead of `DEV_AGENT_CMD` for this invocation |
| `run CMD`   | Run any command with explicit `.env` loading (e.g. `vibe run codex`)    |
| `exec CMD`  | Run any command **without** `.env` loading                             |
| `doctor`    | Check the environment; prints OK/MISS per requirement                  |
| `bootstrap` | Rerun create-time dependency setup (idempotent)                        |
| `update [TAG]` | Move the harness pin (fetch, changelog delta, checkout, stage)      |
| `clip [DIR]` | Save the host clipboard image into container `/tmp`, or `DIR` in the workspace (image-paste workaround) |
| `show [PATH]` | Preview an image in the terminal via sixel (default: newest `vibe clip` capture) |
| `review [DIR]` | Browse/review images with yazi (verdict keys, badges — below)       |
| `tui`       | The workspace as a riced host-side tmux — agent pane, host shell pane, tabs, palette ([below](#the-tui-vibe-tui)) |

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

— or get the whole thing as one themed surface: see
[The TUI](#the-tui-vibe-tui).

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

## The TUI (`vibe tui`)

The front door: a **host-side tmux** owns the layout, tabs, and theme, and
the terminal is just a fullscreen window. Container tmux sessions keep
persistence underneath — the agent pane is `./vibe agent` attaching to
session `agent`, so closing the terminal (or the laptop lid) never loses
work. One surface holds agent panes AND native host shells (host-side git,
`vibe clip`) and works in any terminal on any OS. It scales to several
projects as sessions, not dashboards: each project's `vibe tui` is its own
session on the shared `vibe` socket, so moving between projects is a tmux
session switch. There is no fleet management: the TUI renders agent state
its hooks push out and never drives, schedules, or orchestrates agents
(see [positioning.md](positioning.md)).

```bash
./vibe tui    # session per project on the dedicated "vibe" tmux socket:
              # agent pane (70%) | host shell pane, tabs across the top
```

(The per-pane commands compose manually too — `./vibe agent`,
`./vibe shell`, `./vibe review` each attach to their own container session
from any terminal split. That was the retired `vibe open` workflow; the
commands remain first-class, only the wt.exe layout automation is gone.
`vibe review` in a plain terminal pane outside the TUI remains the
best-rendered image-review surface: yazi talks sixel directly to the
terminal.)

Needs tmux ≥ 3.4 on the host; below 3.7, `vibe show` images don't survive
pane redraws. The pinned 3.7b `--enable-sixel` build (same recipe as the
container's) installs to `~/.local` with:

```bash
bash .vibe/harness/src/scripts/host/install-tmux.sh   # VIBE_TUI_TMUX=... to point elsewhere
```

Keys (prefix is **Ctrl+Space**, with **Ctrl+a** as a full equivalent for
setups where an IME owns Ctrl+Space; double-tap Ctrl+a to send a literal
one through. The inner agent session keeps its own `Ctrl+b`, so in-agent
habits still work. If NO chord works, the socket is likely serving a
stale or conf-less server — check `tmux -L vibe show -g prefix2` (expect
`C-a`) and reset with `tmux -L vibe kill-server` + `vibe tui`. Windows
Terminal swallows `Alt+arrows` for its own pane-focus bindings — unbind
them in WT settings or use `prefix+arrows` / `Alt+1..9` instead):

| Chord | Effect |
| --- | --- |
| `prefix Space` | palette: agent/codex/shell/services windows, git popup, doctor, detach/quit |
| `prefix v` | `vibe clip` and type the container path into the agent pane |
| `prefix g` | host git popup in the repo root (lazygit when installed) |
| `prefix r` | respawn a dead agent pane (it stays visible on exit) |
| `prefix d` | detach — everything keeps running; `vibe tui` reattaches |
| `prefix Q` | quit the UI session (asks first; agents keep running) |
| `prefix R` | reload tmux-ui.conf |
| `Alt+←→↑↓` / `Alt+1..9` | move between panes / windows, no prefix |

Tabs are clickable (mouse is on), and the `+` at the right end of the status
bar opens a new host window. The config lives at `src/config/tmux-ui.conf`
on its own socket — your personal `~/.tmux.conf` and default tmux server are
never touched.

**Leaving the UI:** closing the terminal window is just a detach (same as
`prefix d`) — the layout and every pane keep running, and the next
`./vibe tui` reattaches, respawning the agent pane if it died in the
meantime. `prefix Q` closes the UI session for real; container agent
sessions survive either way — the UI never owns your work. One upgrade
gotcha: a *running* UI server pins the tmux binary it was started with, so
after installing a newer tmux run `tmux -L vibe kill-server` once (`vibe
ui` warns about the skew and prints exactly that).

Windows Terminal bindings worth adding for pane-heavy layouts (all unbound
by default; Settings → "Open JSON file"). Recent WT splits these across two
top-level arrays — actions declare what, keybindings declare the keys:

```json
"actions": [
  { "command": "togglePaneZoom", "id": "User.PaneZoom" },
  { "command": { "action": "moveFocus", "direction": "nextInOrder" }, "id": "User.PaneNext" },
  { "command": { "action": "moveFocus", "direction": "previousInOrder" }, "id": "User.PanePrev" }
],
"keybindings": [
  { "keys": "ctrl+shift+z", "id": "User.PaneZoom" },
  { "keys": "alt+pgdn", "id": "User.PaneNext" },
  { "keys": "alt+pgup", "id": "User.PanePrev" }
]
```

(Merge into the arrays if they already exist. Older WT accepted `"keys"`
inline in the action entry.)

`togglePaneZoom` expands the focused pane to the whole tab and back (the
on-demand "hide the side panes"); the two `moveFocus` actions are
Ctrl-Tab-style cycling but for panes, in creation order. If you live in
panes rather than tabs, rebinding `ctrl+tab`/`ctrl+shift+tab` themselves to
the moveFocus pair also works — direct tab switching stays reachable via
`ctrl+alt+<number>`.

The full working set with those in place (rest are stock WT defaults):

| Keys | Does |
| ---- | ---- |
| `Ctrl+Shift+Z` | Zoom focused pane full-tab / back *(custom, above)* |
| `Alt+PgDn` / `Alt+PgUp` | Cycle panes in creation order *(custom, above)* |
| `Ctrl+Tab` / `Ctrl+Shift+Tab` | Next / previous tab — the `tabs`-layout toggle |
| `Alt+arrows` | Focus pane by direction |
| `Alt+Shift+arrows` | Resize the focused pane |
| `Ctrl+Shift+W` | Close pane (tab when last) — safe: sessions live in tmux |
| `Alt+Shift+D` | New split; run any `./vibe ...` in it |
| `Ctrl+Alt+1..9` | Jump straight to tab N |
| `Ctrl+Shift+P` | Command palette (every action, bound or not) |

Inside an agent pane, tmux still answers `Ctrl+B D` — detach, agent keeps
running (same effect as closing the pane).

Anywhere without `wt.exe` (macOS, WSL without Windows Terminal) it prints the
per-pane commands instead — that fallback is the intended degraded mode, not
an error. Layouts are hardcoded for now; config-driven layouts are on the
backlog (BACKLOG.md).

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

Claude Code sessions feed your reviewer automatically: hooks in the
seeded `.claude/settings.json` (from `src/templates/claude-settings.json`) fire
when you submit a prompt containing an image path and whenever the agent
`Read`s an image file. Delivery targets, in order:

1. **A live `vibe review`** (most recently launched — e.g. in its own
   plain terminal pane): the image path arrives over DDS as a
   toast — "Agent image: … — `g i` jumps to it". Deliberately no
   auto-reveal: nothing moves your cursor or cwd while you're browsing;
   `g i` jumps to the last agent image only when you ask.
2. **The tmux `preview` window** (no reviewer open, agent inside tmux):
   the hook ensures the detached window exists and auto-reveals the image
   there (`ya emit-to`) — fine to jump, nobody browses that surface; its
   name lights up in the status bar until visited (`prefix+i` to look).

When Claude Code converts a pasted path into an
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
- **A sidecar service died** — `vibe up` restarts it (compose up is
  idempotent; `vibe status` shows every project service, stopped ones included).
- **Slow file operations on macOS** — Docker Desktop bind mounts (virtiofs) are
  slower than WSL's native ext4; if a heavy directory (e.g. `node_modules`) hurts,
  move it to a named volume in the project's `compose.yaml`.
- **Root shell for maintenance**: `docker exec -u root -it <container> bash`
  (deliberately outside the normal flow).

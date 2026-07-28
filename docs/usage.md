# Usage

**Authority: as-built.** Where this page and the CLI disagree, the page
is the bug — `vibe help` is the generated truth.

Commands run from anywhere inside a project (the engine walks up to
`.vibe/vibe.yaml`); `vibe init` is the one exception — it refuses to
run inside an existing project. Every command takes `--json` for a
versioned machine-readable result (interactive and streaming commands
emit only an exit-code envelope; `tui` and `logs` emit none) and
`--quiet` to suppress human output. Build/pull progress renders on
stderr as a live bill-of-materials view (each tools-image part flips
pending → building → cached/built; a failing step replays its last
output lines) — `--verbose` streams the raw build output instead, and
non-terminal stderr stays silent. Every command exits with a stable
code: 0 ok,
1 failure, 2 usage, 3 invalid configuration, 4 not registered / not
found, 5 conflict, 6 unavailable or unsupported, 130 interrupted. This
is the one home of that exit-code table.

## Project lifecycle

| Command | Effect |
| --- | --- |
| `vibe init [--preset NAME] [--auto-memory[=BOOL]]` | Seed `.vibe/` from an embedded preset (`minimal`, `go`, `node`, `bun`, `playwright`), register, pin the newest artifact. Interactive runs ask about Claude auto memory unless the flag decides it; scripted/`--json` runs default it off |
| `vibe register [--name NAME]` | Register an existing project |
| `vibe up` | Freeze inputs → compile candidate → reconcile containers → mark approved. Idempotent: warm caches, no agent re-pulls |
| `vibe rebuild` | Same, but recreate containers even when already in sync — and always re-pull the unversioned (channel-tracking) agents to latest; pin one in `image.agents` (`claude@2.1.220`) to hold it |
| `vibe down [--volumes]` | Stop and remove containers and network; also closes the project's tui session; volumes survive unless asked |
| `vibe status` | Containers vs the approved candidate (running / stopped / stale) |
| `vibe config` | Human summary of the plan compiled from current inputs, including the tools image bill of materials (`--json` for the canonical plan JSON) |
| `vibe ps` | All registered projects, plus this project's agent sessions |
| `vibe forget` | Remove the registration; the workspace is untouched |

`up` is idempotent: unchanged inputs produce the identical candidate
digest and touch nothing. A changed candidate replaces containers;
a failed `up` never moves the approved-candidate pointer.

## Working inside the container

| Command | Environment |
| --- | --- |
| `vibe exec [-u USER] [-w DIR] [-e K=V]… -- CMD ARGS…` | explicit `-e` entries only (plus the host `TERM` for TTY execs) |
| `vibe run -- CMD ARGS…` | the env file frozen in the approved candidate, then `-e` |
| `vibe agent [--cold] [-a CMD] [-s NAME] [--stop\|--restart]` | the manifest's agent CLI, with the frozen env file |
| `vibe shell` | first of zsh/bash/sh found in the container, as a login shell |
| `vibe attach [SESSION]` | the main process; with SESSION, that in-container tmux session (e.g. `services`) |
| `vibe logs [SERVICE] [-f] [--tail N]` | container logs — the dev container, or a named sidecar |
| `vibe bootstrap` | verify `bootstrap.required` tools exist in the container |
| `vibe clip [DIR] [--path-only]` | host clipboard image → container `/tmp` (or workspace `DIR`, no daemon needed); prints the container path |

Argv is preserved exactly — there is no shell-string form. `-u USER`
works on all five (exec, run, agent, shell, attach). The container
process's exit code becomes `vibe`'s exit code. Interactive sessions get
a raw TTY with resize forwarding; container commands run from
`/workspace`.

### Lifecycle hooks and the services session

Projects may ship `.vibe/hooks/post-create.sh` (runs once per
container) and `.vibe/hooks/post-start.sh` (runs after every actual
start). The engine executes them inside the container as the container
user, cwd `/workspace`, during `vibe up`/`rebuild` — output streams to
your terminal and a nonzero exit fails the command. No env file is
loaded (secrets enter one process via `vibe run`, never ambiently).
Presets seed inert `.sample` files; rename to activate.

A post-start hook stands up long-running dev processes with the
idempotent payload helper — each becomes a window in the in-container
`services` tmux session:

```sh
bash "$VIBE_PAYLOAD/container/svc.sh" web npm run dev
```

`vibe attach services` joins that session; logs live in each window's
scrollback.

### Agent sessions persist

`vibe agent` runs the CLI inside a tmux session *inside the container*,
so the agent survives its viewer: kill the pane, the TUI, or the whole
terminal and the conversation keeps running — the next `vibe agent`
reattaches to it. Flags: `--cold` starts without repo instruction files
(the tray's `+` chooser also lists a `:cold` launch per CLI);
`-a`/`--agent CMD` runs a different installed agent in its own session;
`-s`/`--session NAME` runs a named parallel instance with its own
identity and state dot. `vibe ps` lists the current project's agent
sessions alongside the registered projects. (Containers whose image
lacks tmux fall back to direct exec: no persistence, no dot.)

Session names are **addresses** (`agent`, `agent-review`, …) — what
`--stop`/`-s`/`-a` target. Displays show **truth**: the tab, pane
border, and sidebar roster are renamed to the CLI actually running
(`claude`, `codex:review`), with the model as a dim roster suffix where
the CLI reports one; `vibe ps` carries both in its detail column.

To end one: `vibe agent --stop` stops the addressed session (combine
with `-s`/`-a`/`--cold` to address a variant; idempotent), and
`--restart` replaces it — the conversation ends and a fresh one starts.
Because sessions persist, a changed `agent.cmd` alone never switches a
*running* session; reattaching to one that runs a different agent than
the manifest asks for now prompts to restart it (or warns, when nothing
can prompt). `vibe down` remains the container-level stop for
everything at once.

## The TUI

`vibe tui` opens (or joins) the project's tmux session with `vibe agent`
in the main window and the engine state in the status line: `●` running,
`◐` running but stale candidate, `○` stopped, plus a pending-request
count. When the containers are not running it first starts the
**approved** candidate — no input freeze, no approval movement: exactly
what you already approved comes back, services and all. Changed inputs
still take a deliberate `vibe up` (the `◐` glyph is the reminder).
Sessions are named from the project ID, so display renames never
strand a session.

The bar sits at the bottom, system-tray style: the `🥡` cell is a
clickable start button (opens the palette, as does the `+` cell), the
`▤` cell beside the tabs toggles the host dock (clicking the collapsed
`host` strip expands it too), window tabs show a state dot and the CLI
actually running, and the right side carries the engine state (click
it for the request list) and a clock. Holding the
prefix swaps the tabs for a keybind cheatsheet — hints on demand, no
extra row. Project identity lives in the sidebar and the OS window
title, never the bar.

The mouse speaks two verbs beyond plain clicks (2026-07-28).
**Ctrl+click any file path** — in a transcript, a shell, anywhere —
and it opens: text lands in the review stack's nvim popup (a
`path:line` jumps to the line), images open the **preview window**, a
reusable tab rendering real pixels via sixel (chafa in the container;
full fidelity needs host tmux ≥ 3.7 — `vibe doctor` grades it, and
lesser hosts get a loud low-fi fallback). Paths resolve inside the
container first, so `vibe clip`'s `/tmp` drops preview like anything
else; prose and dead ends are silent no-ops. **Right-click an agent**
— its window tab, its tray ghost cell, or its sidebar row — for
per-session surgery: stop the SESSION (not just the viewer; the
viewer buries itself), open a viewer, close just the viewer, and on a
dead ✗ row, dismiss the record once you've seen it. Non-agent tabs
and panes keep a stock-style tmux menu.

The daily cycle is symmetric: evening `prefix+Space → park project`
(or `vibe down` from any terminal) stops the containers and closes the
project's UI session — agent logins and conversations live on the
agent-state volume and survive. Morning is `vibe tui`, alone. Leaving
everything running overnight also works (reattach is instant); it just
keeps the container — and on WSL2 the VM — warm. Agent state (working / attention / idle / exited) is
pushed by the agent's own hooks into tab, border, and sidebar dots — a
permission prompt flashes the tab even from another window.

The prefix is `C-Space` (`C-a` also works). The keys that matter:

| Key | Action |
| --- | --- |
| `prefix+Space` | palette: agent/shell windows, files, git, requests, ps, doctor |
| `prefix+f` | files popup: nvim + oil in the container — browse, read, light edits |
| `prefix+g` | git popup: lazygit in the container — diffs, staging, commits |
| `prefix+b` | toggle the project sidebar |
| `prefix+t` | toggle the bottom host dock |
| `prefix+v` | host clipboard image → agent prompt |
| `prefix+o` | switch project (live sessions tree) |
| `prefix+r` | respawn a dead pane (reattaches the agent session); `prefix+x` closes it |
| `prefix+Q` / `prefix+K` | quit this project's UI / kill every project's UI (both confirm; agents and containers keep running) |

`prefix+f` and `prefix+g` are the **review stack**: nvim and lazygit
baked into the tools image at pinned versions with an opinionated,
ascii-safe config — reviewing an agent's work needs nothing installed
on the host. Both popups say how to leave in their border; inside
nvim, `q` quits from anywhere, `-` closes an open file back to the
listing, and `Space` shows the keymap. To use your OWN editor instead,
rebind the key in the user conf — e.g.
`bind f display-popup -d "#{session_path}" -w 90% -h 90% -E "nvim ."`
runs your host nvim with your config against the same checkout.

Layout knobs (tmux user options on the vibe socket; see
[tui-layout.md](tui-layout.md)): `@vibe_sidebar_on` (default 1),
`@vibe_sidebar_w` (30 cols), `@vibe_dock_size` (30%),
`@vibe_engine_refresh` (30s). Deeper customization — bar position,
accents, extra binds — goes in `~/.config/vibe/tui.conf`, sourced last
by the materialized conf so it always wins and survives updates.

## Rebuild requests (the broker)

Agents cannot change the container they run in. Instead they write
`.vibe/requests/<id>.json`:

```json
{"format": 1, "id": "add-port", "kind": "rebuild",
 "reason": "rojo needs 34872", "summary": "add 127.0.0.1:34872:34872"}
```

On the host:

```sh
vibe request list             # poll; each new request is bound to an
                              # immutable candidate built from current inputs
vibe request show add-port    # sanitized reason/summary + candidate digest
                              # + a bounded plan diff vs the approved candidate
vibe request approve sha256:… [--yes]
vibe request reject  sha256:… [-m "why"]
```

Approval addresses the candidate digest — what was frozen at poll time —
never a filename an agent could rewrite afterwards. Decisions land in a
read-only results mount at `/vibe/results` inside the container. Request
text is untrusted: it renders through the control-character-escaping
encoder everywhere. The plan diff is the trusted half of the decision:
computed from the immutable candidates themselves, it shows what will
actually change beside whatever the agent *claims* will change (both
`show` and the approve confirmation include it).

## Releases and health

| Command | Effect |
| --- | --- |
| `vibe provision` | Install this binary + embedded payload as an artifact; pin the project |
| `vibe update --version vX.Y.Z` | Download, verify, install a release; swap the host binary |
| `vibe doctor` | Layout, registration, artifact integrity, daemon, containers, lifecycle marker, tmux |
| `vibe gc [--dry-run] [--min-age DUR]` | Remove unreferenced artifacts/candidates/snapshots, stale staging, binaries, and forgotten-project state |
| `vibe version` | Engine version |

`gc` is the only thing that ever deletes store state, and it refuses
anything pinned by a project, approved, bound to a pending request, held
by a live lease, or younger than `--min-age` (default one hour).

## Dev mode (hacking on the engine itself)

Inside an engine checkout:

```sh
vibe dev on       # snapshot allowlisted sources, build in a pinned
                  # golang container, install as a dev artifact, pin
                  # THIS project to it
vibe dev status   # provenance: source, builder, and output digests
vibe dev sync     # rebuild after edits
vibe dev off      # back to the newest release artifact
```

The artifact pin is per-project, but `dev on`/`sync` also repoint the
host-global `vibe` shim at the fresh dev binary (`dev off` hands it
back to the newest release). Entering dev mode asks a one-time
source-trust confirmation — the build output runs on the host; syncing
an already-dev project rebuilds without re-asking. Release-mode
projects never see dev artifacts.

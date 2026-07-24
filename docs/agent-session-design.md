# Agent session layer — persistence + the state channel

Design for restoring the two v1 properties the cutover dropped:

1. **Persistence** — an agent survives its viewer. In v1 the host pane
   was only a docker-exec window onto a tmux session *inside* the
   container; killing the pane, the tui server, or the terminal never
   killed the agent. In v2 today, `vibe agent` execs the agent CLI
   directly: the pane dying takes the conversation with it.
2. **State at a glance** — working / attention / idle / exited dots on
   tabs, pane borders, and the sidebar roster, fed by the agent itself
   with no polling.

Both are one design because they share a carrier: the container-side
tmux session. Placement follows the language split (AGENTS.md): the Go
engine is the trusted custodian — image content, exec argv, identity
env — and every tmux/UI mechanism is payload shell, container- or
host-side.

Non-goals here: `--jailed`, egress visibility, event-driven sidebar
refresh, review-as-split (their own BACKLOG entries).

## Architecture

```
host tmux (vibe-engine socket)                     container
┌────────────────────────────────┐    docker exec ┌──────────────────────────────┐
│ agent pane = viewer            │────────────────│ agent-session.sh             │
│   pane-title-changed hook      │      TTY       │  └─ inner tmux session       │
│   └─ state-render.sh           │◄── OSC title ──│      └─ claude (via env-run) │
│       └─ @vibe_glyph/@vibe_attn│                │  agent-state-hook.sh         │
│          (tabs/borders/sidebar)│                │   ▲ Claude Code hooks        │
└────────────────────────────────┘                └──────────────────────────────┘
```

The bridge is v1's spike-validated no-polling channel: the inner tmux
re-emits its `set-titles-string` as an OSC title through the docker-exec
TTY; the host server sees its pane title change and its
`pane-title-changed` hook renders the dot.

## Components

### Engine (Go — trusted core)

- **tmux in the tools image.** The install recipe set
  (internal/builder/install.go) gains a root apt layer installing
  `tmux` whenever any agent is selected. No version pin beyond the
  distro's (decision record: version-lock machinery deferred).
- **Exec wrapping.** `App.Agent` (and only it — `exec`/`run`/`shell`
  stay raw): when the candidate has a payload mounted and the container
  has tmux, the argv becomes
  `bash /vibe/payload/container/agent-session.sh agent [FLAGS…] -- CMD [ARGS…]`;
  otherwise fall back to today's direct exec. The engine never builds
  tmux command strings — it passes real argv; the one quoting layer
  lives at the bottom of the script (v1 rule).
- **Identity env.** The engine already freezes the env file; it adds
  `VIBE_PROJECT` (id) and `VIBE_PROJECT_NAME` (display name) to the
  agent exec so container-side scripts never parse workspace files for
  identity (v1's `$VIBE_PROJECT_NAME` rule, post-security-review).
- **Flag surface.** `vibe agent` grows `--cold`, `-a/--agent CMD`, and
  `-s/--session NAME` pass-throughs (v1 semantics: default session
  reattaches with `-A`; a named session is a parallel instance with its
  own identity, dot, and roster row).

### Container payload (shell — `payload/container/`)

- **`agent-session.sh`** (port of v1 `agent-entry.sh`, minus the
  config.env/lib machinery v2 replaced): modes
  - `agent` — ensure-or-attach the inner tmux session
    (`agent[-NAME]`), minting the instance identity `<pid>.<epoch>`,
    exporting `VIBE_AGENT_SESSION` / `VIBE_AGENT_INSTANCE`, launching
    the agent CLI inside it. Claude runs with
    `--settings /vibe/payload/container/claude-settings.json` — the
    hook/statusline wiring rides the read-only payload mount instead of
    v1's settings-merge into the user's config.
  - `attach` — door into the services session (parity, later slice).
  - `reap` — v1 `reap-nested`: detach `VIBE_NESTED=1` ghost clients on
    tui kill; agents keep running.
- **`tmux-agent.conf`** — the minimal inner conf: `status off`,
  `set-titles on`, `allow-passthrough on`, low `escape-time`, default
  prefix (C-b, no collision with the host's C-Space).
- **`agent-state-hook.sh`** (near-verbatim port): event name as argv —
  `SessionStart`→idle, `UserPromptSubmit`/`PostToolUse`→working,
  `Notification`→attention, `Stop`→idle, `SessionEnd`/`__exit`→exited.
  Two best-effort outputs: a state record in
  `${XDG_RUNTIME_DIR:-/tmp}/vibe-agent-state-<uid>/` (tmpfs only —
  never the workspace, never the agent-state volume) for `vibe ps`, and
  the title channel `vibe1|<project>|<session>|<instance>|<state>`.
  Straggler guard by instance mint; stdout stays empty; always exit 0 —
  state is cosmetic, the agent is not.
- **`claude-settings.json`** — hook registrations (each event with its
  name as argv, so the hot path never spawns jq) and, in a later slice,
  the statusline command.

### Host payload (shell — `payload/host/scripts/`)

- **`state-render.sh`** (port): decodes the title channel into
  `@vibe_state`/`@vibe_glyph`/`@vibe_dot_fg`/`@vibe_attn` on pane and
  window (theme.sh owns the glyph/color map), bumps
  `@vibe_state_serial` so the sidebar redraws. The tab formats, border
  format, and sidebar roster already consume these options — they light
  up with zero conf changes.
- **Conf hooks** (re-added from v1): `pane-title-changed` →
  state-render; `pane-died` → state-render `frontend-dead` (the viewer
  died; the run may live — one corpse semantics for tabs, borders,
  roster).

## Security

- **The title is container-controlled and untrusted.** Hooks
  interpolate ONLY `#{pane_id}` (server-controlled) and the stamped
  `@vibe_payload_dir` (same trust domain); the title itself is fetched
  out-of-band by state-render.sh and never expanded into shell words
  (v1 spike-validated rule).
- **Closed vocabulary.** Unknown states are dropped, not rendered.
- **Pane-bound, not title-bound.** The dot lands on the pane the title
  arrived on; the encoded `<project>` field is display metadata only. A
  compromised container can lie about its own dot — it cannot paint
  another project's.
- **Records in runtime tmpfs only** — no workspace writes, nothing in
  the agent-state volume for a future host reader to trust.

## Failure modes

- No tmux in the image (base-image project, older tools image): direct
  exec, exactly today's behavior; no dot, no persistence.
- Hook/render failures: silent no-ops (`|| true` throughout) — the
  agent never notices.
- Host pane killed: inner session lives; the pane shows frontend-dead
  (`◌`); the next `vibe agent` / tui respawn reattaches (`-A`) to the
  same conversation.
- Container recreated (`vibe rebuild`): inner server dies with it —
  that boundary is unchanged; Claude's own state survives via
  CLAUDE_CONFIG_DIR in the agent-state volume.

## Slices

1. **Persistence.** tmux recipe layer; `agent-session.sh` (agent mode
   only) + inner conf; engine wrapping + identity env; reattach
   semantics; `prefix+r` respawn reattaches. Exit: kill the pane, rerun
   `vibe agent`, same conversation.
2. **State channel.** claude-settings hooks + `agent-state-hook.sh`;
   `state-render.sh` + conf hooks; dots/attention flash/roster live.
   Exit: permission prompt flashes the tab coral from another window.
3. **Roster + polish.** `vibe ps` reads the in-container records
   (docker exec, out-of-band); sidebar roster rows for sessions without
   windows; `attach`/`reap` modes; statusline glue.

Verification is dogfood (this repo runs inside its own engine): the
real-daemon CI story for exec paths stays in BACKLOG with the rest.

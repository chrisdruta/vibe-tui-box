# Configuration — `.vibe/vibe.yaml`

**Authority: as-built.** This page describes what the schema and engine
actually do; where it disagrees with them, the page is the bug. The
promises behind the policy live in [security.md](security.md).

One closed, versioned document is the entire project configuration.
Unknown keys, unknown enum values, YAML anchors/aliases, custom tags,
and duplicate keys are errors — a typo fails loudly instead of being
ignored. `vibe config` prints the compiled result; changes take effect
on the next `vibe up` / `vibe rebuild`.

```yaml
schema: 1
image:
  base: "mcr.microsoft.com/devcontainers/base:debian"
  agents: [claude, codex]     # claude | codex | grok; claude@2.1.220 pins, claude@stable tracks that channel, bare = latest per rebuild
  toolchains: [go]            # node | bun | go | rokit
  extension: true             # opt into .vibe/Dockerfile (see extending.md)
runtime:
  ports: ["127.0.0.1:34872:34872"]
  imports:
    - {source: models, target: /models, readonly: true}
  env: {MY_FLAG: "1"}
services:
  db:
    image: "postgres:16"
    ports: ["127.0.0.1:5432:5432"]
    env: {POSTGRES_PASSWORD: "x"}
    volumes:
      - {name: data, target: /var/lib/postgresql/data}
agent:
  cmd: claude                 # must be listed in image.agents
  tmux: true
  memory: off                 # auto | off — the agent CLI's cross-session auto memory
env_file: .env
bootstrap:
  required: [git, go]
```

## Field notes

- **`schema`** — must be exactly `1`. There is no engine-version field:
  which engine runs a project is the host registry's per-project
  artifact pin, not a manifest concern. (A `harness:` field existed
  pre-release — required, shape-checked, consumed by nothing — and was
  removed 2026-07-28; a manifest still carrying it fails the closed
  schema's unknown-key check, and the fix is deleting the line.)
- **`image.base`** — any image reference; the engine resolves it to a
  registry digest at candidate time and runs by digest from then on.
  Pin with `@sha256:…` yourself for full reproducibility.
- **`image.agents` / `image.toolchains`** — closed enums the engine
  bakes into a generated install image layered on the base; recipes
  ship with the engine. Unlike the extension there is no approval
  prompt: the install Dockerfile is engine-authored, never project
  input. `agent.cmd` must be listed in `image.agents`.

  An agent entry optionally carries a qualifier. `claude@2.1.220`
  (digit-leading = exact version) installs that build and never moves.
  Plain `claude` tracks the installer's **latest** channel and is
  **re-pulled on every `vibe rebuild`** — no version given means "keep
  me current" (claude's `stable` channel lags latest by design, so
  latest is the default). A word qualifier (`claude@stable`, an npm
  dist-tag for codex) selects that channel and refreshes per rebuild
  like an unversioned entry — channels are moving targets, never
  frozen into a cached layer. The refresh weaves a per-rebuild
  cache-buster into only the channel-tracking agent layers; pinned agents
  and the system toolchains (Go/Node, and tmux — an engine-pinned
  source build, because distro tmux drops sixel images on redraw)
  stay cached and move only with the manifest or engine releases.
  `vibe up` never refreshes (idempotent ups stay off the network):
  rebuild is the one refresh boundary, so there is exactly one way to
  move an agent and one way to hold it. `grok` cannot be pinned — its
  installer has no version parameter.

  The image is the only version authority: claude's in-container
  self-updater is disabled (engine env `DISABLE_AUTOUPDATER=1` plus the
  payload settings), because an update would land in the container's
  writable layer and silently revert on the next replace. Agent
  versions move exactly one way — a rebuild (unversioned) or a manifest
  pin change.
- **`runtime.ports`** — published ports must bind a loopback IP; there
  is no way to expose a container to the network. The sanctioned use is
  host tooling that must reach a server inside (e.g. Roblox Studio →
  Rojo).
- **`runtime.imports`** — bounded *data* inputs, not live code. Each
  source is copied into the immutable input snapshot and that copy is
  mounted; editing the source on the host does nothing until the next
  candidate. The workspace itself is the only live bind.
- **`runtime.egress`** — the per-project DNS ledger, on when absent.
  A provisioned project's plan gains an engine-generated CoreDNS
  sidecar (`vibe-<id>-svc-dns`, digest-pinned in engine code) that the
  dev container's resolver rides — capability-probed since 2026-08-04:
  the sidecar compiles in only when the pinned artifact's payload
  actually carries the Corefile, so a pre-egress artifact runs without
  the ledger instead of failing `up`. Its query log IS the project's
  domain ledger — read raw with `vibe logs dns [-f]`, or joined with a
  live-socket sample via the "network egress" popup (prefix+E, the
  palette, or one left-click on the sidebar's `dns ledger` row). Resolution behavior is unchanged (the sidecar forwards to
  the same resolvers Docker uses today); in-network alias lookups
  (`db`, `dns`) are answered by Docker's embedded DNS and never appear
  in the ledger, and direct-to-IP or DoH traffic bypasses it
  ([security.md](security.md) "Egress"). `egress: off` removes the
  sidecar and the resolver pointing. `dns` joins the reserved service
  names.
- **`runtime.env` / `services.*.env`** — planned configuration:
  container-ambient and part of the plan digest (maps are sorted before
  hashing, so ordering can't change it), never a host process
  environment. Secrets belong in `env_file`, which is neither.
- **`services`** — sidecars get the same closed policy as the dev
  container (they do run as their image's own user, not `vscode`) and
  join the project network under their short name (the dev container
  reaches `db` as `db`). Volumes are engine-named from the project ID;
  there is no way to reference another project's volume.
- **`agent.tmux`** — selects the persistent in-container session
  carrier. With it false (or a tmux-less image), `vibe agent` still
  runs the CLI directly, but the session-shaped variants (`--cold`,
  `-s NAME`, `--stop`, `--restart`) are unavailable. Read live from the
  manifest like `agent.memory` — no rebuild to flip.
- **`agent.memory`** — opt-in to the agent CLI's cross-session auto
  memory (Claude Code's `autoMemoryEnabled`; other agents ignore it for
  now). Off when absent: the payload settings pin memory off, and
  `auto` flips the key in a derived settings copy at session start.
  Read live from the manifest like `agent.tmux` — flipping it needs no
  rebuild, only a new `vibe agent` session. With memory on, the
  memory directory lives under the agent-state volume, so it survives
  rebuilds until `vibe down --volumes`.
- **`env_file`** — workspace-relative, parsed literally (no shell
  syntax, no interpolation), frozen into the snapshot. Exec-scoped by
  design: `vibe run` and `vibe agent` inject it; `vibe exec`, shells,
  hooks, and service windows never see it ambiently
  ([security.md](security.md) "Environment values").
- **`bootstrap.required`** — probed by `vibe bootstrap` and reported;
  names only, no shell.

## What is deliberately absent

Privileged mode, added capabilities, devices, host namespaces, Docker
sockets, non-loopback ports, arbitrary bind mounts, external volume
names, raw compose keys, command/entrypoint overrides, and any
string-through-a-shell field. If the schema can't say it, the engine
won't do it — that is the point. The one escape hatch is the image
extension (`extension: true` + `.vibe/Dockerfile`), which is
digest-approved per change (see [extending.md](extending.md)).

## The rest of `.vibe/`

Beyond the manifest, the engine knows these project files:

| Path | Role |
| --- | --- |
| `.vibe/Dockerfile` + `.vibe/build/` | image extension inputs (digest-approved; [extending.md](extending.md)) |
| `.vibe/requests/` | agent rebuild requests ([usage.md](usage.md)) |
| `.vibe/hooks/post-create.sh` | runs in-container once per container |
| `.vibe/hooks/post-start.sh` | runs in-container after every actual start |
| `.vibe/AGENTS.md` | seeded instructions teaching agents this environment |

Hooks are workload code: they execute inside the container only, as the
container user, with no env file loaded. The host never reads or runs
them.

## Mount layout inside the container

| Target | Content | Mode |
| --- | --- | --- |
| `/workspace` | the project root (the only live host bind) | rw |
| `/vibe/payload` | the pinned artifact's container payload | ro |
| `/vibe/agent-state` | per-project volume for agent logins/state | rw |
| `/vibe/results` | broker decision records | ro |
| custom | `runtime.imports` snapshot copies | per entry |

Custom import targets may not equal, contain, or be contained by any of
the engine-owned targets.

Claude's harness integration ships as a **vibe plugin** loaded per
session from the read-only payload (`--plugin-dir` — never installed,
so nothing lands on the agent-state volume): the agent-state hooks, the
subagent statusline, and a `/vibe:request` command that authors a
well-formed rebuild request. The keys a plugin cannot express
(`statusLine`, `autoMemoryEnabled`, `autoUpdates`, `sandbox`) ride a
thin `--settings` file beside it.

Claude settings otherwise layer exactly like plain Claude Code — the
engine adds no config system of its own. The per-session `--settings`
file pins only those four keys and outranks every file scope;
everything else follows the CLI's normal precedence, which leaves two
layers that are yours: the repo's `.claude/settings.json` (project
scope, travels with the checkout) and user scope, which lives at
`/vibe/agent-state/claude/settings.json` because `CLAUDE_CONFIG_DIR`
points at the volume — edit it in-container and it persists across
rebuilds exactly like a login. Don't fight the four pinned keys from a
file (the flag wins); `agent.memory` is the manifest knob for the one
pinned key that is meant to move.

Agent logins relocate onto the agent-state volume per agent: claude via
`CLAUDE_CONFIG_DIR=/vibe/agent-state/claude`, codex via
`CODEX_HOME=/vibe/agent-state/codex` — log in once inside the container
(`claude`; for codex use `codex login --device-auth` headless, or pipe
a key: `printenv OPENAI_API_KEY | codex login --with-api-key`) and the
login survives rebuilds until `vibe down --volumes`. When claude and
codex are installed together, post-create also best-effort installs
the codex second-opinion plugin into Claude at user scope
(`/codex:review` and friends), retrying on a later `up` if the first
attempt had no network. The agent state dots and statusline are
Claude-wired today; codex sessions run fine but report at most
`running` in `vibe ps`.

Codex's own sandbox cannot start inside the hardened container
([security.md](security.md) "Inner agent sandboxes"), so post-create
seeds `sandbox_mode = "danger-full-access"` into
`$CODEX_HOME/config.toml` (key-absent only — your own setting wins)
and rewrites the plugin's pinned per-thread sandbox modes, which
config cannot reach, to the same. Two caveats: a `claude plugin
update` reverts that rewrite until the next `vibe up` re-applies it,
and patched review threads gain workspace write access — the
container plus git history remain the boundary and the undo.

When the image ships `gopls` beside claude (no stock preset installs
it — this arms when a project's extension or hook adds it),
post-create likewise installs and enables Claude Code's official
`gopls-lsp` plugin at user scope, so Go code intelligence works in a
fresh container without the recommendation popup. Same contract as the
codex plugin: best-effort, marker-guarded on the volume, retried on a
later `up`.

## GitHub access

The container can push. `gh` rides every agent image, and on every
start the engine wires git to it: gh becomes git's credential helper
for github.com, and both GitHub SSH remote forms (`git@github.com:`,
`ssh://git@github.com/`) rewrite to HTTPS — container-side only, in
the container-local `~/.gitconfig` (the container has no SSH keys, so
an SSH-cloned repo shared with the host would otherwise be push-dead
in here; host git is untouched). Until you log in, a push asks for
`gh auth login` and fails — that gate is the design: credentials
enter only when you paste them.

Preferred: a **per-project fine-grained PAT** — single-repository
access, an expiry, and only the permissions below — pasted into
`gh auth login` inside the container (choose HTTPS; SSH never uses
the PAT). `GH_CONFIG_DIR` points into the agent-state volume, so the
login persists across rebuilds and stays compartmentalized per
project, exactly like the agent logins. There is deliberately no
`GH_TOKEN` passthrough from the host: container env is baked at
create time and visible to every process, while a pasted login stays
on the volume behind gh's own storage.

### Fine-grained PAT quick reference

Create at GitHub → Settings → Developer settings → Fine-grained tokens
(<https://github.com/settings/personal-access-tokens/new>). Repository
access: **Only select repositories** → the one project repo. Set an
expiration. Repository permissions:

| Permission      | Access         | Enables                                            |
| --------------- | -------------- | -------------------------------------------------- |
| Contents        | Read and write | clone, pull, push, branches, merges, releases      |
| Pull requests   | Read and write | `gh pr create/view/comment/merge`                  |
| Actions         | Read-only      | `gh run list/view/watch` — following CI runs       |
| Commit statuses | Read-only      | `gh pr checks`, commit status on PRs               |
| Workflows       | Read and write | pushes that touch `.github/workflows/` (see below) |
| Metadata        | Read-only      | added automatically (required)                     |

**Workflows is the conscious trade in this set.** Without it, GitHub
rejects any push containing changes under `.github/workflows/` —
annoying in repos where CI files are part of normal development. With
it, whatever runs in the container can modify CI, which is a
privilege-escalation path (a malicious change to a workflow file
executes with the repository's Actions credentials). Grant it for
interactive work on repos whose CI you edit; leave it off for
low-trust projects, where a rejected workflow-file push is the
guardrail working.

# Configuration — `.vibe/vibe.yaml`

One closed, versioned document is the entire project configuration.
Unknown keys, unknown enum values, YAML anchors/aliases, custom tags,
and duplicate keys are errors — a typo fails loudly instead of being
ignored. `vibe config` prints the compiled result; changes take effect
on the next `vibe up` / `vibe rebuild`.

```yaml
schema: 1
harness: v2.0.0
image:
  base: "mcr.microsoft.com/devcontainers/base:debian"
  agents: [claude]            # claude | codex | grok
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
  auto: {install: true, git_hooks: false, git_lfs: false}
```

## Field notes

- **`image.base`** — any image reference; the engine resolves it to a
  registry digest at candidate time and runs by digest from then on.
  Pin with `@sha256:…` yourself for full reproducibility.
- **`image.agents` / `image.toolchains`** — closed enums the engine
  bakes into a generated install image layered on the base; recipes and
  version pins ship with the engine and move with engine releases.
  Unlike the extension there is no approval prompt: the install
  Dockerfile is engine-authored, never project input. `agent.cmd` must
  be listed in `image.agents`.
- **`runtime.ports`** — published ports must bind a loopback IP; there
  is no way to expose a container to the network. The sanctioned use is
  host tooling that must reach a server inside (e.g. Roblox Studio →
  Rojo).
- **`runtime.imports`** — bounded *data* inputs, not live code. Each
  source is copied into the immutable input snapshot and that copy is
  mounted; editing the source on the host does nothing until the next
  candidate. The workspace itself is the only live bind.
- **`runtime.env` / `services.*.env`** — values are container data,
  assigned through the Docker API; they never enter a host process
  environment. Maps are sorted before hashing, so ordering can't change
  the candidate digest.
- **`services`** — sidecars get the same closed policy as the dev
  container and join the project network under their short name (the
  dev container reaches `db` as `db`). Volumes are engine-named from
  the project ID; there is no way to reference another project's
  volume.
- **`agent.memory`** — opt-in to the agent CLI's cross-session auto
  memory (Claude Code's `autoMemoryEnabled`; other agents ignore it for
  now). Off when absent: the payload settings pin memory off, and
  `auto` flips the key in a derived settings copy at session start.
  Read live from the manifest like `agent.tmux` — flipping it needs no
  rebuild, only a new `vibe agent` session. With memory on, the
  memory directory lives under the agent-state volume, so it survives
  rebuilds until `vibe down --volumes`.
- **`env_file`** — workspace-relative, parsed literally (no shell
  syntax, no interpolation), frozen into the snapshot. `vibe run` and
  `vibe agent` load it; `vibe exec` never does.
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

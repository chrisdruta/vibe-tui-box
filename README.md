# vibe-tui-box 🥡

*Vibe coding in a box: a riced tmux TUI on a secure, isolated devcontainer chassis.*

[![CI](https://github.com/chrisdruta/vibe-tui-box/actions/workflows/ci.yml/badge.svg)](https://github.com/chrisdruta/vibe-tui-box/actions/workflows/ci.yml)
[![MIT-0 license](https://img.shields.io/badge/license-MIT--0-3d59a1)](LICENSE)

<!-- TODO(brand): swap for assets/tui.gif once recorded; assets/brand/NOTES.md has the capture recipe -->
<img src="assets/mark.svg" align="right" width="220" alt="vibe-tui-box mark: a line-art takeout box whose side panel is the tmux cockpit">

`vibe` is a single compiled binary that runs coding agents (Claude Code by
default; Codex and Grok opt-in) inside a locked-down container per project,
on Windows + WSL2 or macOS. The host needs git, docker, and tmux. The
binary embeds everything the container mounts, so installing it installs
the whole harness.

- **Closed by construction.** One closed `vibe.yaml` per project: no raw
  Docker, Compose, or shell passthrough anywhere in the schema. Containers
  drop all capabilities, set `no-new-privileges`, publish ports on
  loopback only, and never see the Docker socket or your host home.
- **Deterministic by digest.** Every `up` freezes inputs into a
  content-addressed snapshot and compiles a canonical plan. Identical
  inputs produce the identical candidate, and what you approved is exactly
  what runs.
- **A cockpit, not a wrapper.** A host tmux TUI keeps every project's
  agents in view with live state dots pushed by the agents' own hooks, and
  review happens in-container. Nothing to install on the host.

## Getting started

You need a reachable Docker daemon (on Windows: Docker Desktop with WSL2
integration enabled for your distro), git, and tmux; building from source
also needs Go 1.26.4 or newer (`go.mod` is the floor). tmux 3.7 or newer
is recommended; older versions work, but image previews degrade to
low-fi. `vibe doctor` verifies the docker and tmux halves of this.

No release is tagged yet, so today the binary builds from source. This
step flips to a release download at v1.0; everything after it stays the
same.

```sh
git clone https://github.com/chrisdruta/vibe-tui-box.git && cd vibe-tui-box
go build -o bin/vibe ./cmd/vibe
bin/vibe provision                # publish the embedded harness under ~/.vibe
mkdir -p ~/.local/bin && cp bin/vibe ~/.local/bin/   # provision creates no PATH entry
```

If `vibe` is not found after that, open a new shell: a freshly created
`~/.local/bin` joins PATH at login on most distros.

Then, in a project of yours (not this repo: a directory that already has
a `.vibe/vibe.yaml`, like this checkout, takes `vibe register` instead of
`vibe init`):

```sh
vibe init          # seed .vibe/vibe.yaml from a preset and register the project
vibe up            # freeze inputs, compile a candidate, start containers
vibe doctor        # host / project / artifact / container health
vibe tui           # tmux session with the agent running inside
```

Everything a project configures lives under one directory. `vibe init`
seeds the first three entries; the rest appear as you opt in:

```text
my-project/
└── .vibe/
    ├── vibe.yaml       # the entire project configuration (closed schema)
    ├── AGENTS.md       # seeded instructions teaching agents this environment
    ├── hooks/          # post-create / post-start samples; rename to activate
    ├── Dockerfile      # optional image extension (digest-approved), yours to add
    └── requests/       # agents drop rebuild requests here at runtime
```

## Day to day

```sh
vibe status                 # container state vs the approved candidate
vibe exec -- go test ./...  # run argv in the container (explicit env only)
vibe run  -- make dev       # same, plus the project's frozen env file
vibe shell                  # interactive login shell
vibe logs dns -f            # follow a container or sidecar log
vibe rebuild                # recreate containers from fresh inputs
vibe down                   # stop and remove; agent state survives
                            #   (--volumes wipes it: logins, memory, gh auth)
vibe gc --dry-run           # prune unreferenced store objects
vibe update --version vX.Y.Z   # download, verify, and install a release
```

`vibe agent` takes `-s NAME` for parallel sessions and `--cold` to start
without repo instruction files; `vibe forget` unregisters a project and
leaves the workspace untouched. The full command surface is in
[docs/usage.md](docs/usage.md).

When an agent inside the container wants a config change applied, it writes
a request file; you decide on the host:

```sh
vibe request list           # poll + bind each request to an immutable candidate
vibe request show add-port
vibe request approve add-port   # applies exactly the frozen candidate you saw
```

## The cockpit

`vibe tui` opens a host tmux session per project, and one tab runs the
whole fleet: a sidebar that keeps every project's agents, workspace
services, and engine sidecars in view (a parked project's row is one
click from live: containers up, session open, you switched in), live
state dots pushed by the agents' own hooks (nothing polls), a full-width
host dock (`prefix+t`, VS Code ctrl+` feel), and a chooser for launching
whatever the image installed. Agents run in tmux *inside* the container,
so a closed terminal never kills a session. Reviewing their work needs
nothing on the host: `prefix+f` (nvim + oil) and `prefix+g` (lazygit)
open popups running in the container, pinned into the image at exact
versions. Images preview over sixel, ctrl+click opens a path in nvim at
its line, and `prefix+v` carries a host clipboard image through the
boundary into the agent's prompt.

## How it holds together

Projects author one closed `vibe.yaml`: base image, agents, toolchains,
loopback-only ports, bounded data imports, sidecar services, env file.
Unknown keys and enum values are errors. Every `up` freezes the inputs
into a content-addressed snapshot, compiles a canonical plan, and
reconciles containers against it by digest. The host never executes a
byte a container could have written: workspace files are read as data
through bounded strict parsers, frozen before use, and never re-read;
agent-authored text reaches your terminal only through an encoder.
[docs/security.md](docs/security.md) is the authoritative statement of
the trust model.

## Documentation

| doc | covers |
| --- | --- |
| [architecture](docs/architecture.md) | the system as built, with diagrams |
| [engine internals](docs/engine-internals.md) | contributor deep dive, package by package |
| [installation](docs/installation.md) | releases, `vibe provision`, updates |
| [usage](docs/usage.md) | the full command surface |
| [configuration](docs/configuration.md) | the `vibe.yaml` schema |
| [extending](docs/extending.md) | project image extensions |
| [security](docs/security.md) | the trust model in practice |
| [tui layout](docs/tui-layout.md) | the cockpit's design record |

## What this deliberately is not

Agent tooling has three layers: the agent CLIs themselves (Claude Code,
Codex), the orchestrators that fan them out across worktrees, and the
environment the agent process actually executes in. `vibe` is the third
layer only: a hardened container the CLIs run inside, plus the terminal
affordances that make an agent workable there. Anything that *drives* an
agent is ceded to the layers above: no orchestration UI, scheduler, or
fleet manager; no first-party agent loop or model API client; no centralized
credential store; no bind-mounting host credentials (`~/.claude`, SSH
keys, keychains) into containers. Logins are per-project by design, not
omission: each project's agent-state volume is a blast-radius cell, and
"log in once, use everywhere" is exactly the cross-project token
exposure the per-project boundary exists to prevent.

## Status

The Go engine replaced the original bash/compose harness in this tree
(that line lives in git history up to tag `v0.7.3`). There is no
migration: old installs reinstall, projects `vibe init` fresh. No release
is tagged yet; [ROADMAP.md](ROADMAP.md) lays out the path to v1.0,
[CHANGELOG.md](CHANGELOG.md) has the cutover details, and
[BACKLOG.md](BACKLOG.md) holds the unscheduled ideas.

---

<p align="center">
  <img src="assets/logo.svg" alt="vibe-tui-box line-art mark" width="200">
</p>

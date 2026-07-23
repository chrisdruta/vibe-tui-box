# Architecture v2 — clean-slate Go engine

Status: revised draft 2026-07-23 · supersedes
[go-port-plan.md](go-port-plan.md) · revised against the
[adversarial review](architecture-v2-review.md) · owner: chris

Implementation package and API design:
[go-engine-design.md](go-engine-design.md).

## Premise and boundary

v2 is a rewrite, not a port. There is no migration surface: existing installs
are removed and reinstalled, and existing projects are initialized again.
Nothing recognizes the submodule layout, `.devcontainer`, k=v records, or the
bash host layer.

The product remains a tmux TUI over hardened per-project containers. The
container reduces accidental host damage; it does not make untrusted project
code safe. v2's host boundary is:

> The host executes only attested, digest-identified engine artifacts from
> host-owned immutable state. Workspace bytes are never executed, sourced, or
> evaluated by a host process. They reach Docker only as either:
>
> 1. values in a canonical, capability-bounded typed model produced by the
>    trusted engine; or
> 2. a separately identified, immutable input whose exact digest a human has
>    explicitly trusted for that operation.

The first category covers ordinary `vibe.yaml` changes. The schema can express
container data such as environment values, loopback ports, digest-pinned
images, and copied data mounts, but cannot express Docker capabilities or raw
Compose. The second category covers an extension Dockerfile. It is not called
safe merely because the engine snapshots it: Docker executes its instructions,
so the user approves its exact frozen digest separately.

Dev mode is a third, deliberately weaker boundary. It compiles
workspace-derived Go source into a host executable and therefore disables the
host-code provenance guarantee for that project. Entering or synchronizing dev
mode is a separate, explicit source-trust ceremony; an environment rebuild
request can never trigger it.

This is intentionally weaker, but more accurate, than v1's literal “never feed
container-writable bytes to Docker” sentence. Preserving that sentence would
require projects to select only host-predeclared opaque configurations, which
would defeat agent-driven environment layering.

## System overview

```text
┌─ host ──────────────────────────────────────────────────────────────────┐
│ ~/.vibe/bin/vibe (stable shim)                                          │
│   └─exec verified FD→ ~/.vibe/artifacts/<sha256>/vibe                   │
│                              │ attested Go engine                       │
│ tmux -L vibe ←───────────────┤ _sidebar / _state / _fleet              │
│ Docker API ←─────────────────┤ typed container/build requests           │
│                              ├ registry: host-owned project records     │
│                              └ snapshots: immutable approved inputs     │
└──────────────────────────────┼──────────────────────────────────────────┘
                               │ payload mounted read-only by digest
┌─ container (per project) ────┼──────────────────────────────────────────┐
│ .vibe/harness → payload (ro) ▼                                         │
│ lifecycle + preview scripts (bash 5, from payload)                      │
│ agent tmux session: claude / codex / grok                               │
│ requests written to .vibe/requests/; results read from host-state (ro)  │
└─────────────────────────────────────────────────────────────────────────┘
```

One release produces one artifact per supported platform:
`linux-amd64`, `linux-arm64`, and `darwin-arm64`. Each binary embeds the
container payload and project templates. Distribution is release-based; the
git mirror, submodule, `git archive` materialization, and user-authored Compose
scanner disappear.

The engine uses the Docker API, not Docker Compose, for create, start, exec,
build, inspect, and remove operations. This removes Compose's second
interpolation/parser phase and its implicit `.env`, `COMPOSE_*`, include, and
provider surfaces. A diagnostic `vibe config` prints the canonical typed model
as JSON; it is not an executable Compose file.

## Project surface

Projects author one closed, versioned `.vibe/vibe.yaml`:

```yaml
schema: 1
harness: v2.0.0
image:
  base: "mcr.microsoft.com/devcontainers/base@sha256:..."
  agents: [claude]
  toolchains: [go]
  extension: true
runtime:
  ports: ["127.0.0.1:34872:34872"]
  imports:
    - {source: ./models, target: /models, readonly: true}
  env: {MY_FLAG: "1"}
services:
  db: {image: "postgres@sha256:..."}
agent:
  cmd: claude
  tmux: true
env_file: .env
bootstrap:
  required: [git, gh, jq, rg, uv, claude]
  auto: {install: true, git_hooks: false, git_lfs: false}
```

Unknown keys and unknown enum values are errors. There is no raw Docker,
Compose, BuildKit, tmux, shell, or command passthrough.

### Manifest parser

The engine:

- accepts valid UTF-8 YAML only, with a 256 KiB default limit and a 1 MiB
  absolute ceiling;
- parses exactly one document with `gopkg.in/yaml.v3` into `yaml.Node`,
  validates the node graph, then decodes with `KnownFields(true)`;
- limits nesting to 32, total nodes to 10,000, entries in one collection to
  1,000, and one scalar to 64 KiB;
- rejects aliases, anchors, merge keys, custom tags, duplicate keys,
  non-string mapping keys, NUL, and disallowed control characters; and
- reports bounded errors without echoing terminal control bytes.

### Runtime model

The engine constructs Docker API structs itself and validates the final structs
immediately before the API call. Every container, including sidecars, gets a
closed policy:

- the dev container runs as the image's `vscode` user; extension images must
  end as that user;
- all Linux capabilities are dropped and `no-new-privileges` is set;
- privileged mode, added capabilities, devices, host PID/IPC/network/user
  namespaces, Docker sockets, SSH and host-secret mounts are not schema
  concepts;
- published ports bind only to a loopback address, including sidecar ports;
- required workspace, agent-state, and payload mounts are generated by the
  engine and checked for exact normalized targets;
- custom targets must be absolute, normalized, unique, and cannot equal,
  contain, or be contained by the workspace, payload, agent-state, lifecycle,
  or broker-result targets; and
- named volumes receive engine-generated names derived from the host project
  ID. External names and cross-project volume references cannot be supplied.

The only live workspace bind is the exact canonical project root selected from
the host registry. Arbitrary workspace subpaths are never passed to Docker as
live host bind sources. A `runtime.imports` entry is copied into an immutable,
host-owned content snapshot and that snapshot is mounted. Imports are for
bounded input data, not live code; users who need an additional live bind use
an explicit `--unsafe-bind` invocation, which records the canonical source and
target and disables the normal mount guarantee for that operation.

Environment values are opaque container data. They are assigned through Docker
API fields and never merged into a host child process environment. `env_file`
must be a workspace-relative regular file and is included in the immutable
input snapshot. It is parsed literally: no shell syntax, interpolation, or
variable expansion; keys are validated; duplicate keys, NUL, oversized keys or
values, and malformed lines are rejected.

Base, service, payload, and builder images are digest-pinned. Friendly tags may
be accepted during an interactive configuration change only after resolution;
the candidate snapshot and approval UI record the resolved registry digest.
The approved model and project record store the digest, never merely the tag.

### Snapshot algorithm

All workspace inputs used for a Docker operation are frozen before validation,
diff, or approval. The snapshotter is a git-free, FD-relative filesystem
walker:

- it starts from an already-open canonical project-root directory;
- it uses `openat2(RESOLVE_BENEATH|RESOLVE_NO_SYMLINKS)` on Linux and an
  `openat`/`fstat` no-follow component walk on macOS;
- it accepts an explicit allowlist for the operation rather than recursively
  copying `.vibe/` or the repository;
- it rejects symlinks, hard-linked files, sockets, devices, FIFOs, and any type
  other than a regular file or directory;
- it checks identity and metadata again after copying, caps file count,
  individual size, and aggregate bytes, and aborts on concurrent mutation;
- it writes into a mode-0700 host staging directory, fsyncs files and
  directories, computes a canonical Merkle digest, then atomically renames to
  immutable host state.

The ordinary runtime candidate contains `vibe.yaml`, the literal parsed
`env_file`, copied imports, resolved image digests, and the complete canonical
Docker model. Project hooks and source code remain workload content inside the
workspace; the host never reads them as configuration or executes them.

## Extension builds

An optional `.vibe/Dockerfile` is not part of the capability-bounded manifest
lane. Enabling or changing it creates a separate build candidate containing:

- the Dockerfile and an explicit, size-bounded context allowlist;
- the trusted payload/base image digest;
- the builder identity, frontend digest, target platform, network policy, and
  complete build options; and
- a digest of the resulting immutable candidate.

The user must approve that digest before the engine submits it to BuildKit.
Approval is per changed candidate, not a persistent trust in the pathname.
Custom `# syntax` frontends, remote/additional contexts, SSH or secret mounts,
host networking, privileged entitlements, device access, and arbitrary build
args are rejected. The frontend and base are digest-pinned. The builder uses a
dedicated rootless BuildKit instance with no host credentials or host bind
mounts and a deny-by-default entitlement policy. Its network policy is shown in
the approval; reproducible/offline builds are preferred but not claimed when
the Dockerfile downloads from the network.

Extension mode expands the trusted workload sent to Docker and the BuildKit
attack surface. First contact and each changed digest state this plainly.
Untrusted projects still belong in disposable checkouts with minimal
credentials; release attestation of the engine does not attest project code,
images, or Dockerfile instructions.

## Trust store and distribution

The immutable identity of executable code is an artifact SHA-256 digest, never
a tag, version string, path, or source hash:

```text
~/.vibe/                              # 0700, host-owned
├── bin/vibe                          # small stable shim
├── artifacts/<artifact-sha256>/
│   ├── vibe                          # platform binary
│   ├── payload/                      # unpacked from that binary
│   ├── manifest.json                 # canonical file digests + modes
│   └── provenance.json               # verified identity and release metadata
└── state/
    ├── projects/<project-id>.json    # canonical root identity + artifact digest
    ├── candidates/<digest>/          # immutable input/build candidates
    ├── broker/<project-id>/          # requests/results, host-owned
    └── locks/
```

A release record binds its human version, platform, artifact digest, payload
manifest digest, and verified provenance. Existing artifact directories are
never replaced. Acquisition uses staging, fsync, manifest verification, and
atomic rename under a native advisory lock. Project and candidate updates use
per-project locks under a documented global-before-project lock order. Garbage
collection cannot remove an artifact or candidate while a shared lease is
held.

The shim opens the artifact beneath the host store without following symlinks,
verifies it from the opened descriptor, and executes that descriptor where the
platform supports it. On platforms without descriptor execution it holds a
shared store lease, verifies the immutable inode, and executes its fixed
digest-addressed path. Payload directories use the same lease until Docker has
accepted the mount.

### Release verification

Release provenance verification is native and mandatory by default. The
trusted updater pins:

- the Sigstore/GitHub OIDC issuer;
- repository owner and repository;
- release workflow identity;
- allowed ref/tag policy;
- target platform; and
- the artifact digest covered by the attestation.

TLS and same-origin checksums provide transport/corruption checks only. They do
not authenticate the publisher. `gh` is not part of the verification path.
`vibe init`, `update`, and non-interactive provisioning fail closed if
provenance cannot be verified. CI has no bypass.

An interactive one-invocation `--unsafe-artifact <sha256>` escape hatch may
import an unattested binary, but it requires the expected digest on the command
line, displays that exact digest, and records unsafe provenance in the project
record and TUI. It cannot establish a release identity or satisfy CI.

The initial installer is a separately published, attested platform artifact
verified with the same pinned identity by a documented bootstrap verifier.
Installation never executes a downloaded checksum file or workspace script.
The detailed bootstrap mechanism is a release-design deliverable and blocks
M2 until its macOS and WSL2 paths are demonstrated end to end.

### Project identity and host commands

Project discovery walks upward from a physical, canonical cwd without invoking
git. Registration records the root's stable host identity (canonical path plus
platform file identity) and assigns a random project ID. Symlink roots,
ambiguous nested registrations, and identity changes require re-registration.
Display names never participate in trust lookup.

Host subprocesses are rare—the Docker API replaces the Docker/Compose CLI—but
tmux and any release/bootstrap helper run by absolute, prevalidated paths
outside every registered workspace. They receive a fixed minimal environment
with a trusted system PATH and explicit locale/home values. `LD_*`, `DYLD_*`,
`BASH_ENV`, `ENV`, `GIT_*`, `DOCKER_*`, `COMPOSE_*`, `TMUX_TMPDIR`, language
startup variables, and project environment values are not inherited.
Subprocesses use argv, never a shell command string.

The Docker endpoint is selected from host-owned configuration. Workspace
values and ambient `DOCKER_HOST` cannot redirect it. Doctor reports non-local
or unexpectedly privileged endpoints and refuses normal mode when endpoint
identity differs from the registered one.

Tmux sessions and actions use engine-generated opaque IDs. Paths and display
names are passed as argv or encoded tmux values, never interpolated into
`run-shell`, format strings, or shell fragments. Fleet selection dispatches
inside the Go process.

## Dev mode

Dev mode is not release-equivalent and does not preserve the normal host-code
provenance boundary. It exists only for developing this harness.

`vibe dev sync`:

1. snapshots an explicit source allowlist with the git-free snapshotter;
2. shows a terminal-safe source diff and the complete source Merkle digest;
3. requires a distinct confirmation that the resulting code will execute on
   the host;
4. builds in a digest-pinned builder with a verified dependency set and no
   host credentials;
5. records project ID, source Merkle root, builder image digest, dependency
   lock/checksum state, target, build settings, and output artifact digest; and
6. stores the output by artifact digest without allowing it to satisfy a
   release pin or another project's dev record.

The project record and TUI remain visibly marked `DEV—HOST BOUNDARY DISABLED`.
No broker action, `rebuild`, `up`, lifecycle action, or automatic file watcher
can invoke `dev sync`. Cross-compilation is supported only when the pinned
builder and dependency inputs make it deterministic enough to verify the
recorded output; source identity alone is never treated as artifact identity.

## Rebuild broker

The broker lets an agent request an environment change without granting it host
execution:

1. The agent edits workspace inputs and creates a size-capped
   `.vibe/requests/<ulid>.json` containing only `{kind, reason, summary}`.
2. The host polls with bounded work: a maximum number of entries inspected per
   poll, pending requests per project, accepted request size, and candidate
   builds per interval. It ignores symlinks and non-regular files and does not
   recursively walk the directory.
3. Before showing anything, the host snapshots the complete candidate into
   immutable host state and assigns its digest. Diff and validation operate
   only on that snapshot.
4. The approval view renders sanitized, bounded text plus trusted chrome and
   the candidate digest. It visibly escapes C0/C1 controls, ESC, bidi controls,
   invalid UTF-8, overlong lines, and hostile filenames. It uses no external
   pager and never emits untrusted terminal control sequences.
5. Approval names the candidate digest. The engine builds exactly that
   candidate under a project lock. Later workspace changes become a different
   pending candidate; there is no “fresh snapshot after approval.”
6. Results are written only under `state/broker/<project-id>/`. A dedicated
   read-only mount and `vibe request status` expose them inside the container.
   The host never writes into `.vibe/requests/` or any other workspace path.

Extension candidates use the separate Dockerfile approval described above.
Dev-source changes cannot be requested through this protocol.

## TUI and fleet

One host tmux server on the fixed `vibe` socket owns one session per project.
The Go engine implements `_sidebar`, `_state`, `_fleet`, and `_statusline`;
tmux configuration remains static trusted payload.

The host registry makes `vibe tui` cwd-independent. Fleet lists registered
projects by escaped display label and opaque project ID. Selecting a stopped
project dispatches `up` inside the current verified engine. Forgetting a
project removes only its registry record after confirmation; artifact,
snapshot, volume, and container garbage collection are separate explicit
operations.

Security-relevant approval views do not reuse raw container output. Ordinary
agent panes still expose the host terminal emulator to untrusted output; that
residual risk is documented, but it is not part of an approval ceremony.

## Command surface

```text
init [--preset P] [--tui]      seed .vibe/, register, build, enter TUI
up / rebuild / build / down / status / config / doctor
agent [-a CMD] [-s NAME] / run CMD / exec CMD / shell / attach [S]
tui [--kill|--fresh|--detach] / ps / bootstrap
update [VERSION] / provision  acquire and record verified release artifacts
dev {on|sync|off|status}       explicit weakened-boundary harness development
clip / show / review           image hand-off and preview
request {list|show|approve|reject|status}
_sidebar / _state / _fleet / _statusline
```

Container-side, `vibe` on PATH is a payload shell shim limited to
container-facing verbs. It cannot reach the Docker API or mutate host records.

## Repository after cutover

```text
cmd/vibe/                   # dispatch to internal/cli
internal/cli/               # user and private command handlers
internal/schema/            # bounded YAML parser + typed manifest
internal/model/             # canonical Docker API model and policy
internal/snapshot/          # FD-safe, git-free immutable snapshots
internal/store/             # artifacts, provenance, leases, atomic records
internal/registry/          # canonical project identity and fleet
internal/broker/            # bounded request polling and digest approvals
internal/terminal/          # terminal-safe untrusted-text renderer
internal/tmuxui/            # sidebar/state/fleet/statusline
internal/dockerapi/         # narrow Docker API client + test fake
payload/                    # embedded Dockerfile, scripts, configs, templates
assets.go                   # //go:embed payload
verify.sh                   # shellcheck payload + unit/integration/e2e suites
.goreleaser.yaml  .github/workflows/ci.yml
docs/  examples/  .vibe/    # dogfood v2 project
```

At cutover, the root Bash `vibe`, `install.sh`, `src/scripts/host/`,
`src/compose/`, and submodule flow are removed. Container scripts move from
`src/scripts/` to `payload/scripts/` and remain Bash 5.

## Verification and release gates

Unit and property tests cover:

- parser limits, duplicate/alias/tag rejection, and schema accept/reject tables;
- typed-model policy, protected target intersections, volume scoping, loopback
  ports, environment separation, and image digest enforcement;
- snapshot races, symlinks, hardlinks, special files, path traversal, mutation
  during copy, and platform-specific directory walking;
- artifact acquisition, native provenance verification, atomic install,
  immutable replacement refusal, lease/GC concurrency, and project identity;
- hostile broker floods, approval TOCTOU, terminal escapes/bidi, and
  host-to-workspace write prohibition; and
- host executable resolution and environment scrubbing.

Integration tests use a fake Docker API to assert exact request structs and
that no user value enters a host process environment. End-to-end CI uses a
real local Docker daemon to initialize a scratch repository, start it, run
doctor, exercise a broker candidate, rebuild, and remove it. Separate WSL2 and
macOS release-candidate runs verify project discovery, file locking,
snapshotting, tmux dispatch, bootstrap provenance, and Docker endpoint
selection.

Release artifacts are produced for linux/amd64, linux/arm64, and darwin/arm64,
with SHA-256 checksums and GitHub/Sigstore provenance. Shared base, frontend,
and builder images are pinned by digest in source. Resolved project image
digests form part of each approved candidate.

## Milestones

- **M0 — threat-model closure:** accept this boundary; prototype Docker API
  coverage, Linux/macOS snapshot walkers, native provenance verification,
  bootstrap verification, and rootless BuildKit constraints. No engine
  implementation starts until those proofs close.
- **M1 — engine core:** parser, canonical model, snapshotter, store/shim,
  registry, Docker API client, and core lifecycle/doctor commands.
- **M2 — distribution:** init/presets, update/provision, mandatory provenance,
  bootstrap installer, explicit dev mode, release matrix, and real WSL2/macOS
  verification. First tagged v2 prerelease.
- **M3 — TUI/fleet:** TUI private commands, cwd-independent fleet, image-review
  parity, and hostile-terminal tests.
- **M4 — broker and extensions:** digest-bound request protocol, terminal-safe
  approval, agent contract, and separately approved rootless extension builds.
- **M5 — cutover:** dogfood v2, delete the v1 host layer, complete destructive
  reinstall documentation, then tag v2.0.0.

## Explicit non-goals and residual risks

- v2 is not a sandbox for malicious repositories, images, Dockerfiles,
  lifecycle hooks, package installers, or agent-supplied code.
- Docker and the selected daemon remain trusted privileged infrastructure.
  The typed API reduces configuration surface; it cannot contain a daemon or
  kernel vulnerability.
- An approved extension Dockerfile is arbitrary workload code executed by
  BuildKit. Snapshot and approval provide identity and intent, not safety.
- Live project source remains a workspace bind because editing it is the
  product. Additional live binds and unsafe artifacts explicitly weaken the
  boundary.
- Credentials deliberately exposed to a project remain readable by that
  project. Per-project state limits cross-project reach but is not a vault.
- Ordinary container output still reaches tmux and the terminal. Only
  security-decision views promise terminal-safe rendering.
- Dev mode deliberately executes workspace-derived host code and displays that
  the normal host-code provenance guarantee is disabled.

These residuals are first-contact documentation, not footnotes hidden behind an
attested-engine claim.

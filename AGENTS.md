# Agent instructions

## What this repository is

`vibe` is a containerized agent-development harness: one compiled Go
binary that owns the host command surface (project registry, immutable
artifact/candidate store, Docker lifecycle, release updates, tmux UI,
rebuild broker, dev-mode builds) and embeds the container payload it
mounts into every project. Coding agents run *inside* the containers
this engine manages; the engine itself is the host-side root of trust.

The engine is the Go code under `cmd/vibe` and `internal/`, documented
as-built in [docs/architecture.md](docs/architecture.md) and
[docs/engine-internals.md](docs/engine-internals.md); all eight
implementation slices of the original design are in the tree. The v1 bash/compose
harness was removed at the cutover and lives only in git history — do not
resurrect pieces of it, and **never add v1-record importers or migration
paths**: v1 installs reinstall, projects `vibe init` fresh. The
repository dogfoods itself: `.vibe/vibe.yaml` makes this checkout a v2
project, and `vibe dev on` builds the engine from it.

## Build, test, verify

```sh
go build -o bin/vibe ./cmd/vibe   # use -o: the default output name `vibe`
                                  # at the repo root is gitignored clutter
go test ./...
go vet ./...
gofmt -l .                        # must print nothing
golangci-lint run ./...           # policy in .golangci.yml
go generate ./internal/payload    # after editing payload/** (regenerates
                                  # payload/manifest.json; CI fails on drift)
go test ./internal/model -run TestCompileGolden -update   # after intended
                                  # canonical-plan changes; review the diff
```

Every change must keep all three release targets compiling:
`GOOS=linux GOARCH=amd64`, `GOOS=linux GOARCH=arm64`,
`GOOS=darwin GOARCH=arm64`, each with `CGO_ENABLED=0`.

**Dogfood handoff — nothing lands in a running project until it rides a
new artifact.** Engine code AND `payload/**` ship inside the binary;
containers mount the store-owned artifact copy, never this repo's
tree, so committing (or even building) changes nothing you can run:

```sh
vibe dev sync    # rebuild the dev artifact from this source, repin it,
                 # repoint ~/.vibe/bin/vibe at the fresh build, AND
                 # restamp a running tui (@vibe_exe/@vibe_payload_dir):
                 # host-side pieces land live — sidebars self-exec the
                 # new script within a slow tick (~30s)
vibe rebuild     # recreate containers on the new artifact's payload
                 # (post-create reruns; container-side pieces need this
                 # — nothing restamps a mounted payload)
```

When you finish a change the operator will try live, say so and name
this cycle — it is the standing reminder this section exists for.
Two refinements the dock work taught (2026-08-14): `dev sync`
re-sources the tmux conf on a RUNNING ui, so binds and hooks land
live, but anything stamped on panes/sessions at creation (titles,
tints, `@vibe_engine_ver`) only lands on re-mint or flip; and TUI
chrome/mouse changes should be proven headlessly against the pinned
tmux BEFORE the sync — `build/tui-screendump.py` plus the SGR
injection rig (docs/tui-layout.md "Verification", which also records
the measured 3.7b mouse/format quirks so they aren't re-derived).

Integration tests that need a Docker daemon (`internal/dockerapi`
`TestSDKLifecycle`) skip themselves when no daemon is reachable — never
convert that skip into a failure, and never mock around it: it is the
only place SDK translation meets a real daemon.

Manual smoke test without touching your real `~/.vibe`:

```sh
export HOME=$(mktemp -d)
bin/vibe init && bin/vibe provision && bin/vibe config
```

## Architecture map

`cmd/vibe/main.go` is wiring only. Business logic lives in `internal/`,
with strictly inward dependencies:

```text
cli → app → { runtime → model → schema
              dockerapi (only package importing Docker SDK types)
              builder, broker, dev, initproject, doctor, release,
              payload, registry, store, snapshot, tmux, tmuxui }
shared leaves: domain, envfile, lock, paths, progressui, runner,
               terminal, version
```

Per-package responsibilities live in the package doc comments and
[docs/engine-internals.md](docs/engine-internals.md) — don't restate
them here. The boundaries that matter: `dockerapi` is the **only**
package that may import Docker SDK packages (everything else sees its
narrow `Client` interface or the programmable `dockerapi/fake`);
`schema` knows YAML but never Docker; `model` compiles the canonical,
golden-tested `Plan` and never sees SDK types; `tmuxui` renders pure
views (only `terminal` as a dependency, no tmux calls).

The `payload/` directory at the repo root is the embedded payload:
`container/` (entrypoint, lifecycle/services runners, agent
session/state/statusline scripts, and the `claude-plugin/` loaded per
session via `--plugin-dir`), `host/` (tui conf + host scripts),
and `presets/` (including the shared `common` overlay every preset
renders on top of);
`payload/manifest.json` is generated, tracked, and authoritative for
file modes and digests. The language split is deliberate: the Go
engine is the trusted custodian (image content, exec argv, identity
env, anything rendering container-controlled bytes), and tmux/UI
mechanics stay payload shell — container-side under `container/`,
host-side under `host/`, where the host executes only store-owned
bytes, never workspace files.

## Invariants — do not break these

**Trust boundary.** The host never executes, sources, or evals any byte
a container could have written. Workspace files (`vibe.yaml`, env files,
Dockerfiles) are read strictly as data through bounded, strict parsers,
frozen into immutable snapshots *before* use, and never re-read from
the workspace by later stages; request JSON is bounded-read in place,
with its decision bound to the immutable candidate digest instead.
Env-file values are container data and exec-scoped: injected per
`vibe run`/agent exec through Docker API fields, never
container-ambient, never a host process environment, never the
canonical plan.

**Closed container policy.** Every managed container gets
`cap_drop: ALL` and `no-new-privileges`; the dev container additionally
runs as `vscode` (sidecars keep their image's user). One narrow,
engine-owned exception: the dns ledger sidecar runs as root with
exactly `NET_BIND_SERVICE` re-granted (the CoreDNS binary's file
capabilities EPERM at exec against an empty permitted set); no schema
surface can express it and `model.Validate` rejects it on any other
container. Published
ports bind loopback only; mount targets are absolute, normalized,
unique, non-nesting, and never collide with the engine-owned targets
(`/workspace`, `/vibe/payload`, `/vibe/agent-state`, `/vibe/results`,
the `/run/user/1000` tmpfs, and `/vibe/dns`).
The workspace bind is the exact registered root — never a subpath, never
another live host path. There is no raw Docker/Compose/shell
passthrough anywhere in the schema, and no command accepts a shell
string: argv only, everywhere (`exec`, probes, tmux, builders).

**Immutability and determinism.** Artifacts, candidates, and snapshots
are addressed by tree digest, published by atomic rename, and never
mutated. Identical inputs must produce identical digests — anything
feeding a digest (canonical JSON field order, sorted slices, normalized
file modes, merkle line format in `store.DigestTree`) is a compatibility
surface; bump the relevant format number when its meaning changes.
Persistent records are versioned JSON, decoded with
`DisallowUnknownFields`, dispatched by `format`.

**Ordering and safety.** Lock order is fixed: store-global →
artifact/candidate → project; never acquire earlier while holding
later. The store-global lock is shared by reference-minting flows
(from their first store publish through their durable root write) and
exclusive for GC, with an intent gate inside the locker preventing
shared-churn starvation. Mutable records (registry `Approved`, pins)
move only *after* the durable object they reference exists and its
containers run — a failed `up` must not move the approved-candidate
pointer. Container replacement is journaled and failure-atomic: the
old generation is parked as `.prev` and restored on any failure, and
nothing is deleted before its replacement (or restoration) is proven —
see `internal/runtime/transaction.go`. Reconciliation never removes a
container it did not decide to replace, and refuses name-colliding
containers that lack `dev.vibe.managed`. GC roots include candidates
and artifacts labeled on live managed containers and fails closed on
unreadable live metadata.

**Untrusted text.** Agent-authored strings (request reason/summary,
statusline messages, Dockerfiles shown in prompts) reach a terminal only
through `terminal.Encode`/`terminal.Line`, and prompt chrome stays
structurally separate from encoded content. Extension builds require
per-digest operator approval; the build context is a restricted copy
that must never contain the env file or manifest.

**Error and exit discipline.** Wrap `domain.Err*` sentinels with `%w`;
the CLI maps them to stable exit codes (0 ok, 1 failure, 2 usage,
3 invalid config, 4 not registered / not found, 5 conflict,
6 unavailable or unsupported, 130 interrupted). App methods return typed results and never print;
only `cli`, `terminal`, and `tmuxui` produce user-facing text.

**Construction discipline.** Dependencies are injected via
`app.Dependencies`; no package globals, `init` side effects, or ambient
environment reads in core packages. External processes go through
`runner.Runner`; Docker goes through `dockerapi.Client`. Everything
external is behind a fakeable interface — new features come with
fake-backed tests asserting full request equality, not selected fields.

## Conventions

- Table tests per package; failure and cancellation paths included.
- `t.TempDir()` layouts via `paths.NewLayout`; never touch the real
  `~/.vibe` in tests.
- New commands: typed request structs parsed in `internal/cli`
  (`commands_*.go` groups, one merge point in `commands.go`), an
  `app.App` method returning a typed result, and a renderer in
  `output.go` deriving human and `--json` output from the same model.
- Standard library first. Current direct deps (yaml.v3, the docker SDK
  plus go-connections and containerd/errdefs — daemon error
  classification, confined to `dockerapi` — and x/term) are pinned;
  adding one needs a reason the stdlib can't answer. `gopkg.in/yaml.v3` is archived upstream — a swap candidate,
  not a pattern to extend.
- Comments state constraints the code can't show; match existing
  density.

## Known open work

Scheduled work lives in [ROADMAP.md](ROADMAP.md) (release pipeline,
install story, Sigstore provenance — the path to the first tagged
release); unscheduled ideas and decision records in
[BACKLOG.md](BACKLOG.md).

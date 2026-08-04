# Engine internals

**Authority: as-built.** Contributor reference for the Go engine:
load-bearing formats, orderings, and the testing bar. Where this page
disagrees with the code, the page is the bug. The system-level view
lives in [architecture.md](architecture.md); agent-facing rules and
invariants in [../AGENTS.md](../AGENTS.md). Per-package
responsibilities live in the package doc comments — this page does not
restate them.

## Design rules

- `cmd/vibe` is wiring plus stderr progress rendering; business logic
  lives under `internal/` with strictly inward dependencies.
- Every operation receives `context.Context`; long operations honor
  cancellation and emit structured progress.
- Paths enter the core as typed absolute paths from the `paths` and
  store resolvers; core packages never repeatedly clean arbitrary strings.
- Persistent records are versioned JSON with deterministic encoding
  (fixed struct field order, sorted slices), decoded with
  `DisallowUnknownFields`, dispatched by `format`
  (`store.DecodeRecord`). Dev-mode provenance records predate the
  helper and skip both — recorded debt, not a pattern to copy.
- Immutable objects are addressed by SHA-256 digest; display versions and
  project names are metadata, never identity.
- Writes use staging + fsync + atomic rename. Mutable records use
  compare-and-swap revisions under a project lock.
- External processes go through `runner.Runner`; Docker goes through
  `dockerapi.Client`; everything external is behind a fakeable interface.
- Dependencies are injected via `app.Dependencies`: no package globals,
  `init` side effects, or ambient environment reads in core packages
  (the tmux client's env allowlist is the one deliberate exception).
- User-facing text is produced only by `cli`, `terminal`, and `tmuxui`;
  app and domain packages return typed results.
- Standard library first; each of the few direct dependencies needs a
  reason the stdlib can't answer.

## Knowledge boundaries

`schema` knows YAML, never Docker. `model` knows runtime semantics,
never SDK types. `dockerapi` performs the one translation from engine
types to SDK requests and is the **only** package allowed to import
Docker SDK packages. `tmuxui` renderers are pure functions over view
models — no Docker, registry, or tmux calls inside a render (its only
dependency is `terminal`). Sentinel errors live in `domain`; wrap them
with `%w` and the CLI maps error classes to the stable exit codes
([usage.md](usage.md) has the one table).

## Load-bearing formats

Anything feeding a digest is a compatibility surface; bump the relevant
format number when its serialized meaning changes.

- **TUI porcelains** (`internal/tmuxui` `Fleet()`/`Agents()` and the
  `_frame` records): version-prefixed, US-separated; currently v3
  (2026-08-04 — fleet dropped the unread engine-version field, agents
  the unread detail). Read tolerance is exactly ONE generation (v2
  parses, v1 pruned) — the window is a previous-artifact `sidebar.sh`
  or `_watch` daemon driving the new binary until its slow-tick
  self-exec / shim-drift retire; the `_frame` W record requires all 12
  fields, the S record's trailing svc_fold stays optional one
  generation.
- **Merkle tree digest** (`store.DigestTree`): one line per entry,
  `type NUL mode NUL size NUL relative-path NUL content-digest LF`,
  slash-separated paths on every platform, sorted — deterministic across
  Linux and macOS for identical content and modes.
- **Canonical plan** (`model.CanonicalJSON`): fixed field order, sorted
  slices, normalized mounts/ports/env; `model.Hash` computes the digest
  with the plan's `CanonicalHash` field cleared. Golden-tested — after
  intended plan changes run
  `go test ./internal/model -run TestCompileGolden -update`
  and review the diff.
- **Persistent records**: artifact records (digest, version, platform,
  binary/payload digests, provenance), candidate records (project, kind,
  snapshot/plan digests, resolved images), registry records (root
  identity, artifact pin, approved candidate, CAS `revision`). All
  versioned JSON as above.
- **Docker labels**: every managed object carries `dev.vibe.managed`
  and `dev.vibe.project`; containers add `dev.vibe.candidate`,
  `dev.vibe.artifact`, and `dev.vibe.role` (`dev`, `sidecar:<name>`,
  or `dev-builder` on dev-mode build containers). Containers whose plan
  names a resolver service also carry `dev.vibe.dns` — the
  runtime-resolved sidecar address, deliberately outside the candidate
  digest; reconcile treats a mismatch as drift and replaces.
- **Egress view formats**: the sampler TSV
  (`egress-sample\t1` version line, then
  `proto\tlocal\tremote\tpid\tcomm` rows — script and parser move
  together inside one artifact, wrong version fails the whole sample)
  and the CoreDNS query-log parse anchor (the quoted seven-field
  request section only; every other line counts as unparsed, never
  fatal, never rendered raw).
- **Schema limits** (manifest parser): ≤256 KiB default / 1 MiB ceiling,
  one document, depth ≤32, ≤10,000 nodes, ≤1,000 entries per collection,
  ≤64 KiB per scalar; aliases, anchors, merge keys, custom tags,
  duplicate keys, non-string keys, NUL, and control characters rejected.

## Candidate preparation and reconcile

Candidate creation is one pipeline shared by `up`, `rebuild`, and broker
requests (`app`): snapshot manifest + env file + imports → load/validate
the manifest *from the snapshot* (a malformed env file fails here) →
resolve image references to digests, building the tools and approved
extension images as needed → compile and validate the plan → publish
under the candidate digest. No later stage rereads workspace paths.

Reconcile (`runtime.Up`, under the project lock with artifact,
candidate, and snapshot leases held): ensure networks, then volumes →
for each planned container, sidecars first and the dev container last,
compare any existing container by its `dev.vibe.candidate` label —
matching containers are started if stopped, mismatched (or `--force`)
ones are replaced transactionally, and images are pulled lazily only
when create reports them missing → run lifecycle hooks (post-create
marker-guarded on every reconcile, post-start only after an actual
create or start; only when the payload is mounted and carries
`lifecycle.sh`) → re-inspect every desired container and require it
running (a sidecar that started and immediately exited fails the
reconcile while the old generation is still restorable).

Replacement is failure-atomic. Every container mutation is recorded in
a durable per-project journal (`state/replace/<id>.json`, written
before the Docker call it describes) stamped with a per-transaction
nonce that also rides every created container as the immutable
`dev.vibe.txn` label. A replacement stages the new container as
`<name>.next` (created, never started — the old one keeps running
through any pull), then stops and parks the old one as `<name>.prev`,
renames the staged one in, and starts it. On any failure — container
op, lifecycle hook, or the liveness gate — the whole topology rolls
back in reverse: stop the new, park it aside, restore and restart the
old, and only after the restore is proven delete the new. Matching is
by recorded container ID or the full ownership conjunction (name +
nonce + managed + project + candidate labels), never name alone; an
old container that vanished externally fails closed with the unproven
replacement kept (something running beats nothing). The journal is the
single phase marker: deleting it (fsynced) is commit, after which
`.prev` leftovers are debris; a present journal makes the next `up`'s
sweep finish the rollback before reconciling, and an unreadable one
aborts fail-closed. A failed reconcile still never removes a container
it did not decide to replace and refuses name-colliding containers
lacking `dev.vibe.managed`. The approved-candidate pointer moves
afterwards, in `app`, under a fresh CAS registry update — which is why
a failed `up` cannot move it.

Garbage collection is app-orchestrated (`app.GC` gathers roots:
registry pins, approved candidates, pending broker bindings, the
newest release artifact, candidates and artifacts labeled on live
managed containers — running or stopped, any project — and their
snapshots) over store primitives (`ListObjects` / `ObjectStat` /
`RemoveObject` / `CleanStaging`). Live-container metadata fails
closed: an unreachable daemon, a malformed candidate or artifact
label, or an unreadable candidate record aborts the whole GC rather
than narrowing the root set. `RemoveObject` takes the object's
exclusive flock non-blocking — any live shared lease defeats it — then
leaves the published path in one rename before deleting. GC holds the
store-global lock exclusively, while every flow that mints a durable
reference (up, init, broker adoption, dev builds, release publishes,
dev off) holds it shared from its first store publish until its root
record is written — candidate publication is idempotent and preserves
mtimes, so the age floor alone cannot shield a re-referenced old
object. The age floor remains as defense-in-depth for in-flight
publishes.

## Concurrency

Lock order is fixed — never acquire an earlier lock while holding a
later one:

```text
store-global → artifact/candidate → project
```

The store-global lock has reader/writer semantics: reference-minting
flows hold it shared, GC exclusively. Every acquisition first passes
through the name's intent gate inside the locker (a per-process
singleton that serializes waiters through one blocking flock, released
per logical holder), so an exclusive waiter drains existing shared
holders instead of being systematically starved by new ones — the
nonblocking poll loop alone only wins if the lock happens to be free
at a poll instant. Fairness across processes is kernel wake order:
probabilistic, an availability property only, never load-bearing for
safety.

Mutable records move only *after* the durable object they reference
exists and its containers run. Read-only status is a bare Docker list —
no lock, no lease; lifecycle mutations serialize per project.

## Testing strategy

- **Unit**: table tests per package, failure and cancellation paths
  included. Layouts via `paths.NewLayout` under `t.TempDir()` — never
  the real `~/.vibe`.
- **Fakes over mocks**: `dockerapi/fake` is programmable and records
  requests; tests assert **full request equality**, not selected
  fields. Runner/tmux/release fakes are lighter ad-hoc test doubles —
  hold new ones to the dockerapi bar.
- **Golden**: `model` canonical JSON (`-update` flag). `tmuxui` views
  are width-bound-asserted, not golden.
- **Integration**: `internal/dockerapi` `TestSDKLifecycle` runs against
  a real daemon and self-skips without one — never convert that skip to
  a failure or mock around it; it is the only place SDK translation
  meets a real daemon. CI runs it on push.
- **Fuzz**: native fuzz targets cover the hostile-input surfaces —
  schema load (`FuzzLoad`), envfile (`FuzzParse`), digest/ID parsers
  (`FuzzParsers`), broker request JSON (`FuzzParseRequest`), and the
  terminal encoder and diff (`FuzzEncode`/`FuzzDiff`). Seeds run in
  every `go test`; CI adds short live bursts. Found crashers are saved
  under `testdata/fuzz/` and committed as permanent regression seeds.
- **CI** (`.github/workflows/ci.yml`): gofmt, vet, build, test,
  golangci-lint, payload-manifest drift (`go generate
  ./internal/payload` must be clean), ShellCheck on the container
  payload, the three-platform cross-compile matrix with
  `CGO_ENABLED=0`, and the fuzz-burst job.
- **Owed** (tracked in [../ROADMAP.md](../ROADMAP.md)): real-daemon CI
  for the tools/extension/dev build paths.

The new-command shape and the day-to-day verification gate live in
[../AGENTS.md](../AGENTS.md); they are not repeated here.

## Dependencies

Direct dependencies are pinned and deliberate: `gopkg.in/yaml.v3` (node
parsing; archived upstream — a swap candidate, not a pattern to
extend), the Docker SDK plus `go-connections` (client + API types,
imported only by `dockerapi`), and `golang.org/x/term` (prompt TTY
handling). Locking and path confinement are stdlib (`syscall` flocks,
`os.Root`). Provenance verification may add a minimal Sigstore set when
that milestone lands. All three release targets (`linux-amd64`,
`linux-arm64`, `darwin-arm64`) must always compile with
`CGO_ENABLED=0`.

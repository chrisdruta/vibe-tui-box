# Go engine implementation design

Status: implementation draft 2026-07-23 · derives from
[architecture-v2.md](architecture-v2.md) · clean-slate v2 only

## Goal

Build one Go executable, `vibe`, that owns the host command surface, project
registry, immutable artifacts and candidates, Docker lifecycle, release
updates, tmux UI, and rebuild requests. Container lifecycle and preview scripts
remain Bash and ship as embedded payload.

This document defines the code shape: packages, data structures, interfaces,
command flows, storage formats, platform seams, and implementation order.

## Design rules

- `cmd/vibe` contains wiring only. Business logic lives under `internal/`.
- Packages depend inward on small domain types and interfaces, not on CLI
  parsing, Docker SDK structs, tmux output, or filesystem globals.
- Every operation receives `context.Context`.
- Paths enter the core as typed absolute paths returned by the project or store
  resolvers. Core packages do not repeatedly clean arbitrary strings.
- Persistent records are versioned JSON with deterministic encoding.
- Immutable objects are addressed by SHA-256 digest; display versions and
  project names are metadata.
- Writes use staging plus atomic rename. Mutable records use compare-and-swap
  revisions under a project lock.
- External processes are isolated behind `runner.Runner`. Docker operations
  use the Docker API behind `dockerapi.Client`.
- User-facing text is produced by `cli`, `terminal`, or `tmuxui`; domain
  packages return typed results and errors.
- Packages expose constructors and narrow interfaces. Avoid package globals,
  `init` side effects, and hidden environment reads.
- The initial implementation uses the standard library unless a dependency
  materially replaces complex protocol code.

## Repository layout

```text
cmd/vibe/
  main.go                       process entry

internal/
  app/
    app.go                      dependency container and command methods
    lifecycle.go                up/build/rebuild/down/status/config
    projects.go                 init/register/forget/ps
    agents.go                   run/exec/shell/agent/attach/bootstrap
    releases.go                 update/provision
    requests.go                 request list/show/approve/reject/status
    tui.go                      tui and private renderer commands
    dev.go                      dev on/sync/off/status

  cli/
    cli.go                      argv dispatch and exit codes
    flags.go                    shared flag parsing
    commands.go                 command table and help
    output.go                   JSON/plain output selection

  domain/
    digest.go                   Digest and parsing
    ids.go                      ProjectID, CandidateID, RequestID
    platform.go                 OS/architecture pair
    errors.go                   sentinel and typed errors

  paths/
    layout.go                   ~/.vibe layout
    project.go                  cwd discovery and canonical root
    platform_linux.go           Linux file identity
    platform_darwin.go          macOS file identity

  schema/
    load.go                     bounded YAML loading
    inspect.go                  yaml.Node validation
    decode.go                   KnownFields decode
    types.go                    project manifest types
    validate.go                 field-level validation
    diagnostics.go              path-aware errors

  model/
    compile.go                  manifest + resolved inputs -> Plan
    plan.go                     canonical engine model
    validate.go                 final-model checks
    targets.go                  mount target normalization/intersection
    names.go                    generated Docker names and labels

  snapshot/
    snapshot.go                 snapshot service
    walk.go                     common FD-relative walker
    limits.go                   input limits
    merkle.go                   canonical digest
    platform_linux.go           openat2 implementation
    platform_darwin.go          openat/fstat implementation
    fake_test.go                deterministic test walker

  store/
    store.go                    immutable object operations
    artifacts.go                release/dev artifacts
    candidates.go               runtime and build candidates
    manifests.go                payload manifests
    leases.go                   active-use leases
    atomic.go                   staging/fsync/rename helpers
    gc.go                       explicit garbage collection

  lock/
    lock.go                     Locker interface and lock ordering
    flock_unix.go               advisory file locks

  registry/
    registry.go                 project record CRUD
    records.go                  persistent types
    discover.go                 cwd -> registered project
    fleet.go                    ordered project summaries

  release/
    acquire.go                  release download and selection
    verify.go                   provenance verification
    metadata.go                 release records
    source.go                   canonical release source
    fake_test.go                test release source

  payload/
    payload.go                  embedded payload extraction
    manifest.go                 embedded manifest
    templates.go                preset access

  dockerapi/
    client.go                   narrow Client interface
    sdk.go                      Docker SDK adapter
    types.go                    engine-facing request/result types
    images.go                   pull/resolve/build
    containers.go               create/start/stop/remove/inspect
    exec.go                     exec and attach
    events.go                   lifecycle event helpers
    fake/
      client.go                 programmable test fake

  builder/
    builder.go                  extension build orchestration
    candidate.go                build candidate assembly
    progress.go                 normalized build progress

  runtime/
    runtime.go                  lifecycle orchestration
    reconcile.go                desired Plan vs current Docker state
    labels.go                   engine-owned Docker labels
    state.go                    project runtime status

  envfile/
    parse.go                    literal dotenv parser
    types.go                    validated environment entries

  runner/
    runner.go                   fixed-environment argv subprocesses
    resolve.go                  executable discovery at startup
    fake_test.go                recorded invocation fake

  tmux/
    client.go                   typed tmux operations
    commands.go                 argv construction
    formats.go                  generated opaque identifiers
    fake_test.go                recorded command fake

  tmuxui/
    tui.go                      session/server construction
    sidebar.go                  sidebar view model and renderer
    state.go                    tab/state renderer
    fleet.go                    project picker
    statusline.go               agent statusline
    palette.go                  action palette

  broker/
    poll.go                     bounded request discovery
    parse.go                    request JSON parser
    records.go                  request/result types
    service.go                  snapshot/list/approve/reject flow
    limits.go                   polling and queue limits

  terminal/
    sanitize.go                 untrusted text encoder
    diff.go                     bounded candidate diff
    prompt.go                   confirmation interface

  dev/
    service.go                  explicit dev state transitions
    source.go                   source allowlist snapshot
    build.go                    reproducible build invocation
    records.go                  dev provenance record

  initproject/
    init.go                     new-project transaction
    presets.go                  preset selection
    render.go                   template rendering

  doctor/
    doctor.go                   check runner
    checks.go                   registered checks
    report.go                   stable report model

  version/
    version.go                  build-injected release metadata
```

The current `internal/compose` package is removed. `internal/store`,
`internal/registry`, and `internal/tmuxui` are replaced with real packages;
their migration-plan `doc.go` files are deleted when implementation begins.

## Package dependency direction

```text
cmd/vibe
  └── cli
      └── app
          ├── runtime ── model ── schema
          │    └── dockerapi
          ├── builder ── dockerapi
          ├── broker ── snapshot, model, builder
          ├── dev ── snapshot, store, runner
          ├── initproject ── payload, registry
          ├── doctor
          ├── release ── store
          ├── tmuxui ── tmux
          └── registry, store, paths

shared leaves: domain, envfile, lock, runner, terminal, version
```

`schema` knows YAML but not Docker. `model` knows desired runtime semantics but
not Docker SDK types. `dockerapi` translates the canonical model into SDK
requests. `runtime` coordinates those packages.

## Shared domain types

### Digests and identifiers

```go
package domain

type Digest struct {
	algorithm string
	hex       string
}

func ParseDigest(string) (Digest, error)
func SHA256([]byte) Digest
func (d Digest) String() string
func (d Digest) Hex() string
func (d Digest) IsZero() bool

type ProjectID string
type CandidateID string
type RequestID string

func NewProjectID() (ProjectID, error)
func ParseProjectID(string) (ProjectID, error)
func ParseRequestID(string) (RequestID, error)
```

Constructors validate representation once. Persistent and API types use these
types rather than raw strings.

### Error model

```go
var (
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("conflict")
	ErrInvalid      = errors.New("invalid input")
	ErrUnavailable  = errors.New("unavailable")
	ErrNotSupported = errors.New("not supported")
	ErrCanceled     = errors.New("canceled")
)

type FieldError struct {
	Path    string
	Line    int
	Column  int
	Message string
}

type OpError struct {
	Op      string
	Project ProjectID
	Err     error
}
```

Errors retain structured context and implement `Unwrap`. The CLI maps error
classes to stable exit codes:

```text
0 success
1 operation failed
2 command usage
3 invalid project configuration
4 project/artifact not registered
5 conflict or concurrent change
6 external dependency unavailable
130 interrupted
```

## Process entry and application wiring

`cmd/vibe/main.go` performs only:

1. install signal-aware root context;
2. load build version information;
3. construct the host layout;
4. resolve required host executables;
5. open registry, store, Docker client, and tmux client;
6. create `app.App`;
7. dispatch `os.Args[1:]` through `cli.Run`; and
8. render the returned result/error and exit.

```go
type Dependencies struct {
	Clock       Clock
	Layout      paths.Layout
	Store       *store.Store
	Registry    *registry.Registry
	Snapshots   *snapshot.Service
	Docker      dockerapi.Client
	Runner      runner.Runner
	Tmux        tmux.Client
	Prompt      terminal.Prompt
	Release     *release.Service
	Payload     *payload.Bundle
	Version     version.Info
}

type App struct {
	deps Dependencies
}

func New(deps Dependencies) (*App, error)
```

Construction validates dependencies. Methods do not fall back to ambient
environment or package-level defaults.

## CLI design

Use the standard `flag` package behind a small command table. Avoid a large CLI
framework until command nesting or completion generation demonstrates a real
need.

```go
type Command struct {
	Name        string
	Summary     string
	Usage       string
	Hidden      bool
	Parse       func(args []string) (Request, error)
	Run         func(context.Context, *app.App, Request) (Result, error)
}

func Run(ctx context.Context, a *app.App, args []string) int
```

Every parser returns a typed request:

```go
type UpRequest struct {
	ProjectRoot string
	Attach      bool
}

type ExecRequest struct {
	ProjectRoot string
	User        string
	TTY         bool
	Env         []string
	Argv        []string
}

type RequestApproveRequest struct {
	ProjectRoot string
	Candidate   domain.Digest
}
```

Commands never pass a partially parsed `flag.FlagSet` into the application.
Hidden `_sidebar`, `_state`, `_fleet`, and `_statusline` commands use the same
dispatcher and typed requests.

Global output modes:

- normal human output;
- `--json`, emitting versioned result objects;
- `--quiet`, suppressing nonessential output; and
- no color when stdout is not a terminal or `NO_COLOR` is set.

Only presentation code inspects terminal capability.

## Host layout and project discovery

### Layout

```go
type Layout struct {
	Root       string
	Bin        string
	Artifacts string
	State      string
	Projects   string
	Candidates string
	Broker     string
	Locks      string
	Staging    string
}

func NewLayout(home string) (Layout, error)
func (l Layout) Validate() error
func (l Layout) Ensure() error
```

`NewLayout` requires an absolute home path. Tests create layouts under
`t.TempDir`; packages never read `os.UserHomeDir` themselves.

### Project root

```go
type Root struct {
	Path     string
	Identity FileIdentity
}

type FileIdentity struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}

func Discover(start string) (Root, error)
func OpenRegistered(path string, want FileIdentity) (*os.File, error)
```

Discovery walks physical parents looking for `.vibe/vibe.yaml`. It returns a
canonical absolute path plus platform identity. The registry resolves nested
registrations deterministically and reports ambiguity instead of guessing.

## Project manifest

### Schema types

```go
type Manifest struct {
	Schema    int             `yaml:"schema"`
	Harness   string          `yaml:"harness"`
	Image     Image           `yaml:"image"`
	Runtime   Runtime         `yaml:"runtime"`
	Services  map[string]Sidecar `yaml:"services,omitempty"`
	Agent     Agent           `yaml:"agent"`
	EnvFile   string          `yaml:"env_file,omitempty"`
	Bootstrap Bootstrap       `yaml:"bootstrap"`
}

type Image struct {
	Base       string   `yaml:"base"`
	Agents     []AgentKind `yaml:"agents,omitempty"`
	Toolchains []Toolchain `yaml:"toolchains,omitempty"`
	Extension  bool     `yaml:"extension,omitempty"`
}

type Runtime struct {
	Ports   []Port      `yaml:"ports,omitempty"`
	Imports []Import    `yaml:"imports,omitempty"`
	Env     OrderedEnv  `yaml:"env,omitempty"`
}

type Import struct {
	Source   string `yaml:"source"`
	Target   string `yaml:"target"`
	Readonly bool   `yaml:"readonly"`
}

type Sidecar struct {
	Image   string     `yaml:"image"`
	Ports   []Port     `yaml:"ports,omitempty"`
	Env     OrderedEnv `yaml:"env,omitempty"`
	Volumes []VolumeRef `yaml:"volumes,omitempty"`
}
```

Enums implement `encoding.TextUnmarshaler`. Ports, image references, container
paths, environment keys, and service names have dedicated parsers.

YAML maps are converted into sorted slices before canonicalization. This avoids
map iteration affecting candidate digests or diagnostic output.

### Load pipeline

```go
func Load(r io.Reader, limits Limits) (*Document, error)

type Document struct {
	Manifest Manifest
	Source   []byte
	Index    SourceIndex
}

func (d *Document) Validate() []domain.FieldError
```

Pipeline:

1. bounded read;
2. UTF-8 and document-count checks;
3. parse into `yaml.Node`;
4. inspect structure and limits;
5. decode with known fields;
6. field validation;
7. return all independent diagnostics in source order.

`SourceIndex` maps schema paths to line/column locations without retaining
parser nodes beyond the load operation.

## Canonical plan

The compiled `model.Plan` is the central handoff between configuration and
execution:

```go
type Plan struct {
	Format        int
	Project       Project
	Artifact      Artifact
	Dev           Container
	Services      []Container
	Networks      []Network
	Volumes       []Volume
	Images        []Image
	Inputs        Inputs
	Extension     *Extension
	CanonicalHash domain.Digest
}

type Project struct {
	ID          domain.ProjectID
	Root        string
	DisplayName string
}

type Container struct {
	Name        string
	Image       ImageID
	User        string
	Command     []string
	Environment []Env
	Mounts      []Mount
	Ports       []PortBinding
	Labels      []Label
	Policy      ContainerPolicy
}

type Mount struct {
	Kind     MountKind
	Source   string
	Target   string
	ReadOnly bool
}

type ContainerPolicy struct {
	DropAllCapabilities bool
	NoNewPrivileges     bool
	ReadonlyRootFS      bool
	Network             NetworkMode
}
```

`model.Compile` receives only resolved inputs:

```go
type CompileInput struct {
	Project      registry.Record
	Artifact     store.Artifact
	Manifest     schema.Manifest
	Snapshot     snapshot.Result
	ImageDigests map[string]domain.Digest
}

func Compile(CompileInput) (Plan, []domain.FieldError)
func Validate(Plan) []domain.FieldError
func CanonicalJSON(Plan) ([]byte, error)
```

Compilation generates names, labels, required mounts, volumes, and defaults.
Validation runs again on the completed plan. `CanonicalHash` is calculated
with that field cleared.

## Immutable snapshots

```go
type Limits struct {
	MaxFiles      int
	MaxFileBytes  int64
	MaxTotalBytes int64
	MaxDepth      int
}

type Spec struct {
	ProjectRoot paths.Root
	Entries     []Entry
	Limits      Limits
}

type Entry struct {
	Source   string
	Dest     string
	Optional bool
}

type Result struct {
	Digest   domain.Digest
	Path     string
	Files    []File
	Bytes    int64
}

type Service struct {
	store *store.Store
	walk  Walker
}

func (s *Service) Create(context.Context, Spec) (Result, error)
```

`Walker` is internal except for platform implementations. It opens entries
relative to the root directory descriptor, copies into a staging directory,
and returns normalized file metadata. Merkle entries use:

```text
type NUL mode NUL size NUL relative-path NUL content-digest LF
```

Paths use slash separators in the digest on every platform. Results are
deterministic across Linux and macOS for identical files and modes.

The snapshot package creates content; the store package publishes and leases
it. This separation keeps filesystem traversal tests independent of store
locking.

## Store

### Immutable objects

```go
type ObjectKind string

const (
	ArtifactObject  ObjectKind = "artifact"
	CandidateObject ObjectKind = "candidate"
	SnapshotObject  ObjectKind = "snapshot"
)

type Object struct {
	Kind   ObjectKind
	Digest domain.Digest
	Path   string
}

func (s *Store) Publish(ctx context.Context, kind ObjectKind, staged string, digest domain.Digest) (Object, error)
func (s *Store) Open(ctx context.Context, kind ObjectKind, digest domain.Digest) (*Lease, error)
func (s *Store) Exists(ctx context.Context, kind ObjectKind, digest domain.Digest) (bool, error)
```

`Publish` accepts a staging directory created under the store. It verifies the
digest, syncs it, renames it once, and treats an identical existing object as
success. A conflicting existing object returns `ErrConflict`.

```go
type Lease struct {
	Object Object
	// unexported lock and open descriptors
}

func (l *Lease) Close() error
```

Callers hold leases through process execution, payload mount acceptance, or
candidate completion.

### Artifact metadata

```go
type ArtifactRecord struct {
	Format        int                      `json:"format"`
	Digest        domain.Digest            `json:"digest"`
	Version       string                   `json:"version"`
	Platform      domain.Platform          `json:"platform"`
	BinaryDigest  domain.Digest            `json:"binary_digest"`
	PayloadDigest domain.Digest            `json:"payload_digest"`
	Release       domain.ReleaseProvenance `json:"release"`
	InstalledAt   time.Time                `json:"installed_at"`
}
```

Shared release value types live in `internal/domain`; `store` does not import
`release`.

### Candidate metadata

```go
type CandidateRecord struct {
	Format       int
	Digest       domain.Digest
	ProjectID    domain.ProjectID
	Kind         CandidateKind
	Snapshot     domain.Digest
	Plan         domain.Digest
	Extension    *domain.Digest
	Images       []ResolvedImage
	CreatedAt    time.Time
	SourceRequest *domain.RequestID
}
```

The record and canonical plan are stored together under the candidate digest.

## Registry

```go
type Record struct {
	Format         int               `json:"format"`
	Revision       uint64            `json:"revision"`
	ID             domain.ProjectID  `json:"id"`
	Root           string            `json:"root"`
	RootIdentity   paths.FileIdentity `json:"root_identity"`
	DisplayName    string            `json:"display_name"`
	Artifact       domain.Digest     `json:"artifact"`
	ReleaseVersion string            `json:"release_version,omitempty"`
	Mode           Mode              `json:"mode"`
	Approved       *domain.Digest    `json:"approved_candidate,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

type Registry struct {
	dir   string
	locks lock.Locker
}

func (r *Registry) Create(ctx context.Context, NewRecord) (Record, error)
func (r *Registry) Get(ctx context.Context, id domain.ProjectID) (Record, error)
func (r *Registry) Resolve(ctx context.Context, root paths.Root) (Record, error)
func (r *Registry) Update(ctx context.Context, id domain.ProjectID, revision uint64, fn func(*Record) error) (Record, error)
func (r *Registry) Delete(ctx context.Context, id domain.ProjectID, revision uint64) error
func (r *Registry) List(ctx context.Context) ([]Record, error)
```

JSON writes are deterministic and end with one newline. Unknown record fields
are rejected so data upgrades are explicit. Each format version has a decoder;
v2 starts at format 1 and has no v1 import path.

## Docker API adapter

The rest of the engine does not import Docker SDK packages.

```go
type Client interface {
	Ping(context.Context) error
	ResolveImage(context.Context, ImageRef) (ResolvedImage, error)
	PullImage(context.Context, ResolvedImage, ProgressSink) error
	Build(context.Context, BuildRequest, ProgressSink) (BuiltImage, error)

	InspectContainer(context.Context, ContainerName) (ContainerState, error)
	CreateContainer(context.Context, CreateRequest) (ContainerID, error)
	StartContainer(context.Context, ContainerID) error
	StopContainer(context.Context, ContainerID, time.Duration) error
	RemoveContainer(context.Context, ContainerID, RemoveOptions) error
	ListProjectContainers(context.Context, domain.ProjectID) ([]ContainerState, error)

	Exec(context.Context, ExecRequest) (ExecResult, error)
	Attach(context.Context, AttachRequest) error

	EnsureVolume(context.Context, VolumeSpec) error
	RemoveVolume(context.Context, VolumeName) error
	EnsureNetwork(context.Context, NetworkSpec) error
	RemoveNetwork(context.Context, NetworkName) error
}
```

`CreateRequest` mirrors the canonical model using engine-owned types. The SDK
adapter performs one translation and rejects unsupported fields. It never
accepts `map[string]any`.

Interactive attach uses an explicit stream interface:

```go
type Streams struct {
	In     io.Reader
	Out    io.Writer
	Err    io.Writer
	Resize <-chan WindowSize
}
```

The fake client records requests and supports scripted results:

```go
fake := dockerfake.New()
fake.ResolveImageResult = ...
fake.InspectResults["vibe-..."] = ...
```

Tests assert full request equality, not selected fields.

## Runtime reconciliation

`runtime.Service` turns a candidate plan into Docker state:

```go
type Service struct {
	docker dockerapi.Client
	store  *store.Store
	locks  lock.Locker
}

func (s *Service) Up(context.Context, Candidate, UpOptions) (State, error)
func (s *Service) Build(context.Context, Candidate, BuildOptions) (BuildResult, error)
func (s *Service) Rebuild(context.Context, Candidate, RebuildOptions) (State, error)
func (s *Service) Down(context.Context, registry.Record, DownOptions) error
func (s *Service) Status(context.Context, registry.Record) (State, error)
```

Each managed Docker object carries labels:

```text
dev.vibe.managed=true
dev.vibe.project=<project-id>
dev.vibe.candidate=sha256:<digest>
dev.vibe.artifact=sha256:<digest>
dev.vibe.role=dev|sidecar:<name>
```

Reconcile order:

1. acquire project operation lock;
2. lease artifact and candidate;
3. resolve/pull required images;
4. ensure generated volumes and network;
5. build extension image when present;
6. compare existing objects by labels and normalized specification;
7. replace changed containers in dependency order;
8. start sidecars, then dev container;
9. run post-create only for a newly created dev container;
10. run post-start after each actual start;
11. update the approved candidate revision;
12. release leases and lock.

Failures return a `Result` containing completed stages and preserve enough
state for the next run to reconcile. Rollback removes only objects created by
the failed transaction; it never removes pre-existing working objects unless
replacement had already been confirmed.

## Candidate preparation

Candidate creation is shared by `up`, `rebuild`, and broker requests:

```go
type CandidateService struct {
	snapshots *snapshot.Service
	registry  *registry.Registry
	store     *store.Store
	release   ImageResolver
}

func (s *CandidateService) Prepare(ctx context.Context, project registry.Record, opts PrepareOptions) (Candidate, error)
```

Pipeline:

1. snapshot manifest, selected env file, and declared imports;
2. load and validate manifest from the snapshot;
3. resolve all image references to digests;
4. assemble extension candidate when enabled;
5. compile and validate canonical plan;
6. write canonical plan and candidate metadata to staging;
7. derive candidate digest;
8. publish candidate;
9. return a leased candidate.

No later stage rereads workspace paths.

## Extension builder

```go
type Candidate struct {
	Digest       domain.Digest
	Dockerfile   string
	Context      snapshot.Result
	BaseImage    dockerapi.ResolvedImage
	Frontend     dockerapi.ResolvedImage
	Platform     domain.Platform
	Network      NetworkPolicy
	BuildOptions []Option
}

type Service struct {
	docker dockerapi.Client
	prompt terminal.Prompt
}

func (s *Service) Approve(ctx context.Context, Candidate, Approval) error
func (s *Service) Build(ctx context.Context, Candidate, ProgressSink) (dockerapi.BuiltImage, error)
```

Dockerfile parsing has a deliberately small purpose: enforce the supported
frontend, confirm the final user contract, and reject unsupported directives.
It is not a general Dockerfile interpreter. The actual build always uses the
frozen candidate context and recorded build options.

Build progress is normalized into structured events consumed by human output,
JSON output, or tmux state:

```go
type Progress struct {
	Stage   string
	Current int64
	Total   int64
	Message string
	Done    bool
}
```

## Release acquisition

```go
type Source interface {
	Resolve(ctx context.Context, version string, platform domain.Platform) (Descriptor, error)
	OpenArtifact(ctx context.Context, Descriptor) (io.ReadCloser, error)
	OpenAttestation(ctx context.Context, Descriptor) (io.ReadCloser, error)
}

type Verifier interface {
	Verify(ctx context.Context, Descriptor, artifact io.Reader, attestation io.Reader) (Verified, error)
}

type Service struct {
	source   Source
	verify   Verifier
	store    *store.Store
	payload  payload.Extractor
	platform domain.Platform
}

func (s *Service) Acquire(ctx context.Context, version string) (store.Artifact, error)
```

Acquisition streams the artifact to host-owned staging while hashing, verifies
release metadata, extracts only known archive entry types, validates the
embedded payload manifest, writes the artifact record, then publishes by output
digest.

Network clients use explicit timeouts, redirect policy, body limits, and a
fixed user agent. Tests use `httptest.Server` and in-memory `Source` fakes.

## Embedded payload and templates

`assets.go` at repository root embeds:

```go
//go:embed payload/**
var payloadFS embed.FS
```

`payload.Bundle` exposes logical resources rather than the raw filesystem:

```go
type Bundle interface {
	Manifest() Manifest
	Extract(ctx context.Context, destination string) (domain.Digest, error)
	Preset(name string) (Preset, error)
	Names() []string
}
```

The payload manifest is generated during release builds and checked into the
release artifact. Development builds can generate it through `go generate`,
but CI fails if regeneration changes tracked output.

Template rendering is typed. Presets supply values to a fixed template set;
they cannot choose output paths outside `.vibe/`.

## Project initialization

```go
type Request struct {
	Root       paths.Root
	Preset     string
	Artifact   store.Artifact
	Interactive bool
	StartTUI   bool
}

type Service struct {
	payload  payload.Bundle
	registry *registry.Registry
	runtime  RuntimeStarter
}

func (s *Service) Init(context.Context, Request) (Result, error)
```

Transaction:

1. require a directory that is not already registered;
2. resolve preset and render into a host staging directory;
3. show the file plan for interactive initialization;
4. atomically create `.vibe` without overwriting it;
5. create the registry record;
6. prepare the first candidate;
7. start the project;
8. optionally enter TUI.

If a later step fails, `.vibe` and the registry remain so the user can inspect
and retry. The command reports the exact recovery command.

## Rebuild broker

### Records

```go
type Request struct {
	Format  int              `json:"format"`
	ID      domain.RequestID `json:"id"`
	Kind    Kind             `json:"kind"`
	Reason  string           `json:"reason"`
	Summary string           `json:"summary"`
}

type Result struct {
	Format     int                 `json:"format"`
	RequestID  domain.RequestID    `json:"request_id"`
	ProjectID  domain.ProjectID    `json:"project_id"`
	Candidate  *domain.Digest      `json:"candidate,omitempty"`
	Status     Status              `json:"status"`
	Message    string              `json:"message,omitempty"`
	CreatedAt  time.Time           `json:"created_at"`
	CompletedAt *time.Time         `json:"completed_at,omitempty"`
}
```

### Service

```go
type Service struct {
	poller     *Poller
	candidates *CandidateService
	runtime    *runtime.Service
	results    ResultStore
	prompt     terminal.Prompt
}

func (s *Service) Poll(context.Context, registry.Record) ([]Pending, error)
func (s *Service) Show(context.Context, domain.ProjectID, domain.RequestID) (View, error)
func (s *Service) Approve(context.Context, domain.ProjectID, domain.Digest) (Result, error)
func (s *Service) Reject(context.Context, domain.ProjectID, domain.Digest, string) (Result, error)
```

Polling uses bounded `ReadDir` batches, ignores entries already recorded by ID
and content digest, and creates the immutable candidate before returning
`Pending`. The pending record binds request ID to candidate digest.

Approval accepts the digest, not a workspace request filename. Results are
stored under the project broker directory and rendered into the container
through its read-only result mount.

## Terminal presentation

```go
type Prompt interface {
	Confirm(ctx context.Context, Confirmation) (bool, error)
	Select(ctx context.Context, Selection) (int, error)
}

type Confirmation struct {
	Title   string
	Digest  domain.Digest
	Chrome  []Line
	Content []EncodedLine
}

func Encode(string, Limits) Encoded
func Diff(old, next io.Reader, Limits) (DiffResult, error)
```

Encoding replaces control characters with visible notation, normalizes line
endings, bounds width and line count, and returns truncation metadata. Views
keep labels/chrome separate from encoded content so callers cannot accidentally
render arbitrary text as interface chrome.

## Tmux and TUI

### Typed tmux client

```go
type Client interface {
	EnsureServer(context.Context, ServerSpec) error
	EnsureSession(context.Context, SessionSpec) error
	KillSession(context.Context, SessionID) error
	Attach(context.Context, SessionID, AttachOptions) error
	DisplayPopup(context.Context, PopupSpec) error
	SetOption(context.Context, Scope, string, string) error
	ListSessions(context.Context) ([]Session, error)
}
```

The implementation invokes an absolute tmux binary with argv. Session IDs are
derived from project IDs, never display names.

### Renderers

Private commands emit small stable protocols:

- `_sidebar --project ID`: plain tmux-formatted lines;
- `_state --project ID`: one compact state token and attention count;
- `_fleet --format lines|json`: registered project summaries;
- `_statusline --project ID --agent NAME`: one sanitized status line.

Renderer pipeline:

1. load registry record;
2. inspect runtime state;
3. inspect pending broker records;
4. build a view model;
5. render with a width budget.

Views are pure functions and golden-tested. Docker, registry, and tmux calls do
not occur inside render functions.

## Agent commands

`app.Agent`, `Run`, `Exec`, `Shell`, and `Attach` share:

```go
type ContainerCommand struct {
	Project domain.ProjectID
	User    string
	Workdir string
	Env     []envfile.Entry
	Argv    []string
	TTY     bool
	Stdin   bool
}
```

`run` and `agent` may load the snapshotted project env file, then pass entries
as Docker exec fields. `exec` uses only explicit `--env` arguments. `shell`
selects the first available shell from a fixed list by probing inside the
container. No command accepts a single shell command string; `vibe run --`
preserves argv exactly.

Window resize forwarding is implemented once in `dockerapi.Attach`, with
platform-specific terminal sizing behind a small interface.

## Doctor

```go
type Check interface {
	Name() string
	Run(context.Context, Input) Result
}

type Result struct {
	Name     string
	Status   Status
	Summary  string
	Details  []string
	Duration time.Duration
}
```

Checks are grouped:

- host layout and writable state;
- registered project identity;
- selected artifact and payload;
- Docker endpoint and API version;
- image, container, mount, volume, labels, and runtime policy;
- lifecycle marker and required container commands;
- tmux server/session; and
- broker/result mounts.

Independent checks run with bounded concurrency. Output order remains registry
order, not completion order. `--json` returns the same report model.

## Dev workflow

```go
type Record struct {
	Format       int
	ProjectID    domain.ProjectID
	Source       domain.Digest
	Builder      domain.Digest
	Dependencies domain.Digest
	Target       domain.Platform
	Output       domain.Digest
	BuiltAt      time.Time
}

type Service struct {
	snapshots *snapshot.Service
	builder   BuildRunner
	store     *store.Store
	registry  *registry.Registry
	prompt    terminal.Prompt
}

func (s *Service) On(context.Context, domain.ProjectID) (Record, error)
func (s *Service) Sync(context.Context, domain.ProjectID) (Record, error)
func (s *Service) Off(context.Context, domain.ProjectID) error
func (s *Service) Status(context.Context, domain.ProjectID) (Status, error)
```

The source allowlist is a tracked file in this repository, for example
`build/dev-sources.txt`. It includes Go sources, module files, payload,
templates, and release metadata while excluding `.git`, `.vibe`, build output,
and scratch files.

Dev builds run in a recorded builder image with a read-only source snapshot and
dedicated output directory. The service hashes the produced binary before
publishing it and updates only the requesting project's record.

## Concurrency and cancellation

Lock order is fixed:

```text
store-global → artifact/candidate → project → broker-request
```

Code never acquires a lock earlier in the order while holding a later one.
Lock helpers attach acquisition sites to timeout errors.

Long operations:

- honor context cancellation;
- use child timeouts for network calls and Docker API operations;
- emit structured progress;
- leave staging directories marked for later cleanup; and
- do not update mutable records until the durable object they reference exists.

`app` serializes lifecycle mutations per project. Read-only status and rendering
use shared leases and may proceed concurrently.

## Time, randomness, and filesystem seams

Use minimal injectable interfaces:

```go
type Clock interface {
	Now() time.Time
}

type Random interface {
	Read([]byte) (int, error)
}
```

Do not build a general virtual filesystem abstraction. Filesystem-heavy tests
use real temporary directories; only platform path walking and failure
injection receive interfaces.

Records store UTC timestamps with RFC3339 nanosecond formatting. Tests use a
fixed clock. IDs use cryptographic randomness and canonical lowercase encoding.

## Logging and progress

Application methods return results; they do not print. A lightweight event
sink carries progress:

```go
type Event struct {
	Time      time.Time
	ProjectID domain.ProjectID
	Operation string
	Stage     string
	Message   string
	Current   int64
	Total     int64
}

type Sink interface {
	Emit(context.Context, Event)
}
```

CLI sinks render interactive progress, JSON lines, or nothing. Logs contain
opaque IDs and normalized paths; environment values and agent output are not
logged.

## Configuration and compatibility

There is one project schema version and one record format version at initial
release. Version handling is explicit:

- schema version mismatch returns a diagnostic with the supported range;
- persistent records dispatch to a decoder by `format`;
- canonical plan format increments only when its serialized meaning changes;
- private tmux output protocols carry a leading protocol version where
  machine-parsed; and
- Docker API minimum version is checked at startup.

There is no code to import v1 layouts.

## Testing strategy

### Unit tests

Every package uses table tests for validation and error classification.
Especially:

- `schema`: malformed YAML, limits, diagnostics, enum and path cases;
- `model`: deterministic canonical JSON, mount targets, names, labels, ports;
- `envfile`: literal parsing and duplicate/limit cases;
- `snapshot`: path types, races, limits, deterministic digests;
- `store`: publish idempotence, conflicts, leases, interrupted staging, GC;
- `registry`: revision conflicts, discovery, deterministic fleet sorting;
- `broker`: polling limits, request replacement, candidate binding, results;
- `terminal`: control characters, bidi, invalid UTF-8, width/line truncation;
- `tmuxui`: golden views at multiple widths; and
- `runtime`: full request equality against the Docker fake.

### Integration tests

- Docker SDK adapter against a real local daemon.
- Artifact acquisition through local HTTP fixtures.
- Linux and macOS snapshot walker suites.
- tmux client against an isolated socket and temporary config.
- payload extraction and preset initialization in a scratch repository.

### End-to-end scenarios

1. `init → up → doctor → agent/exec → rebuild → down`.
2. Sidecar and loopback port lifecycle.
3. Imported data changes create a new candidate.
4. Extension candidate approval and build.
5. Broker request → candidate → approve/reject → result mount.
6. Two projects run concurrently with independent state.
7. Release update moves one project while another retains its artifact.
8. Dev on/sync/off never affects a release-mode project.
9. Interrupted build and interrupted update recover on retry.
10. Linux amd64, Linux arm64, and macOS arm64 release smoke tests.

### Fuzzing

Fuzz targets:

- YAML node inspection and schema loading;
- env file parser;
- digest and ID parsers;
- request JSON parser;
- terminal encoder and diff;
- container target/path normalizers; and
- archive/payload entry validation.

Fuzz tests assert bounded completion where practical, no panics, and stable
error classification.

## Dependency choices

Expected direct dependencies:

```text
gopkg.in/yaml.v3                  YAML node parsing
github.com/docker/docker/client  Docker API
github.com/docker/docker/api     Docker request/response types
golang.org/x/sys/unix            openat2, flock, terminal/platform calls
```

Provenance verification may add official Sigstore libraries after the M0
prototype selects the smallest maintained set. ULID is unnecessary unless
interoperability requires it; random project/request IDs can use a small local
base32 implementation.

Pin every direct dependency in `go.mod`; commit `go.sum`; run
`go mod verify`, `go vet`, tests, and the configured linter in CI. Keep
`CGO_ENABLED=0` for all release targets.

## Implementation slices

Each slice must compile, test, and expose one usable seam.

### Slice 1 — foundations

- Replace skeleton `main` with CLI/app wiring.
- Add `domain`, `paths`, `lock`, `runner`, `version`.
- Establish errors, IDs, layout, project discovery, and host command runner.
- Add unit tests on Linux and macOS build targets.

### Slice 2 — manifest and candidates

- Add bounded YAML parser, schema types, env parser, canonical model.
- Add snapshot walker and immutable candidate store.
- Implement `vibe config` as canonical JSON.
- Golden-test minimal and sidecar manifests.

### Slice 3 — Docker lifecycle

- Add Docker API interface, SDK adapter, and fake.
- Add image resolution, volumes, networks, containers, exec, attach.
- Implement `up`, `build`, `rebuild`, `down`, `status`, `exec`, `run`,
  `shell`, and `attach`.
- Add real-daemon lifecycle test.

### Slice 4 — artifacts and distribution

- Add embedded payload extraction and artifact records.
- Implement native release acquisition and verification.
- Add shim handoff, `update`, and `provision`.
- Produce and exercise the three-platform release matrix.

### Slice 5 — init and doctor

- Add typed presets and transactional `.vibe` creation.
- Implement registry/fleet CRUD and doctor checks.
- Implement `init`, `ps`, `bootstrap`, and project forget.

### Slice 6 — tmux UI

- Add typed tmux client and pure view models.
- Implement `tui`, `_sidebar`, `_state`, `_fleet`, `_statusline`, palette,
  clip/show/review integration, and agent session restoration.

### Slice 7 — broker and extension builds

- Add request poller, immutable pending records, terminal presentation, and
  approve/reject/status commands.
- Add extension candidate assembly and BuildKit orchestration.
- Exercise full request-to-rebuild flow.

### Slice 8 — dev mode and cutover

- Add source allowlist, dev builder, and project-scoped dev records.
- Implement `dev on/sync/off/status`.
- Dogfood v2, remove superseded host Bash and placeholder Go packages, update
  reference docs, then cut the first v2 release.

## Definition of done for a component

A component is complete when:

- its public types and interfaces match an actual caller;
- domain behavior has unit tests, including failure and cancellation paths;
- persistent output is deterministic and versioned;
- filesystem mutations are atomic and recoverable;
- external calls are behind a fakeable interface;
- human and JSON output are derived from the same result model;
- Linux amd64, Linux arm64, and macOS arm64 compile;
- `go test ./...`, `go vet ./...`, formatting, lint, and relevant integration
  tests pass; and
- the command-level documentation is updated with the implementation.

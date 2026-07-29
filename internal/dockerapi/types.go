// Package dockerapi isolates the Docker Engine API behind a narrow,
// engine-owned interface. No other package imports Docker SDK types;
// the SDK adapter in sdk*.go performs exactly one translation and
// rejects anything the engine model cannot express.
package dockerapi

import (
	"io"
	"time"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
)

// ImageRef is an image reference (name[:tag][@sha256:...]) already
// shape-validated by the schema layer.
type ImageRef string

// ResolvedImage pins a reference to the digest the engine will run.
type ResolvedImage struct {
	Ref    ImageRef
	Digest domain.Digest // registry manifest digest when known, else local image ID digest
}

type ContainerName string

type ContainerID string

// ContainerState is the engine's view of one existing container.
type ContainerState struct {
	ID       ContainerID
	Name     ContainerName
	Image    string
	Running  bool
	ExitCode int
	Labels   map[string]string
}

// MountKind mirrors model.MountKind without importing it.
type MountKind string

const (
	BindMount   MountKind = "bind"
	VolumeMount MountKind = "volume"
	TmpfsMount  MountKind = "tmpfs"
)

type Mount struct {
	Kind     MountKind
	Source   string
	Target   string
	ReadOnly bool
}

type PortBinding struct {
	HostIP        string
	HostPort      int
	ContainerPort int
}

// Policy is the closed security posture applied to every container.
type Policy struct {
	DropAllCapabilities bool
	NoNewPrivileges     bool
	ReadonlyRootFS      bool
}

// CreateRequest mirrors the canonical model with engine-owned types.
// Everything is explicit; the adapter never accepts free-form maps.
type CreateRequest struct {
	Name    ContainerName
	Image   string // reference to run, digest-pinned when resolved
	User    string
	Command []string
	Env     []string // KEY=VALUE in engine-defined order
	Labels  map[string]string
	Mounts  []Mount
	Ports   []PortBinding
	Workdir string
	Network string // project network name, DefaultNetwork, or "" (no network)
	Policy  Policy
}

type RemoveOptions struct {
	Force         bool
	RemoveVolumes bool
}

// WindowSize is a terminal geometry update.
type WindowSize struct {
	Width  int
	Height int
}

// Streams carries interactive I/O for exec and attach. Resize may be
// nil for non-TTY operations.
type Streams struct {
	In     io.Reader
	Out    io.Writer
	Err    io.Writer
	Resize <-chan WindowSize
}

type ExecRequest struct {
	Container ContainerName
	User      string
	WorkDir   string
	Env       []string // KEY=VALUE, explicit only
	Argv      []string
	TTY       bool
	Stdin     bool
	Streams   Streams
}

type ExecResult struct {
	ExitCode int
}

type AttachRequest struct {
	Container ContainerName
	TTY       bool
	Streams   Streams
}

// LogsRequest streams a container's log history (and optionally its
// future) into Streams. Engine containers are created without a TTY, so
// output always demultiplexes into Out and Err.
type LogsRequest struct {
	Container ContainerName
	Follow    bool
	Tail      int // last N lines; 0 means everything
	Streams   Streams
}

type VolumeSpec struct {
	Name   string
	Labels map[string]string
}

type VolumeName string

type NetworkSpec struct {
	Name   string
	Labels map[string]string
}

type NetworkName string

// BuildRequest is the extension-build contract.
type BuildRequest struct {
	Tag        string
	ContextDir string // frozen candidate context on the host
	Dockerfile string // path within the context
	BuildArgs  map[string]string
}

type BuiltImage struct {
	Ref    ImageRef
	Digest domain.Digest
}

// Progress is one normalized progress event from pulls and builds.
//
// Stages: "pull" (Message/Current/Total tick, Done closes), "build"
// (the classified classic-builder stream below), "build-start"
// (Message = tag, opens a build), "build-plan" (one BoM row announced
// by the app layer before a tools build: Part/Detail/Refresh, Total =
// the row's step count), and "dev-build" (a status line).
//
// Build classification: Step/Steps mirror the daemon's "Step N/M"
// ordinals. StepStart marks the transition line (Message = the
// instruction text); Cached is the daemon's cache verdict for the
// current step; Failed carries the daemon's error (Message) before the
// stream aborts. A plain event with Step > 0 is one raw line of layer
// output. Part is stamped by the app layer on tools builds only.
type Progress struct {
	Stage   string
	Message string
	Current int64
	Total   int64
	Done    bool

	Part      string
	Detail    string
	Refresh   bool
	Step      int
	Steps     int
	StepStart bool
	Cached    bool
	Failed    bool
}

// Progress stages.
const (
	StagePull       = "pull"
	StageBuild      = "build"
	StageBuildStart = "build-start"
	StageBuildPlan  = "build-plan"
)

// ProgressSink receives progress events; nil means discard.
type ProgressSink func(Progress)

// Emit sends one event, tolerating a nil sink.
func (s ProgressSink) Emit(p Progress) {
	if s != nil {
		s(p)
	}
}

// StopTimeout is the default grace period for container stops.
const StopTimeout = 10 * time.Second

// DefaultNetwork selects the daemon's default bridge network (used by
// the dev builder for module downloads).
const DefaultNetwork = "bridge"

// Package model compiles a validated manifest plus resolved inputs into
// the canonical engine Plan: the single handoff between configuration
// and execution. It knows desired runtime semantics but no Docker SDK
// types and no YAML.
package model

import (
	"bytes"
	"encoding/json"
	"fmt"

	"vibe/internal/domain"
)

// PlanFormat increments only when the serialized meaning of the plan
// changes.
const PlanFormat = 1

// Plan is the complete desired state for one project.
type Plan struct {
	Format        int           `json:"format"`
	Project       Project       `json:"project"`
	Artifact      Artifact      `json:"artifact"`
	Dev           Container     `json:"dev"`
	Services      []Container   `json:"services"`
	Networks      []Network     `json:"networks"`
	Volumes       []Volume      `json:"volumes"`
	Images        []ImageID     `json:"images"`
	Inputs        Inputs        `json:"inputs"`
	Tools         *Tools        `json:"tools,omitempty"`
	Extension     *Extension    `json:"extension,omitempty"`
	CanonicalHash domain.Digest `json:"canonical_hash,omitzero"`
}

type Project struct {
	ID          domain.ProjectID `json:"id"`
	Root        string           `json:"root"`
	DisplayName string           `json:"display_name"`
}

// Artifact identifies the engine artifact the plan was compiled
// against; zero digests mean no artifact is installed yet.
type Artifact struct {
	Digest  domain.Digest `json:"digest,omitzero"`
	Version string        `json:"version,omitempty"`
}

type Container struct {
	Name        string        `json:"name"`
	Image       ImageID       `json:"image"`
	User        string        `json:"user,omitempty"`
	Command     []string      `json:"command,omitempty"`
	Environment []Env         `json:"environment,omitempty"`
	Mounts      []Mount       `json:"mounts,omitempty"`
	Ports       []PortBinding `json:"ports,omitempty"`
	Labels      []Label       `json:"labels,omitempty"`
	// DNSFromService names the plan service whose runtime address
	// becomes this container's resolver. The plan carries the semantic
	// link only — the concrete IP is runtime state and must never enter
	// the canonical hash.
	DNSFromService string `json:"dns_from_service,omitempty"`
	// Log bounds the container's json-file log; nil means the daemon
	// default.
	Log    *LogPolicy      `json:"log,omitempty"`
	Policy ContainerPolicy `json:"policy"`
}

// LogPolicy is a json-file rotation bound for containers whose stdout
// is an engine read surface (the dns ledger) rather than debris.
type LogPolicy struct {
	MaxSize  string `json:"max_size"`
	MaxFiles int    `json:"max_files"`
}

// ImageID names an image by reference plus, when resolved, its registry
// digest. Execution requires the digest; compilation may leave it zero
// before resolution.
type ImageID struct {
	Ref    string        `json:"ref"`
	Digest domain.Digest `json:"digest,omitzero"`
}

type Env struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type MountKind string

const (
	BindMount   MountKind = "bind"
	VolumeMount MountKind = "volume"
	TmpfsMount  MountKind = "tmpfs"
)

type Mount struct {
	Kind     MountKind `json:"kind"`
	Source   string    `json:"source"`
	Target   string    `json:"target"`
	ReadOnly bool      `json:"read_only,omitempty"`
}

type PortBinding struct {
	HostIP        string `json:"host_ip"`
	HostPort      int    `json:"host_port"`
	ContainerPort int    `json:"container_port"`
}

type Label struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type NetworkMode string

// NetworkProject attaches the container to the project network; it is
// the only mode the compiler produces.
const NetworkProject NetworkMode = "project"

// ContainerPolicy is the closed security posture every container gets.
type ContainerPolicy struct {
	DropAllCapabilities bool        `json:"drop_all_capabilities"`
	NoNewPrivileges     bool        `json:"no_new_privileges"`
	ReadonlyRootFS      bool        `json:"readonly_root_fs"`
	Network             NetworkMode `json:"network"`
	// NetBindService re-grants exactly CAP_NET_BIND_SERVICE on top of
	// the dropped set. The one legitimate holder is the engine's dns
	// forwarder (Validate enforces this): the official CoreDNS binary
	// carries file capabilities, and a binary whose file caps exceed
	// the process's permitted set fails exec under no-new-privileges.
	// No schema surface can express it.
	NetBindService bool `json:"net_bind_service,omitempty"`
}

type Network struct {
	Name string `json:"name"`
}

type Volume struct {
	Name string `json:"name"`
}

// Inputs records the frozen inputs the plan was compiled from.
type Inputs struct {
	Snapshot domain.Digest `json:"snapshot,omitzero"`
	EnvFile  string        `json:"env_file,omitempty"`
}

// Tools marks an engine-generated agent/toolchain install image. The
// lists are sorted at compile time; the Dockerfile is engine-authored
// (internal/builder), never project input.
type Tools struct {
	Agents     []string `json:"agents,omitempty"`
	Toolchains []string `json:"toolchains,omitempty"`
}

// Extension marks an enabled image-extension build; the build candidate
// itself is assembled separately (slice 7).
type Extension struct {
	Dockerfile string `json:"dockerfile"` // slash-relative path inside the snapshot
}

// CanonicalJSON encodes the plan deterministically: fixed struct field
// order, sorted slices established at compile time, two-space indent,
// trailing newline.
func CanonicalJSON(p Plan) ([]byte, error) {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// DecodePlan is the strict decoder every plan reader must use: the
// plan is a persistent record (container policy, mounts, users), so it
// follows the record discipline — DisallowUnknownFields, format
// dispatch, then Validate. A lenient decode would let an older binary
// silently drop an additive policy field.
func DecodePlan(data []byte) (Plan, error) {
	var plan Plan
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&plan); err != nil {
		return Plan{}, fmt.Errorf("%w: plan: %v", domain.ErrInvalid, err)
	}
	if plan.Format != PlanFormat {
		return Plan{}, fmt.Errorf("%w: plan format %d, this engine speaks %d", domain.ErrInvalid, plan.Format, PlanFormat)
	}
	if ferrs := Validate(plan); len(ferrs) > 0 {
		return Plan{}, fmt.Errorf("%w: plan invalid: %v", domain.ErrInvalid, ferrs[0])
	}
	return plan, nil
}

// Hash computes the canonical digest of the plan with CanonicalHash
// cleared.
func Hash(p Plan) (domain.Digest, error) {
	p.CanonicalHash = domain.Digest{}
	data, err := CanonicalJSON(p)
	if err != nil {
		return domain.Digest{}, err
	}
	return domain.SHA256(data), nil
}

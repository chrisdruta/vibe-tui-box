// Package schema loads and validates the project manifest
// .vibe/vibe.yaml. It knows YAML and the manifest surface; it knows
// nothing about Docker. The pipeline is: bounded read → structural
// inspection of the yaml.Node graph → KnownFields decode → field
// validation with source positions.
package schema

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
)

// Manifest is the single project configuration document. Unknown keys
// are rejected at decode; unknown enum values are rejected by the enum
// types below.
type Manifest struct {
	Schema    int                `yaml:"schema"`
	Image     Image              `yaml:"image"`
	Runtime   Runtime            `yaml:"runtime"`
	Services  map[string]Sidecar `yaml:"services,omitempty"`
	Agent     Agent              `yaml:"agent"`
	EnvFile   string             `yaml:"env_file,omitempty"`
	Bootstrap Bootstrap          `yaml:"bootstrap"`
}

type Image struct {
	Base       string      `yaml:"base"`
	Agents     []AgentSpec `yaml:"agents,omitempty"`
	Toolchains []Toolchain `yaml:"toolchains,omitempty"`
	Extension  bool        `yaml:"extension,omitempty"`
}

type Runtime struct {
	Ports   []Port     `yaml:"ports,omitempty"`
	Imports []Import   `yaml:"imports,omitempty"`
	Env     OrderedEnv `yaml:"env,omitempty"`
}

type Import struct {
	Source   string `yaml:"source"`
	Target   string `yaml:"target"`
	Readonly bool   `yaml:"readonly"`
}

type Sidecar struct {
	Image   string      `yaml:"image"`
	Ports   []Port      `yaml:"ports,omitempty"`
	Env     OrderedEnv  `yaml:"env,omitempty"`
	Volumes []VolumeRef `yaml:"volumes,omitempty"`
}

// VolumeRef names an engine-generated volume mounted into a sidecar.
// The name is scoped to the project; external volumes cannot be named.
type VolumeRef struct {
	Name   string `yaml:"name"`
	Target string `yaml:"target"`
}

type Agent struct {
	Cmd    AgentKind  `yaml:"cmd"`
	Tmux   bool       `yaml:"tmux"`
	Memory MemoryMode `yaml:"memory,omitempty"`
}

type Bootstrap struct {
	Required []string `yaml:"required,omitempty"`
}

// AgentKind is the closed set of installable agent CLIs.
type AgentKind string

const (
	AgentClaude AgentKind = "claude"
	AgentCodex  AgentKind = "codex"
	AgentGrok   AgentKind = "grok"
)

func (a *AgentKind) UnmarshalText(b []byte) error {
	switch v := AgentKind(b); v {
	case AgentClaude, AgentCodex, AgentGrok:
		*a = v
		return nil
	default:
		return fmt.Errorf("%w: agent %q: known agents are claude, codex, grok", domain.ErrInvalid, b)
	}
}

// AgentSpec is one image.agents entry: an agent CLI, optionally
// qualified. "claude" tracks the installer's latest channel and is
// re-pulled on every `vibe rebuild`; "claude@2.1.220" (digit-leading =
// exact version) is frozen; "claude@stable" (word = a channel or npm
// dist-tag) selects that channel and refreshes like unversioned. grok
// takes no qualifier — its installer has no version parameter.
type AgentSpec struct {
	Kind    AgentKind
	Version string
}

func (a *AgentSpec) UnmarshalText(b []byte) error {
	name, version, pinned := strings.Cut(string(b), "@")
	if err := a.Kind.UnmarshalText([]byte(name)); err != nil {
		return err
	}
	if !pinned {
		a.Version = ""
		return nil
	}
	if a.Kind == AgentGrok {
		return fmt.Errorf("%w: agent %q: grok's installer cannot pin a version (drop the @)", domain.ErrInvalid, b)
	}
	if !validAgentVersion(version) {
		return fmt.Errorf("%w: agent %q: version must be 1-64 characters of [0-9A-Za-z._+-], starting alphanumeric", domain.ErrInvalid, b)
	}
	a.Version = version
	return nil
}

// String renders the manifest form; it is also the canonical-plan form,
// so unversioned entries stay byte-identical to the pre-version output.
func (a AgentSpec) String() string {
	if a.Version == "" {
		return string(a.Kind)
	}
	return string(a.Kind) + "@" + a.Version
}

// validAgentVersion bounds the charset an agent version may carry: the
// value lands inside a generated Dockerfile RUN line, so nothing with
// shell meaning ($, quotes, backslash, whitespace) is representable.
func validAgentVersion(v string) bool {
	if len(v) == 0 || len(v) > 64 {
		return false
	}
	for i, c := range v {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case (c == '.' || c == '-' || c == '_' || c == '+') && i > 0:
		default:
			return false
		}
	}
	return true
}

// MemoryMode selects the agent CLI's cross-session auto memory. The
// zero value (absent) means off — in a hardened container, silent
// cross-session knowledge accumulation is opt-in, not inherited from
// the agent CLI's upstream default.
type MemoryMode string

const (
	MemoryOff  MemoryMode = "off"
	MemoryAuto MemoryMode = "auto"
)

func (m *MemoryMode) UnmarshalText(b []byte) error {
	switch v := MemoryMode(b); v {
	case MemoryOff, MemoryAuto:
		*m = v
		return nil
	default:
		return fmt.Errorf("%w: agent.memory %q: known modes are auto, off", domain.ErrInvalid, b)
	}
}

// Toolchain is the closed set of installable toolchains.
type Toolchain string

const (
	ToolchainNode  Toolchain = "node"
	ToolchainBun   Toolchain = "bun"
	ToolchainGo    Toolchain = "go"
	ToolchainRokit Toolchain = "rokit"
)

func (t *Toolchain) UnmarshalText(b []byte) error {
	switch v := Toolchain(b); v {
	case ToolchainNode, ToolchainBun, ToolchainGo, ToolchainRokit:
		*t = v
		return nil
	default:
		return fmt.Errorf("%w: toolchain %q: known toolchains are node, bun, go, rokit", domain.ErrInvalid, b)
	}
}

// Port is the raw manifest port string ("127.0.0.1:34872:34872").
// Parsing and the loopback policy live in validate.go so all diagnostics
// carry source positions.
type Port string

// EnvVar is one manifest environment entry.
type EnvVar struct {
	Key   string
	Value string
}

// OrderedEnv decodes a YAML string map into a slice sorted by key, so
// map iteration order can never influence candidate digests or
// diagnostics.
type OrderedEnv []EnvVar

func (e *OrderedEnv) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("%w: env must be a mapping of string keys to string values", domain.ErrInvalid)
	}
	out := make(OrderedEnv, 0, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, val := node.Content[i], node.Content[i+1]
		if key.Kind != yaml.ScalarNode || val.Kind != yaml.ScalarNode {
			return fmt.Errorf("%w: env entries must be scalar key/value pairs", domain.ErrInvalid)
		}
		if val.Tag != "!!str" {
			return fmt.Errorf("%w: env value for %q must be a quoted string", domain.ErrInvalid, key.Value)
		}
		out = append(out, EnvVar{Key: key.Value, Value: val.Value})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	*e = out
	return nil
}

// ServiceNames returns sidecar names sorted for deterministic
// compilation and diagnostics.
func (m *Manifest) ServiceNames() []string {
	names := make([]string, 0, len(m.Services))
	for name := range m.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

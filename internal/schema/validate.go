package schema

import (
	"fmt"
	"net"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
)

// SupportedSchema is the only manifest schema version this engine
// accepts.
const SupportedSchema = 1

// PortBinding is a parsed, policy-checked published port.
type PortBinding struct {
	HostIP        string
	HostPort      int
	ContainerPort int
}

// ParsePort parses "ip:hostport:containerport" and enforces the
// loopback-only publishing policy.
func ParsePort(raw string) (PortBinding, error) {
	parts := strings.Split(raw, ":")
	if len(parts) != 3 {
		return PortBinding{}, fmt.Errorf("want <loopback-ip>:<host-port>:<container-port>, got %q", raw)
	}
	ip := net.ParseIP(parts[0])
	if ip == nil {
		return PortBinding{}, fmt.Errorf("%q is not an IP address", parts[0])
	}
	if !ip.IsLoopback() {
		return PortBinding{}, fmt.Errorf("published ports must bind a loopback address, got %q", parts[0])
	}
	host, err := parsePortNumber(parts[1])
	if err != nil {
		return PortBinding{}, fmt.Errorf("host port: %v", err)
	}
	container, err := parsePortNumber(parts[2])
	if err != nil {
		return PortBinding{}, fmt.Errorf("container port: %v", err)
	}
	return PortBinding{HostIP: ip.String(), HostPort: host, ContainerPort: container}, nil
}

func parsePortNumber(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 65535 {
		return 0, fmt.Errorf("%q is not a port number (1-65535)", s)
	}
	return n, nil
}

// ValidServiceName reports whether s is a legal sidecar service name;
// commands taking a service argument gate on it before deriving
// container names.
func ValidServiceName(s string) bool { return serviceNameRe.MatchString(s) }

var (
	serviceNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	volumeNameRe  = serviceNameRe // same closed shape, distinct diagnostics
	commandRe     = regexp.MustCompile(`^[a-z0-9][A-Za-z0-9._-]{0,63}$`)
	// imageRefRe accepts name[:tag][@sha256:hex] with optional registry
	// host. It is a shape check; digest pinning is enforced at candidate
	// resolution, not here.
	imageRefRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]*[a-z0-9])?(/[a-z0-9]([a-z0-9._-]*[a-z0-9])?)*(:[A-Za-z0-9._-]{1,128})?(@sha256:[a-f0-9]{64})?$`)
	envKeyRe   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
)

// reservedServiceNames cannot be used for sidecars: they collide with
// engine-generated container roles.
var reservedServiceNames = map[string]bool{"dev": true, "base": true, "vibe": true, "dns": true}

// Validate performs field-level validation and returns every
// independent diagnostic in source order.
func (d *Document) Validate() []domain.FieldError {
	var v validator
	v.index = d.Index
	m := &d.Manifest

	if m.Schema != SupportedSchema {
		v.add("schema", fmt.Sprintf("schema %d is not supported; this engine supports schema %d", m.Schema, SupportedSchema))
	}

	v.image(m)
	v.runtime(m)
	v.services(m)
	v.agent(m)

	if m.EnvFile != "" {
		v.relPath("env_file", m.EnvFile)
	}
	for i, cmd := range m.Bootstrap.Required {
		if !commandRe.MatchString(cmd) {
			v.add(fmt.Sprintf("bootstrap.required[%d]", i), fmt.Sprintf("%q is not a command name", cmd))
		}
	}

	return v.errs
}

type validator struct {
	index SourceIndex
	errs  []domain.FieldError
}

func (v *validator) add(path, msg string) {
	pos := v.index.Pos(path)
	v.errs = append(v.errs, domain.FieldError{Path: path, Line: pos.Line, Column: pos.Column, Message: msg})
}

func (v *validator) image(m *Manifest) {
	if m.Image.Base == "" {
		v.add("image.base", "base image is required")
	} else if !imageRefRe.MatchString(m.Image.Base) {
		v.add("image.base", fmt.Sprintf("%q is not an image reference (name[:tag][@sha256:...])", m.Image.Base))
	}
	// Duplicates key on the agent, not the pin: one CLI installs once,
	// so "claude" beside "claude@2.1.220" is a contradiction, not two
	// entries.
	seenAgents := map[AgentKind]bool{}
	for i, a := range m.Image.Agents {
		if seenAgents[a.Kind] {
			v.add(fmt.Sprintf("image.agents[%d]", i), fmt.Sprintf("duplicate agent %q", a.Kind))
		}
		seenAgents[a.Kind] = true
	}
	seenTools := map[Toolchain]bool{}
	for i, t := range m.Image.Toolchains {
		if seenTools[t] {
			v.add(fmt.Sprintf("image.toolchains[%d]", i), fmt.Sprintf("duplicate toolchain %q", t))
		}
		seenTools[t] = true
	}
}

func (v *validator) runtime(m *Manifest) {
	for i, p := range m.Runtime.Ports {
		if _, err := ParsePort(string(p)); err != nil {
			v.add(fmt.Sprintf("runtime.ports[%d]", i), err.Error())
		}
	}
	targets := map[string]string{}
	for i, imp := range m.Runtime.Imports {
		base := fmt.Sprintf("runtime.imports[%d]", i)
		v.relPath(base+".source", imp.Source)
		if tgt, ok := v.containerTarget(base+".target", imp.Target); ok {
			if prev, dup := targets[tgt]; dup {
				v.add(base+".target", fmt.Sprintf("target %q duplicates %s", tgt, prev))
			}
			targets[tgt] = base
		}
	}
	v.envKeys("runtime.env", m.Runtime.Env)
}

func (v *validator) services(m *Manifest) {
	for _, name := range m.ServiceNames() {
		svc := m.Services[name]
		base := "services." + name
		if !serviceNameRe.MatchString(name) {
			v.add(base, fmt.Sprintf("service name %q must match %s", name, serviceNameRe))
		} else if reservedServiceNames[name] {
			v.add(base, fmt.Sprintf("service name %q is reserved", name))
		}
		if svc.Image == "" {
			v.add(base+".image", "sidecar image is required")
		} else if !imageRefRe.MatchString(svc.Image) {
			v.add(base+".image", fmt.Sprintf("%q is not an image reference", svc.Image))
		}
		for i, p := range svc.Ports {
			if _, err := ParsePort(string(p)); err != nil {
				v.add(fmt.Sprintf("%s.ports[%d]", base, i), err.Error())
			}
		}
		v.envKeys(base+".env", svc.Env)
		for i, vol := range svc.Volumes {
			vbase := fmt.Sprintf("%s.volumes[%d]", base, i)
			if !volumeNameRe.MatchString(vol.Name) {
				v.add(vbase+".name", fmt.Sprintf("volume name %q must match %s", vol.Name, volumeNameRe))
			}
			v.containerTarget(vbase+".target", vol.Target)
		}
	}
}

func (v *validator) agent(m *Manifest) {
	if m.Agent.Cmd == "" {
		v.add("agent.cmd", "agent command is required")
		return
	}
	for _, a := range m.Image.Agents {
		if a.Kind == m.Agent.Cmd {
			return
		}
	}
	v.add("agent.cmd", fmt.Sprintf("agent %q is not listed in image.agents", m.Agent.Cmd))
}

func (v *validator) envKeys(base string, env OrderedEnv) {
	for _, e := range env {
		if !envKeyRe.MatchString(e.Key) {
			v.add(base+"."+e.Key, fmt.Sprintf("env key %q must match %s", e.Key, envKeyRe))
		}
	}
}

// relPath checks a workspace-relative path: non-empty, relative, clean,
// and confined to the workspace.
func (v *validator) relPath(fieldPath, p string) {
	switch {
	case p == "":
		v.add(fieldPath, "path is required")
	case strings.HasPrefix(p, "/"):
		v.add(fieldPath, fmt.Sprintf("%q must be workspace-relative", p))
	case path.Clean(p) != p || p == ".." || strings.HasPrefix(p, "../"):
		v.add(fieldPath, fmt.Sprintf("%q must be a clean relative path inside the workspace", p))
	case strings.Contains(p, "\\"):
		v.add(fieldPath, fmt.Sprintf("%q must use forward slashes", p))
	}
}

// containerTarget checks an absolute normalized in-container path and
// returns the cleaned form when valid.
func (v *validator) containerTarget(fieldPath, p string) (string, bool) {
	switch {
	case p == "":
		v.add(fieldPath, "target is required")
	case !strings.HasPrefix(p, "/"):
		v.add(fieldPath, fmt.Sprintf("target %q must be absolute", p))
	case path.Clean(p) != p || p == "/":
		v.add(fieldPath, fmt.Sprintf("target %q must be a normalized absolute path, not /", p))
	default:
		return p, true
	}
	return "", false
}

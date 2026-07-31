package model

import (
	"fmt"
	"net"
	"path"
	"regexp"
	"strings"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
)

// logMaxSizeRe is the daemon's json-file max-size shape.
var logMaxSizeRe = regexp.MustCompile(`^[1-9][0-9]*[kmg]$`)

// Validate re-checks the completed plan: this is the last gate before
// anything is persisted or handed to Docker, and it assumes nothing
// about how the plan was produced.
func Validate(p Plan) []domain.FieldError {
	var errs []domain.FieldError
	add := func(path, msg string) {
		errs = append(errs, domain.FieldError{Path: path, Message: msg})
	}

	if p.Format != PlanFormat {
		add("format", fmt.Sprintf("plan format %d (supported: %d)", p.Format, PlanFormat))
	}
	if p.Project.ID == "" {
		add("project.id", "project id is required")
	}
	if !strings.HasPrefix(p.Project.Root, "/") {
		add("project.root", fmt.Sprintf("root %q must be absolute", p.Project.Root))
	}

	names := map[string]string{}
	containers := append([]Container{p.Dev}, p.Services...)
	for _, c := range containers {
		base := "container " + c.Name
		if c.Name == "" {
			add(base, "container name is required")
		}
		if prev, dup := names[c.Name]; dup {
			add(base, "name collides with "+prev)
		}
		names[c.Name] = base
		if c.Image.Ref == "" {
			add(base+": image", "image reference is required")
		}
		if !c.Policy.DropAllCapabilities || !c.Policy.NoNewPrivileges {
			add(base+": policy", "closed container policy is mandatory")
		}
		if c.Policy.NetBindService && c.Name != SidecarContainerName(p.Project.ID, DNSServiceName) {
			add(base+": policy", "only the engine dns forwarder may re-grant NET_BIND_SERVICE")
		}
		if c.Log != nil {
			if !logMaxSizeRe.MatchString(c.Log.MaxSize) {
				add(base+": log", fmt.Sprintf("max size %q must be a positive count with a k/m/g suffix", c.Log.MaxSize))
			}
			if c.Log.MaxFiles < 1 {
				add(base+": log", fmt.Sprintf("max files %d must be at least 1", c.Log.MaxFiles))
			}
		}
		validateMounts(base, c.Mounts, add)
		for i, port := range c.Ports {
			ip := net.ParseIP(port.HostIP)
			if ip == nil || !ip.IsLoopback() {
				add(fmt.Sprintf("%s: ports[%d]", base, i), fmt.Sprintf("host ip %q is not loopback", port.HostIP))
			}
			if port.HostPort < 1 || port.HostPort > 65535 || port.ContainerPort < 1 || port.ContainerPort > 65535 {
				add(fmt.Sprintf("%s: ports[%d]", base, i), "port out of range")
			}
		}
	}
	for _, c := range containers {
		if c.DNSFromService == "" {
			continue
		}
		want := SidecarContainerName(p.Project.ID, c.DNSFromService)
		if _, ok := names[want]; !ok {
			add("container "+c.Name+": dns_from_service",
				fmt.Sprintf("service %q has no container %q in the plan", c.DNSFromService, want))
		}
	}
	return errs
}

// validateMounts enforces the target rules: absolute, normalized,
// unique, and no target may equal, contain, or be contained by another
// mount target of the same container.
func validateMounts(base string, mounts []Mount, add func(string, string)) {
	targets := make([]string, 0, len(mounts))
	for i, m := range mounts {
		field := fmt.Sprintf("%s: mounts[%d]", base, i)
		t := m.Target
		switch {
		case t == "" || !strings.HasPrefix(t, "/"):
			add(field, fmt.Sprintf("target %q must be absolute", t))
			continue
		case path.Clean(t) != t || t == "/":
			add(field, fmt.Sprintf("target %q must be normalized and not /", t))
			continue
		}
		switch m.Kind {
		case BindMount, VolumeMount:
			if m.Source == "" {
				add(field, "mount source is required")
			}
		case TmpfsMount:
			if m.Source != "" {
				add(field, fmt.Sprintf("tmpfs mount must not have a source, got %q", m.Source))
			}
		default:
			add(field, fmt.Sprintf("unsupported mount kind %q", m.Kind))
		}
		for _, prev := range targets {
			if t == prev || strings.HasPrefix(t, prev+"/") || strings.HasPrefix(prev, t+"/") {
				add(field, fmt.Sprintf("target %q overlaps mount target %q", t, prev))
			}
		}
		targets = append(targets, t)
	}
}

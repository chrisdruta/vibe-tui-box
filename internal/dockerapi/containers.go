package dockerapi

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/strslice"
	"github.com/docker/go-connections/nat"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
)

// projectLabel is duplicated from the runtime package to keep the
// dependency direction inward; runtime owns the full label vocabulary.
const projectLabel = "dev.vibe.project"

func (s *SDK) InspectContainer(ctx context.Context, name ContainerName) (ContainerState, error) {
	resp, err := s.cli.ContainerInspect(ctx, string(name))
	if err != nil {
		return ContainerState{}, mapErr("inspect container "+string(name), err)
	}
	state := ContainerState{
		ID:    ContainerID(resp.ID),
		Name:  ContainerName(strings.TrimPrefix(resp.Name, "/")),
		Image: resp.Config.Image,
	}
	if resp.State != nil {
		state.Running = resp.State.Running
		state.ExitCode = resp.State.ExitCode
	}
	if resp.Config != nil {
		state.Labels = resp.Config.Labels
	}
	return state, nil
}

func (s *SDK) CreateContainer(ctx context.Context, req CreateRequest) (ContainerID, error) {
	exposed := nat.PortSet{}
	bindings := nat.PortMap{}
	for _, p := range req.Ports {
		port, err := nat.NewPort("tcp", strconv.Itoa(p.ContainerPort))
		if err != nil {
			return "", fmt.Errorf("%w: container port %d: %v", domain.ErrInvalid, p.ContainerPort, err)
		}
		exposed[port] = struct{}{}
		bindings[port] = append(bindings[port], nat.PortBinding{
			HostIP:   p.HostIP,
			HostPort: strconv.Itoa(p.HostPort),
		})
	}

	mounts := make([]mount.Mount, 0, len(req.Mounts))
	for _, m := range req.Mounts {
		var kind mount.Type
		switch m.Kind {
		case BindMount:
			kind = mount.TypeBind
		case VolumeMount:
			kind = mount.TypeVolume
		default:
			return "", fmt.Errorf("%w: mount kind %q", domain.ErrInvalid, m.Kind)
		}
		mounts = append(mounts, mount.Mount{
			Type:     kind,
			Source:   m.Source,
			Target:   m.Target,
			ReadOnly: m.ReadOnly,
		})
	}

	if !req.Policy.DropAllCapabilities || !req.Policy.NoNewPrivileges {
		return "", fmt.Errorf("%w: closed container policy is mandatory", domain.ErrInvalid)
	}
	hostConfig := &container.HostConfig{
		Mounts:         mounts,
		PortBindings:   bindings,
		CapDrop:        strslice.StrSlice{"ALL"},
		SecurityOpt:    []string{"no-new-privileges:true"},
		ReadonlyRootfs: req.Policy.ReadonlyRootFS,
	}
	var netConfig *network.NetworkingConfig
	switch req.Network {
	case "":
		hostConfig.NetworkMode = "none"
	case DefaultNetwork:
		hostConfig.NetworkMode = "bridge"
	default:
		hostConfig.NetworkMode = container.NetworkMode(req.Network)
		netConfig = &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				req.Network: {Aliases: networkAliases(req)},
			},
		}
	}

	config := &container.Config{
		Image:        req.Image,
		User:         req.User,
		WorkingDir:   req.Workdir,
		Cmd:          strslice.StrSlice(req.Command),
		Env:          req.Env,
		Labels:       req.Labels,
		ExposedPorts: exposed,
	}

	resp, err := s.cli.ContainerCreate(ctx, config, hostConfig, netConfig, nil, string(req.Name))
	if err != nil {
		return "", mapErr("create container "+string(req.Name), err)
	}
	return ContainerID(resp.ID), nil
}

// networkAliases derives the sidecar's short DNS name from the
// engine-generated container name ("vibe-<id>-svc-db" → "db"), so
// workloads address services by their manifest names.
func networkAliases(req CreateRequest) []string {
	if role, ok := req.Labels["dev.vibe.role"]; ok {
		if service, found := strings.CutPrefix(role, "sidecar:"); found {
			return []string{service}
		}
	}
	return nil
}

func (s *SDK) StartContainer(ctx context.Context, id ContainerID) error {
	return mapErr("start container", s.cli.ContainerStart(ctx, string(id), container.StartOptions{}))
}

func (s *SDK) StopContainer(ctx context.Context, id ContainerID, timeout time.Duration) error {
	seconds := int(timeout.Seconds())
	return mapErr("stop container", s.cli.ContainerStop(ctx, string(id), container.StopOptions{Timeout: &seconds}))
}

func (s *SDK) RemoveContainer(ctx context.Context, id ContainerID, opts RemoveOptions) error {
	err := s.cli.ContainerRemove(ctx, string(id), container.RemoveOptions{
		Force:         opts.Force,
		RemoveVolumes: opts.RemoveVolumes,
	})
	return mapErr("remove container", err)
}

func (s *SDK) ListProjectContainers(ctx context.Context, project domain.ProjectID) ([]ContainerState, error) {
	summaries, err := s.cli.ContainerList(ctx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", projectLabel+"="+string(project)),
		),
	})
	if err != nil {
		return nil, mapErr("list project containers", err)
	}
	states := make([]ContainerState, 0, len(summaries))
	for _, sum := range summaries {
		name := ""
		if len(sum.Names) > 0 {
			name = strings.TrimPrefix(sum.Names[0], "/")
		}
		states = append(states, ContainerState{
			ID:      ContainerID(sum.ID),
			Name:    ContainerName(name),
			Image:   sum.Image,
			Running: sum.State == container.StateRunning,
			Labels:  sum.Labels,
		})
	}
	return states, nil
}

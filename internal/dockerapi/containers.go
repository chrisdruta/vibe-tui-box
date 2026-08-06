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

	"vibe/internal/domain"
)

// projectLabel and managedLabel are duplicated from the runtime package
// to keep the dependency direction inward; runtime owns the full label
// vocabulary.
const (
	projectLabel = "dev.vibe.project"
	managedLabel = "dev.vibe.managed"
)

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
	if resp.NetworkSettings != nil {
		for netName, ep := range resp.NetworkSettings.Networks {
			if ep == nil || ep.IPAddress == "" {
				continue
			}
			if state.Networks == nil {
				state.Networks = map[string]string{}
			}
			state.Networks[netName] = ep.IPAddress
		}
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
	var tmpfsMounts map[string]string
	for _, m := range req.Mounts {
		var kind mount.Type
		switch m.Kind {
		case BindMount:
			kind = mount.TypeBind
		case VolumeMount:
			kind = mount.TypeVolume
		case TmpfsMount:
			// XDG-shaped runtime dir: private to the dev user. Ownership
			// must be minted here — the closed policy drops CAP_CHOWN, so
			// a root-owned tmpfs would be dead weight the container can
			// never claim. uid/gid match compile's dev user (vscode, 1000).
			// Rides HostConfig.Tmpfs (the --tmpfs path) rather than the
			// typed Mounts API: the daemon whitelists tmpfs options there
			// and rejects uid/gid, while --tmpfs hands them to the kernel.
			if tmpfsMounts == nil {
				tmpfsMounts = map[string]string{}
			}
			tmpfsMounts[m.Target] = "uid=1000,gid=1000,mode=0700,size=64m"
			continue
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
	if len(req.DNS) > 0 && req.Network == "" {
		return "", fmt.Errorf("%w: dns servers require a network", domain.ErrInvalid)
	}
	// Init: the dev entrypoint parks on `sleep infinity`, which never
	// reaps — orphans (detached hooks, dead inner tmux servers) would
	// re-parent to it and zombify for the container's lifetime. tini as
	// PID 1 reaps them for every managed container.
	initProc := true
	hostConfig := &container.HostConfig{
		Mounts:         mounts,
		Tmpfs:          tmpfsMounts,
		PortBindings:   bindings,
		DNS:            req.DNS,
		CapDrop:        strslice.StrSlice{"ALL"},
		SecurityOpt:    []string{"no-new-privileges:true"},
		ReadonlyRootfs: req.Policy.ReadonlyRootFS,
		Init:           &initProc,
	}
	if req.Policy.NetBindService {
		hostConfig.CapAdd = strslice.StrSlice{"NET_BIND_SERVICE"}
	}
	if req.Log != (LogPolicy{}) {
		// json-file, not "local": the docker-logs demux is the ledger
		// read path and json-file is the proven default format.
		if req.Log.MaxSize == "" || req.Log.MaxFiles < 1 {
			return "", fmt.Errorf("%w: log policy requires max size and max files", domain.ErrInvalid)
		}
		hostConfig.LogConfig = container.LogConfig{
			Type: "json-file",
			Config: map[string]string{
				"max-size": req.Log.MaxSize,
				"max-file": strconv.Itoa(req.Log.MaxFiles),
			},
		}
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

func (s *SDK) RenameContainer(ctx context.Context, id ContainerID, name ContainerName) error {
	return mapErr("rename container", s.cli.ContainerRename(ctx, string(id), string(name)))
}

func (s *SDK) RemoveContainer(ctx context.Context, id ContainerID, opts RemoveOptions) error {
	err := s.cli.ContainerRemove(ctx, string(id), container.RemoveOptions{
		Force:         opts.Force,
		RemoveVolumes: opts.RemoveVolumes,
	})
	return mapErr("remove container", err)
}

func (s *SDK) ListProjectContainers(ctx context.Context, project domain.ProjectID) ([]ContainerState, error) {
	return s.listByLabel(ctx, "list project containers", projectLabel+"="+string(project))
}

func (s *SDK) ListManagedContainers(ctx context.Context) ([]ContainerState, error) {
	return s.listByLabel(ctx, "list managed containers", managedLabel+"=true")
}

func (s *SDK) listByLabel(ctx context.Context, op, label string) ([]ContainerState, error) {
	summaries, err := s.cli.ContainerList(ctx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", label),
		),
	})
	if err != nil {
		return nil, mapErr(op, err)
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

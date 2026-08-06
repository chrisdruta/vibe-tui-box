package dockerapi

import (
	"context"
	"fmt"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"

	"vibe/internal/domain"
)

// SDK adapts the Docker SDK to the engine Client interface. It is the
// only place in the module that touches SDK types.
type SDK struct {
	cli *client.Client
}

var _ Client = (*SDK)(nil)

// NewSDK builds a lazy client from the standard Docker environment
// (DOCKER_HOST or the default socket). No daemon contact happens until
// the first call.
func NewSDK() (*SDK, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("%w: docker client: %v", domain.ErrUnavailable, err)
	}
	return &SDK{cli: cli}, nil
}

func (s *SDK) Ping(ctx context.Context) error {
	if _, err := s.cli.Ping(ctx); err != nil {
		return fmt.Errorf("%w: docker daemon: %v", domain.ErrUnavailable, err)
	}
	return nil
}

// mapErr converts SDK errors into engine error classes.
func mapErr(op string, err error) error {
	switch {
	case err == nil:
		return nil
	case cerrdefs.IsNotFound(err):
		return fmt.Errorf("%w: %s: %v", domain.ErrNotFound, op, err)
	case client.IsErrConnectionFailed(err):
		return fmt.Errorf("%w: %s: %v", domain.ErrUnavailable, op, err)
	default:
		return fmt.Errorf("%s: %w", op, err)
	}
}

func (s *SDK) EnsureVolume(ctx context.Context, spec VolumeSpec) error {
	_, err := s.cli.VolumeCreate(ctx, volume.CreateOptions{
		Name:   spec.Name,
		Labels: spec.Labels,
	})
	return mapErr("create volume "+spec.Name, err)
}

func (s *SDK) RemoveVolume(ctx context.Context, name VolumeName) error {
	err := s.cli.VolumeRemove(ctx, string(name), false)
	if cerrdefs.IsNotFound(err) {
		return nil
	}
	return mapErr("remove volume "+string(name), err)
}

func (s *SDK) EnsureNetwork(ctx context.Context, spec NetworkSpec) error {
	_, err := s.cli.NetworkCreate(ctx, spec.Name, network.CreateOptions{
		Driver: "bridge",
		Labels: spec.Labels,
	})
	if cerrdefs.IsConflict(err) {
		return nil // already exists
	}
	return mapErr("create network "+spec.Name, err)
}

func (s *SDK) RemoveNetwork(ctx context.Context, name NetworkName) error {
	err := s.cli.NetworkRemove(ctx, string(name))
	if cerrdefs.IsNotFound(err) {
		return nil
	}
	return mapErr("remove network "+string(name), err)
}

// Build submits an extension build from a frozen candidate context.
func (s *SDK) Build(ctx context.Context, req BuildRequest, sink ProgressSink) (BuiltImage, error) {
	return s.buildSDK(ctx, req, sink)
}

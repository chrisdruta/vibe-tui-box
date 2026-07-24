package dockerapi

import (
	"context"
	"time"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
)

// Client is the complete Docker surface the engine uses. Everything
// else in the Engine API is deliberately unreachable.
type Client interface {
	Ping(ctx context.Context) error

	ResolveImage(ctx context.Context, ref ImageRef) (ResolvedImage, error)
	PullImage(ctx context.Context, image ResolvedImage, sink ProgressSink) error
	Build(ctx context.Context, req BuildRequest, sink ProgressSink) (BuiltImage, error)

	InspectContainer(ctx context.Context, name ContainerName) (ContainerState, error)
	CreateContainer(ctx context.Context, req CreateRequest) (ContainerID, error)
	StartContainer(ctx context.Context, id ContainerID) error
	StopContainer(ctx context.Context, id ContainerID, timeout time.Duration) error
	RemoveContainer(ctx context.Context, id ContainerID, opts RemoveOptions) error
	WaitContainer(ctx context.Context, id ContainerID) (int, error)
	ListProjectContainers(ctx context.Context, project domain.ProjectID) ([]ContainerState, error)

	Exec(ctx context.Context, req ExecRequest) (ExecResult, error)
	Attach(ctx context.Context, req AttachRequest) error
	Logs(ctx context.Context, req LogsRequest) error

	EnsureVolume(ctx context.Context, spec VolumeSpec) error
	RemoveVolume(ctx context.Context, name VolumeName) error
	EnsureNetwork(ctx context.Context, spec NetworkSpec) error
	RemoveNetwork(ctx context.Context, name NetworkName) error
}

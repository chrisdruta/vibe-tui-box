package dockerapi

import (
	"context"
	"time"

	"vibe/internal/domain"
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
	RenameContainer(ctx context.Context, id ContainerID, name ContainerName) error
	WaitContainer(ctx context.Context, id ContainerID) (int, error)
	ListProjectContainers(ctx context.Context, project domain.ProjectID) ([]ContainerState, error)
	// ListManagedContainers enumerates every engine-managed container on
	// the host across all projects, running or not.
	ListManagedContainers(ctx context.Context) ([]ContainerState, error)

	Exec(ctx context.Context, req ExecRequest) (ExecResult, error)
	Attach(ctx context.Context, req AttachRequest) error
	Logs(ctx context.Context, req LogsRequest) error
	// Events streams managed-container daemon events into sink until
	// ctx cancels or the stream dies. No replay: events from before the
	// call are gone, so subscribers keep their own fallback cadence.
	Events(ctx context.Context, req EventsRequest, sink func(ContainerEvent)) error

	EnsureVolume(ctx context.Context, spec VolumeSpec) error
	RemoveVolume(ctx context.Context, name VolumeName) error
	EnsureNetwork(ctx context.Context, spec NetworkSpec) error
	RemoveNetwork(ctx context.Context, name NetworkName) error
}

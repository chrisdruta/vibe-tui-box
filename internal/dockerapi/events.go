package dockerapi

import (
	"context"

	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"

	"vibe/internal/domain"
)

// Events subscribes to the daemon's event stream, narrowed server-side
// to managed containers (and to req.Actions when given — each `event`
// filter value ORs), and pumps each event into sink. The daemon labels
// events with the container's own labels, which is where the project
// field comes from.
func (s *SDK) Events(ctx context.Context, req EventsRequest, sink func(ContainerEvent)) error {
	args := filters.NewArgs(
		filters.Arg("type", string(events.ContainerEventType)),
		filters.Arg("label", managedLabel+"=true"),
	)
	for _, action := range req.Actions {
		args.Add("event", action)
	}
	msgs, errs := s.cli.Events(ctx, events.ListOptions{Filters: args})
	for {
		select {
		case m := <-msgs:
			sink(ContainerEvent{
				Action:  string(m.Action),
				Name:    ContainerName(m.Actor.Attributes["name"]),
				Project: domain.ProjectID(m.Actor.Attributes[projectLabel]),
			})
		case err := <-errs:
			// The SDK reports ctx cancellation here too; both ways the
			// stream is over and the caller owns the reconnect.
			return mapErr("docker events", err)
		}
	}
}

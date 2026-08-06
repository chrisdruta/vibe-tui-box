package app

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"vibe/internal/dockerapi"
	"vibe/internal/domain"
	"vibe/internal/model"
)

func TestLogs(t *testing.T) {
	a, docker := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Up(ctx, UpRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	rec := mustResolve(t, a, dir)
	devName := dockerapi.ContainerName(model.DevContainerName(rec.ID))
	docker.LogsOutputs[devName] = "hello from dev\n"

	var out bytes.Buffer
	if err := a.Logs(ctx, LogsRequest{Dir: dir, Follow: true, Tail: 50,
		Streams: dockerapi.Streams{Out: &out}}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "hello from dev\n" {
		t.Fatalf("logs output %q", out.String())
	}
	calls := docker.CallsTo("Logs")
	if len(calls) != 1 {
		t.Fatalf("want 1 Logs call, got %d", len(calls))
	}
	req := calls[0].Request.(dockerapi.LogsRequest)
	want := dockerapi.LogsRequest{Container: devName, Follow: true, Tail: 50, Streams: req.Streams}
	if req != want {
		t.Fatalf("logs request %+v, want %+v", req, want)
	}

	// A named sidecar addresses its derived container; a missing one is
	// not-found, a malformed name is invalid before Docker is consulted.
	if err := a.Logs(ctx, LogsRequest{Dir: dir, Service: "db"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing sidecar: %v", err)
	}
	if err := a.Logs(ctx, LogsRequest{Dir: dir, Service: "../evil"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("bad service name: %v", err)
	}
	if got := len(docker.CallsTo("Logs")); got != 2 {
		t.Fatalf("invalid name must not reach docker; %d calls", got)
	}
}

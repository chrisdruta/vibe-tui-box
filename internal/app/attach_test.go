package app

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/chrisdruta/vibe-tui-box/internal/dockerapi"
	dockerfake "github.com/chrisdruta/vibe-tui-box/internal/dockerapi/fake"
	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/model"
)

func TestAttachSession(t *testing.T) {
	a, docker := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Up(ctx, UpRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}

	// Bare attach: the container's main process, no exec involved.
	if _, err := a.Attach(ctx, AttachRequest{ContainerCommand: ContainerCommand{Dir: dir}}); err != nil {
		t.Fatal(err)
	}
	if len(docker.CallsTo("Attach")) != 1 {
		t.Fatal("bare attach must use docker attach")
	}

	// Session attach: probed, then the payload attach mode with argv.
	if _, err := a.Attach(ctx, AttachRequest{ContainerCommand: ContainerCommand{Dir: dir}, Session: "services"}); err != nil {
		t.Fatal(err)
	}
	execs := docker.CallsTo("Exec")
	last := execs[len(execs)-1].Request.(dockerapi.ExecRequest)
	want := []string{"bash", model.PayloadAgentSession, "attach", "services"}
	if len(last.Argv) != len(want) {
		t.Fatalf("attach argv %v, want %v", last.Argv, want)
	}
	for i := range want {
		if last.Argv[i] != want[i] {
			t.Fatalf("attach argv %v, want %v", last.Argv, want)
		}
	}

	// Under `vibe tui` the exec carries the nested marker, so the inner
	// client a tray ghost's viewer creates is reapable when the UI dies.
	if _, err := a.Attach(ctx, AttachRequest{
		ContainerCommand: ContainerCommand{Dir: dir}, Session: "agent-ghost", Nested: true,
	}); err != nil {
		t.Fatal(err)
	}
	execs = docker.CallsTo("Exec")
	nested := execs[len(execs)-1].Request.(dockerapi.ExecRequest)
	found := false
	for _, e := range nested.Env {
		if e == "VIBE_NESTED=1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("nested attach must carry the marker: %v", nested.Env)
	}

	// Hostile session names never reach the container.
	before := len(docker.CallsTo("Exec"))
	if _, err := a.Attach(ctx, AttachRequest{ContainerCommand: ContainerCommand{Dir: dir}, Session: "a;rm -rf"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("bad session name: %v", err)
	}
	if len(docker.CallsTo("Exec")) != before {
		t.Fatal("invalid session name reached docker")
	}

	// A tmux-less image degrades to unavailable, not a broken exec.
	docker.ExecResults[dockerfake.ExecKey([]string{
		"test", "-r", model.PayloadAgentSession, "-a",
		"(", "-x", pinnedTmuxPath, "-o", "-x", distroTmuxPath, ")",
	})] = dockerapi.ExecResult{ExitCode: 1}
	if _, err := a.Attach(ctx, AttachRequest{ContainerCommand: ContainerCommand{Dir: dir}, Session: "services"}); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("probe failure should be unavailable: %v", err)
	}
}

func TestStopSession(t *testing.T) {
	a, docker := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Up(ctx, UpRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}

	// Address-direct stop: probed, then the payload kill mode with the
	// FULL address — never a flag reverse-map.
	res, err := a.StopSession(ctx, StopSessionRequest{Dir: dir, Session: "agent-codex-review-cold"})
	if err != nil || res.Session != "agent-codex-review-cold" {
		t.Fatalf("stop: %+v, %v", res, err)
	}
	execs := docker.CallsTo("Exec")
	last := execs[len(execs)-1].Request.(dockerapi.ExecRequest)
	want := []string{"bash", model.PayloadAgentSession, "kill", "agent-codex-review-cold"}
	if len(last.Argv) != len(want) {
		t.Fatalf("kill argv %v, want %v", last.Argv, want)
	}
	for i := range want {
		if last.Argv[i] != want[i] {
			t.Fatalf("kill argv %v, want %v", last.Argv, want)
		}
	}

	// Only agent-convention addresses pass — `services` is a lifecycle
	// surface, and hostile words never reach the container.
	before := len(docker.CallsTo("Exec"))
	for _, bad := range []string{"services", "agentx", "a;rm -rf", ""} {
		if _, err := a.StopSession(ctx, StopSessionRequest{Dir: dir, Session: bad}); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("address %q: %v", bad, err)
		}
	}
	if len(docker.CallsTo("Exec")) != before {
		t.Fatal("invalid address reached docker")
	}

	// A tmux-less image degrades to unavailable, like attach.
	docker.ExecResults[dockerfake.ExecKey([]string{
		"test", "-r", model.PayloadAgentSession, "-a",
		"(", "-x", pinnedTmuxPath, "-o", "-x", distroTmuxPath, ")",
	})] = dockerapi.ExecResult{ExitCode: 1}
	if _, err := a.StopSession(ctx, StopSessionRequest{Dir: dir, Session: "agent"}); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("probe failure should be unavailable: %v", err)
	}
}

func TestDismissSession(t *testing.T) {
	a, docker := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Up(ctx, UpRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}

	// Address-direct dismissal: probed, then the payload dismiss mode.
	res, err := a.DismissSession(ctx, DismissSessionRequest{Dir: dir, Session: "agent-old-cold"})
	if err != nil || res.Session != "agent-old-cold" {
		t.Fatalf("dismiss: %+v, %v", res, err)
	}
	execs := docker.CallsTo("Exec")
	last := execs[len(execs)-1].Request.(dockerapi.ExecRequest)
	want := []string{"bash", model.PayloadAgentSession, "dismiss", "agent-old-cold"}
	if !slices.Equal(last.Argv, want) {
		t.Fatalf("dismiss argv %v, want %v", last.Argv, want)
	}

	// Same address policy as stop: agent-convention or nothing.
	before := len(docker.CallsTo("Exec"))
	for _, bad := range []string{"services", "agentx", "a;b", ""} {
		if _, err := a.DismissSession(ctx, DismissSessionRequest{Dir: dir, Session: bad}); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("address %q: %v", bad, err)
		}
	}
	if len(docker.CallsTo("Exec")) != before {
		t.Fatal("invalid address reached docker")
	}
}

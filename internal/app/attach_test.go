package app

import (
	"context"
	"errors"
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
		"test", "-x", agentTmuxPath, "-a", "-r", model.PayloadAgentSession,
	})] = dockerapi.ExecResult{ExitCode: 1}
	if _, err := a.Attach(ctx, AttachRequest{ContainerCommand: ContainerCommand{Dir: dir}, Session: "services"}); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("probe failure should be unavailable: %v", err)
	}
}

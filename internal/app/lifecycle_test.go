package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/chrisdruta/vibe-tui-box/internal/dockerapi"
	dockerfake "github.com/chrisdruta/vibe-tui-box/internal/dockerapi/fake"
	"github.com/chrisdruta/vibe-tui-box/internal/paths"
)

func writeManifest(t *testing.T, dir, manifest string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, paths.ManifestRelPath), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestUpStatusRunDown(t *testing.T) {
	a, docker := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)

	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}

	up, err := a.Up(ctx, UpRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if up.Candidate.IsZero() || !up.State.Running() {
		t.Fatalf("up result wrong: %+v", up)
	}
	if up.Record.Approved == nil || *up.Record.Approved != up.Candidate {
		t.Fatal("approved candidate not recorded")
	}

	// The base image was resolved to a digest during preparation.
	if len(docker.CallsTo("ResolveImage")) == 0 {
		t.Fatal("images not resolved")
	}

	// Container env carries the frozen env file plus manifest env.
	creates := docker.CallsTo("CreateContainer")
	if len(creates) != 1 {
		t.Fatalf("want 1 container, got %d", len(creates))
	}
	created := creates[0].Request.(dockerapi.CreateRequest)
	wantEnv := []string{"SECRET=s3cret", "FLAG=1"}
	if len(created.Env) != 2 || created.Env[0] != wantEnv[0] || created.Env[1] != wantEnv[1] {
		t.Fatalf("container env wrong: %v", created.Env)
	}

	status, err := a.Status(ctx, StatusRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !status.State.Running() {
		t.Fatalf("status not running: %+v", status.State)
	}

	// Second up is a no-op for containers and bumps the approved pointer
	// to the identical candidate.
	up2, err := a.Up(ctx, UpRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if up2.Candidate != up.Candidate {
		t.Fatal("identical inputs must produce the identical candidate")
	}
	if got := len(docker.CallsTo("CreateContainer")); got != 1 {
		t.Fatalf("idempotent up created containers: %d", got)
	}

	// Run: exec in the dev container with the frozen env file.
	res, err := a.Run(ctx, ContainerCommand{Dir: dir, Argv: []string{"env"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("run exit %d", res.ExitCode)
	}
	execs := docker.CallsTo("Exec")
	lastExec := execs[len(execs)-1].Request.(dockerapi.ExecRequest)
	if len(lastExec.Env) != 1 || lastExec.Env[0] != "SECRET=s3cret" {
		t.Fatalf("run env wrong: %v", lastExec.Env)
	}
	if lastExec.User != "vscode" {
		t.Fatalf("run user %q", lastExec.User)
	}

	// Exec: explicit env only.
	if _, err := a.Exec(ctx, ContainerCommand{Dir: dir, Argv: []string{"true"}}); err != nil {
		t.Fatal(err)
	}
	execs = docker.CallsTo("Exec")
	lastExec = execs[len(execs)-1].Request.(dockerapi.ExecRequest)
	if len(lastExec.Env) != 0 {
		t.Fatalf("exec must not inherit env: %v", lastExec.Env)
	}

	if _, err := a.Down(ctx, DownRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if len(docker.Containers) != 0 {
		t.Fatal("containers left after down")
	}

	// Agent commands against a stopped project fail clearly.
	if _, err := a.Exec(ctx, ContainerCommand{Dir: dir, Argv: []string{"true"}}); err == nil {
		t.Fatal("exec without a running container should fail")
	}
}

func TestShellProbesCandidates(t *testing.T) {
	a, docker := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Up(ctx, UpRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}

	// zsh missing, bash present.
	docker.ExecResults[dockerfake.ExecKey([]string{"test", "-x", "/bin/zsh"})] = dockerapi.ExecResult{ExitCode: 1}
	docker.ExecResults[dockerfake.ExecKey([]string{"test", "-x", "/bin/bash"})] = dockerapi.ExecResult{ExitCode: 0}

	if _, err := a.Shell(ctx, ContainerCommand{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	execs := docker.CallsTo("Exec")
	last := execs[len(execs)-1].Request.(dockerapi.ExecRequest)
	if len(last.Argv) != 2 || last.Argv[0] != "/bin/bash" || last.Argv[1] != "-l" {
		t.Fatalf("shell argv wrong: %v", last.Argv)
	}
}

func TestUpDoesNotApproveOnFailure(t *testing.T) {
	a, docker := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	docker.StartErr = errors.New("boom")
	if _, err := a.Up(ctx, UpRequest{Dir: dir}); err == nil {
		t.Fatal("up should fail when start fails")
	}
	status, err := a.Status(ctx, StatusRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if status.Record.Approved != nil {
		t.Fatal("failed up must not move the approved candidate")
	}
}

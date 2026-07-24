package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/payload"
	"github.com/chrisdruta/vibe-tui-box/internal/runner"
)

// fakeRunner records invocations and plays back a fixed output.
type fakeRunner struct {
	invs []runner.Invocation
	out  runner.Output
	err  error
}

func (f *fakeRunner) Run(_ context.Context, inv runner.Invocation) (runner.Output, error) {
	f.invs = append(f.invs, inv)
	return f.out, f.err
}

// withClipPayload equips a test app with a payload bundle that carries
// the host clip script, then provisions so the project pins it.
func withClipPayload(t *testing.T, a *App, dir string) {
	t.Helper()
	entry := "#!/bin/sh\nexec sleep infinity\n"
	clip := "#!/usr/bin/env bash\nexit 0\n"
	manifest, err := payload.EncodeManifest([]payload.File{
		{Path: "container/entrypoint.sh", Mode: "0755", Size: int64(len(entry)), Digest: domain.SHA256([]byte(entry))},
		{Path: "host/scripts/clip-image.sh", Mode: "0755", Size: int64(len(clip)), Digest: domain.SHA256([]byte(clip))},
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := payload.New(fstest.MapFS{
		"container/entrypoint.sh":    &fstest.MapFile{Data: []byte(entry)},
		"host/scripts/clip-image.sh": &fstest.MapFile{Data: []byte(clip)},
		payload.ManifestPath:         &fstest.MapFile{Data: manifest},
	})
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "vibe")
	if err := os.WriteFile(exe, []byte("FAKE-ENGINE"), 0o755); err != nil {
		t.Fatal(err)
	}
	a.deps.Payload = bundle
	a.deps.Executable = exe
	if _, err := a.Provision(context.Background(), ProvisionRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
}

func TestClipRunsArtifactScript(t *testing.T) {
	a, docker := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	withClipPayload(t, a, dir)
	if _, err := a.Up(ctx, UpRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	fake := &fakeRunner{}
	a.deps.Runner = fake

	env := []string{"PATH=/usr/bin", "WSL_INTEROP=/run/WSL/1_interop"}
	res, err := a.Clip(ctx, ClipRequest{Dir: dir, Env: env})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("clip exit %d", res.ExitCode)
	}
	if len(fake.invs) != 1 {
		t.Fatalf("want 1 invocation, got %d", len(fake.invs))
	}
	inv := fake.invs[0]
	if !filepath.IsAbs(inv.Path) || !strings.HasSuffix(inv.Path, "/host/scripts/clip-image.sh") {
		t.Fatalf("script path wrong: %q", inv.Path)
	}
	// Argv contract: REPO_ROOT DEST_DIR_OR_EMPTY CONTAINER [--path-only].
	// The registered root is the exact repo path; /tmp mode names the
	// running dev container.
	if len(inv.Args) != 3 || inv.Args[1] != "" {
		t.Fatalf("argv wrong: %v", inv.Args)
	}
	if inv.Args[0] != dir {
		t.Fatalf("repo root %q, want %q", inv.Args[0], dir)
	}
	if !strings.HasPrefix(inv.Args[2], "vibe-") || !strings.HasSuffix(inv.Args[2], "-dev") {
		t.Fatalf("container name wrong: %q", inv.Args[2])
	}
	if fmt.Sprint(inv.Env) != fmt.Sprint(env) {
		t.Fatalf("env not passed through: %v", inv.Env)
	}

	// --path-only appends the script's machine-output flag.
	if _, err := a.Clip(ctx, ClipRequest{Dir: dir, PathOnly: true}); err != nil {
		t.Fatal(err)
	}
	inv = fake.invs[len(fake.invs)-1]
	if len(inv.Args) != 4 || inv.Args[3] != "--path-only" {
		t.Fatalf("path-only argv wrong: %v", inv.Args)
	}

	// Script exit codes pass through untouched (1 = no image).
	fake.out = runner.Output{ExitCode: 1}
	res, err = a.Clip(ctx, ClipRequest{Dir: dir})
	if err != nil || res.ExitCode != 1 {
		t.Fatalf("exit passthrough: %d, %v", res.ExitCode, err)
	}
	_ = docker
}

func TestClipDirModeSkipsDocker(t *testing.T) {
	a, docker := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	withClipPayload(t, a, dir)
	fake := &fakeRunner{}
	a.deps.Runner = fake

	// DIR mode: no running container required, no Docker call made, and
	// the container argv slot stays empty (the script ignores it).
	before := len(docker.Calls())
	if _, err := a.Clip(ctx, ClipRequest{Dir: dir, DestDir: "shots"}); err != nil {
		t.Fatal(err)
	}
	if got := len(docker.Calls()); got != before {
		t.Fatalf("DIR mode touched docker: %d calls added", got-before)
	}
	inv := fake.invs[len(fake.invs)-1]
	if len(inv.Args) != 3 || inv.Args[1] != "shots" || inv.Args[2] != "" {
		t.Fatalf("DIR argv wrong: %v", inv.Args)
	}
}

func TestClipRequiresContainerAndArtifact(t *testing.T) {
	a, _ := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}

	// No pinned artifact: unavailable, pointing at provision.
	_, err := a.Clip(ctx, ClipRequest{Dir: dir})
	if !errors.Is(err, domain.ErrUnavailable) || !strings.Contains(err.Error(), "vibe provision") {
		t.Fatalf("no-artifact error wrong: %v", err)
	}

	// Artifact pinned but the container is not running: /tmp mode fails
	// with the vibe-up hint, DIR mode still works.
	withClipPayload(t, a, dir)
	fake := &fakeRunner{}
	a.deps.Runner = fake
	if _, err := a.Clip(ctx, ClipRequest{Dir: dir}); err == nil || !strings.Contains(err.Error(), "vibe up") {
		t.Fatalf("stopped-container error wrong: %v", err)
	}
	if _, err := a.Clip(ctx, ClipRequest{Dir: dir, DestDir: "shots"}); err != nil {
		t.Fatalf("DIR mode should not need a container: %v", err)
	}
}

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

	"vibe/internal/dockerapi"
	"vibe/internal/domain"
	"vibe/internal/payload"
	"vibe/internal/runner"
)

// fakeRunner records invocations, materializes the PNG a real `save`
// would write, and plays back a fixed output.
type fakeRunner struct {
	invs     []runner.Invocation
	out      runner.Output
	err      error
	noImage  bool // save reports an empty clipboard
	pngBytes []byte
}

func (f *fakeRunner) Run(_ context.Context, inv runner.Invocation) (runner.Output, error) {
	f.invs = append(f.invs, inv)
	if len(inv.Args) == 2 && inv.Args[0] == "save" {
		if f.noImage {
			return runner.Output{ExitCode: 1, Stderr: []byte("No image on the clipboard.\n")}, nil
		}
		png := f.pngBytes
		if png == nil {
			png = []byte("PNGDATA")
		}
		if err := os.WriteFile(inv.Args[1], png, 0o644); err != nil {
			return runner.Output{}, err
		}
	}
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

func TestClipTmpModeStreamsThroughEngineExec(t *testing.T) {
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
	if !strings.HasPrefix(res.ContainerPath, "/tmp/clip-") || !strings.HasSuffix(res.ContainerPath, ".png") {
		t.Fatalf("container path wrong: %q", res.ContainerPath)
	}
	if res.SavedPath != "" {
		t.Fatalf("tmp mode must not report a workspace copy: %q", res.SavedPath)
	}
	// The script is invoked as pure clipboard glue — save, then the
	// interactive copy-back — with the curated env, never a repo path
	// or container name.
	if len(fake.invs) != 2 {
		t.Fatalf("want save+copy invocations, got %d", len(fake.invs))
	}
	save, clipCopy := fake.invs[0], fake.invs[1]
	if !filepath.IsAbs(save.Path) || !strings.HasSuffix(save.Path, "/host/scripts/clip-image.sh") {
		t.Fatalf("script path wrong: %q", save.Path)
	}
	if save.Args[0] != "save" || !strings.HasSuffix(save.Args[1], ".png") {
		t.Fatalf("save argv wrong: %v", save.Args)
	}
	if clipCopy.Args[0] != "copy" || clipCopy.Args[1] != res.ContainerPath {
		t.Fatalf("copy argv wrong: %v", clipCopy.Args)
	}
	if !res.PathCopied {
		t.Fatal("successful copy-back must be reported")
	}
	if fmt.Sprint(save.Env) != fmt.Sprint(env) {
		t.Fatalf("env not passed through: %v", save.Env)
	}
	// The bytes ride the engine's typed docker exec (argv-only tee),
	// not a script-side docker CLI.
	execs := docker.CallsTo("Exec")
	if len(execs) == 0 {
		t.Fatal("no engine exec recorded")
	}
	last := execs[len(execs)-1].Request.(dockerapi.ExecRequest)
	if len(last.Argv) != 2 || last.Argv[0] != "tee" || last.Argv[1] != res.ContainerPath {
		t.Fatalf("stream argv wrong: %v", last.Argv)
	}
	if !last.Stdin || last.User != devContainerUser {
		t.Fatalf("stream exec shape wrong: %+v", last)
	}

	// PathOnly is the machine flow: no clipboard copy-back.
	fake.invs = nil
	if res, err := a.Clip(ctx, ClipRequest{Dir: dir, PathOnly: true}); err != nil || res.PathCopied {
		t.Fatalf("path-only must skip copy-back: %+v, %v", res, err)
	}
	if len(fake.invs) != 1 || fake.invs[0].Args[0] != "save" {
		t.Fatalf("path-only invocations wrong: %v", fake.invs)
	}

	// An empty clipboard surfaces the script's diagnostic as the error.
	fake.noImage = true
	if _, err := a.Clip(ctx, ClipRequest{Dir: dir}); err == nil || !strings.Contains(err.Error(), "No image") {
		t.Fatalf("no-image error wrong: %v", err)
	}
}

func TestClipDirModeWritesWorkspaceAndSkipsDocker(t *testing.T) {
	a, docker := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	withClipPayload(t, a, dir)
	fake := &fakeRunner{pngBytes: []byte("IMAGEBYTES")}
	a.deps.Runner = fake

	before := len(docker.Calls())
	res, err := a.Clip(ctx, ClipRequest{Dir: dir, DestDir: "shots"})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(docker.Calls()); got != before {
		t.Fatalf("DIR mode touched docker: %d calls added", got-before)
	}
	if !strings.HasPrefix(res.SavedPath, "shots/clip-") {
		t.Fatalf("saved path wrong: %q", res.SavedPath)
	}
	if res.ContainerPath != "/workspace/"+res.SavedPath {
		t.Fatalf("container path wrong: %q", res.ContainerPath)
	}
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(res.SavedPath)))
	if err != nil || string(data) != "IMAGEBYTES" {
		t.Fatalf("workspace copy wrong: %q, %v", data, err)
	}
}

func TestClipDirModeRefusesEscapes(t *testing.T) {
	a, _ := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	withClipPayload(t, a, dir)
	fake := &fakeRunner{}
	a.deps.Runner = fake

	// Lexical escapes are invalid before anything runs.
	for _, dest := range []string{"/abs", "../out", "a/../../out"} {
		if _, err := a.Clip(ctx, ClipRequest{Dir: dir, DestDir: dest}); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("dest %q should be invalid, got %v", dest, err)
		}
	}
	if len(fake.invs) != 0 {
		t.Fatal("invalid destinations must not reach the clipboard script")
	}

	// A pre-planted symlink pointing outside the workspace cannot be
	// traversed: the os.Root write refuses it.
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "captures")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Clip(ctx, ClipRequest{Dir: dir, DestDir: "captures"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("symlink escape should be invalid, got %v", err)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatal("escape target must stay untouched")
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
	// with the vibe-up hint before the clipboard is touched; DIR mode
	// still works.
	withClipPayload(t, a, dir)
	fake := &fakeRunner{}
	a.deps.Runner = fake
	if _, err := a.Clip(ctx, ClipRequest{Dir: dir}); err == nil || !strings.Contains(err.Error(), "vibe up") {
		t.Fatalf("stopped-container error wrong: %v", err)
	}
	if len(fake.invs) != 0 {
		t.Fatal("a missing container must fail before the clipboard runs")
	}
	if _, err := a.Clip(ctx, ClipRequest{Dir: dir, DestDir: "shots"}); err != nil {
		t.Fatalf("DIR mode should not need a container: %v", err)
	}
}

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/chrisdruta/vibe-tui-box/internal/dockerapi"
	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/payload"
	"github.com/chrisdruta/vibe-tui-box/internal/registry"
	"github.com/chrisdruta/vibe-tui-box/internal/tmux"
)

func TestStartApproved(t *testing.T) {
	a, docker := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	reg, err := a.Register(ctx, RegisterRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}

	// Nothing approved yet: refuse with the `vibe up` hint instead of
	// letting a tui session race a container that cannot exist.
	err = a.startApproved(ctx, reg.Record)
	if err == nil || !errors.Is(err, domain.ErrUnavailable) || !strings.Contains(err.Error(), "vibe up") {
		t.Fatalf("no-approved error wrong: %v", err)
	}

	up, err := a.Up(ctx, UpRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}

	// Running and in sync: a status listing, no reconcile.
	created := len(docker.CallsTo("CreateContainer"))
	if err := a.startApproved(ctx, up.Record); err != nil {
		t.Fatal(err)
	}
	if got := len(docker.CallsTo("CreateContainer")); got != created {
		t.Fatalf("in-sync start reconciled: %d -> %d creates", created, got)
	}

	// After a down, the approved candidate is brought back exactly —
	// containers recreated, no input freeze (no new snapshot).
	if _, err := a.Down(ctx, DownRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	snaps := len(docker.CallsTo("ResolveImage"))
	if err := a.startApproved(ctx, up.Record); err != nil {
		t.Fatal(err)
	}
	if got := len(docker.CallsTo("CreateContainer")); got <= created {
		t.Fatalf("post-down start did not recreate containers: %d creates", got)
	}
	if got := len(docker.CallsTo("ResolveImage")); got != snaps {
		t.Fatal("startApproved must not re-resolve inputs — it runs the approved candidate")
	}
}

func TestStartApprovedGuards(t *testing.T) {
	a, docker := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)

	// A service makes the plan multi-container, so completeness is
	// judged against the PLAN — a removed sidecar must not pass.
	manifest := testManifest + `services:
  cache:
    image: "redis:7"
`
	writeManifest(t, dir, manifest)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	up, err := a.Up(ctx, UpRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}

	// Remove the sidecar outright: the state listing alone can't see it,
	// the plan can. startApproved must reconcile it back.
	containers, err := docker.ListProjectContainers(ctx, up.Record.ID)
	if err != nil {
		t.Fatal(err)
	}
	removed := false
	for _, c := range containers {
		if strings.Contains(string(c.Name), "-svc-") {
			if err := docker.RemoveContainer(ctx, c.ID, dockerapi.RemoveOptions{Force: true}); err != nil {
				t.Fatal(err)
			}
			removed = true
		}
	}
	if !removed {
		t.Fatal("no sidecar container to remove")
	}
	created := len(docker.CallsTo("CreateContainer"))
	if err := a.startApproved(ctx, up.Record); err != nil {
		t.Fatal(err)
	}
	if got := len(docker.CallsTo("CreateContainer")); got <= created {
		t.Fatal("missing planned sidecar did not trigger a reconcile")
	}

	// Running containers on a DIFFERENT candidate are live work: point
	// the approved pointer at the OLD candidate (the crashed-up shape)
	// and startApproved must refuse rather than replace them.
	writeManifest(t, dir, manifest+"bootstrap:\n  required: [git]\n")
	up2, err := a.Up(ctx, UpRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if up2.Candidate == up.Candidate {
		t.Fatal("manifest change should compile a new candidate")
	}
	reverted, err := a.deps.Registry.Update(ctx, up2.Record.ID, up2.Record.Revision, func(r *registry.Record) error {
		r.Approved = &up.Candidate
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = a.startApproved(ctx, reverted)
	if err == nil || !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale running containers should refuse with conflict, got %v", err)
	}
	if !strings.Contains(err.Error(), "vibe up") {
		t.Fatalf("refusal must name the deliberate command: %v", err)
	}
}

func TestDownKillsUISession(t *testing.T) {
	a, _ := newTestApp(t)
	rt := &recordingTmux{}
	a.deps.Tmux = rt
	ctx := context.Background()
	dir := newProject(t)
	reg, err := a.Register(ctx, RegisterRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Up(ctx, UpRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Down(ctx, DownRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	want := tmux.SessionFor(reg.Record.ID)
	if len(rt.killed) != 1 || rt.killed[0] != want {
		t.Fatalf("down killed %v, want exactly [%s]", rt.killed, want)
	}
}

func TestMaterializeTuiConfAppendsUserConf(t *testing.T) {
	a, _ := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}

	entry := "#!/bin/sh\nexec sleep infinity\n"
	conf := "# payload conf body\nset -g status on\n"
	manifest, err := payload.EncodeManifest([]payload.File{
		{Path: "container/entrypoint.sh", Mode: "0755", Size: int64(len(entry)), Digest: domain.SHA256([]byte(entry))},
		{Path: "host/tmux-tui.conf", Mode: "0644", Size: int64(len(conf)), Digest: domain.SHA256([]byte(conf))},
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := payload.New(fstest.MapFS{
		"container/entrypoint.sh": &fstest.MapFile{Data: []byte(entry)},
		"host/tmux-tui.conf":      &fstest.MapFile{Data: []byte(conf)},
		payload.ManifestPath:      &fstest.MapFile{Data: manifest},
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
	if _, err := a.Provision(ctx, ProvisionRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}

	_, rec, err := a.resolveProject(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	path, hostDir, err := a.materializeTuiConf(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	if path == "" || hostDir == "" {
		t.Fatalf("conf not materialized: %q %q", path, hostDir)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	// Prologue stamps, the payload body, then the user conf hook LAST so
	// user overrides win.
	if !strings.Contains(got, "@vibe_exe") || !strings.Contains(got, "# payload conf body") {
		t.Fatalf("materialized conf incomplete:\n%s", got)
	}
	userConf := filepath.Join(filepath.Dir(a.deps.Layout.Root), ".config", "vibe", "tui.conf")
	wantLine := "source-file -q " + `"` + userConf + `"`
	idx := strings.Index(got, wantLine)
	if idx == -1 {
		t.Fatalf("user conf hook missing (want %q):\n%s", wantLine, got)
	}
	if idx < strings.Index(got, "# payload conf body") {
		t.Fatalf("user conf must load after the payload body:\n%s", got)
	}
}

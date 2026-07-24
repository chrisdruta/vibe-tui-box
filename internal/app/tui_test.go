package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/payload"
)

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

package snapshot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"vibe/internal/domain"
	"vibe/internal/lock"
	"vibe/internal/paths"
	"vibe/internal/store"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	layout, err := paths.NewLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	st, err := store.New(layout, lock.NewFlock(layout.Locks))
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewService(st)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func newWorkspace(t *testing.T, files map[string]string) paths.Root {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := files[paths.ManifestRelPath]; !ok {
		if err := os.MkdirAll(filepath.Join(dir, ".vibe"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, paths.ManifestRelPath), []byte("schema: 1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root, err := paths.Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestCreateDeterministic(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	files := map[string]string{
		"data/a.txt":     "alpha",
		"data/sub/b.txt": "beta",
	}
	spec := func(root paths.Root) Spec {
		return Spec{
			ProjectRoot: root,
			Entries: []Entry{
				{Source: ".vibe/vibe.yaml", Dest: "vibe.yaml"},
				{Source: "data", Dest: "imports/0"},
			},
		}
	}

	r1, err := svc.Create(ctx, spec(newWorkspace(t, files)))
	if err != nil {
		t.Fatal(err)
	}
	r2, err := svc.Create(ctx, spec(newWorkspace(t, files)))
	if err != nil {
		t.Fatal(err)
	}
	if r1.Digest != r2.Digest {
		t.Fatalf("identical inputs digest differently: %s vs %s", r1.Digest, r2.Digest)
	}
	if len(r1.Files) != 3 || r1.Bytes != int64(len("alpha")+len("beta")+len("schema: 1\n")) {
		t.Fatalf("files/bytes wrong: %+v", r1)
	}
	if _, err := os.Stat(filepath.Join(r1.Path, "imports/0/sub/b.txt")); err != nil {
		t.Fatal(err)
	}

	changed := map[string]string{"data/a.txt": "ALPHA", "data/sub/b.txt": "beta"}
	r3, err := svc.Create(ctx, spec(newWorkspace(t, changed)))
	if err != nil {
		t.Fatal(err)
	}
	if r3.Digest == r1.Digest {
		t.Fatal("changed content, same digest")
	}
}

func TestCreateRejectsSymlink(t *testing.T) {
	svc := newTestService(t)
	root := newWorkspace(t, map[string]string{"data/a.txt": "x"})
	if err := os.Symlink("a.txt", filepath.Join(root.Path, "data", "link")); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Create(context.Background(), Spec{
		ProjectRoot: root,
		Entries:     []Entry{{Source: "data", Dest: "d"}},
	})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("symlink should be rejected, got %v", err)
	}
}

func TestCreateOptionalAndMissing(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	root := newWorkspace(t, nil)

	if _, err := svc.Create(ctx, Spec{
		ProjectRoot: root,
		Entries:     []Entry{{Source: "absent", Dest: "a"}},
	}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing required entry should be not-found, got %v", err)
	}

	res, err := svc.Create(ctx, Spec{
		ProjectRoot: root,
		Entries: []Entry{
			{Source: ".vibe/vibe.yaml", Dest: "vibe.yaml"},
			{Source: "absent", Dest: "a", Optional: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 1 {
		t.Fatalf("optional entry should be skipped: %+v", res.Files)
	}
}

func TestCreateEscapingPaths(t *testing.T) {
	svc := newTestService(t)
	root := newWorkspace(t, nil)
	for _, src := range []string{"../outside", "/etc/passwd", "a/../../b", ""} {
		_, err := svc.Create(context.Background(), Spec{
			ProjectRoot: root,
			Entries:     []Entry{{Source: src, Dest: "d"}},
		})
		if !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("source %q should be invalid, got %v", src, err)
		}
	}
}

func TestCreateLimits(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	root := newWorkspace(t, map[string]string{"data/a.txt": "aaaa", "data/b.txt": "bbbb"})

	entry := []Entry{{Source: "data", Dest: "d"}}
	if _, err := svc.Create(ctx, Spec{ProjectRoot: root, Entries: entry, Limits: Limits{MaxFiles: 1}}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("file-count limit not enforced: %v", err)
	}
	if _, err := svc.Create(ctx, Spec{ProjectRoot: root, Entries: entry, Limits: Limits{MaxFileBytes: 2}}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("file-size limit not enforced: %v", err)
	}
	if _, err := svc.Create(ctx, Spec{ProjectRoot: root, Entries: entry, Limits: Limits{MaxTotalBytes: 5}}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("total-size limit not enforced: %v", err)
	}
}

func TestCreateExecutableModePreserved(t *testing.T) {
	svc := newTestService(t)
	root := newWorkspace(t, map[string]string{"bin/tool": "#!/bin/sh\n"})
	if err := os.Chmod(filepath.Join(root.Path, "bin/tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := svc.Create(context.Background(), Spec{
		ProjectRoot: root,
		Entries:     []Entry{{Source: "bin/tool", Dest: "bin/tool"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(res.Path, "bin/tool"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Fatal("executable bit lost")
	}
	if res.Files[0].Mode != 0o755 {
		t.Fatalf("normalized mode wrong: %o", res.Files[0].Mode)
	}
}

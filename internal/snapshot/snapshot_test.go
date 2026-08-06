package snapshot

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

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

// TestCreateRejectsHardLink pins the walker's aliasing defense: a
// hard-linked source could be rewritten through its other name after
// the snapshot froze, so any nlink > 1 refuses the snapshot outright.
func TestCreateRejectsHardLink(t *testing.T) {
	svc := newTestService(t)
	root := newWorkspace(t, map[string]string{"data/a.txt": "x"})
	if err := os.Link(filepath.Join(root.Path, "data", "a.txt"),
		filepath.Join(root.Path, "data", "alias.txt")); err != nil {
		t.Skipf("hard links unsupported here: %v", err)
	}
	_, err := svc.Create(context.Background(), Spec{
		ProjectRoot: root,
		Entries:     []Entry{{Source: "data", Dest: "d"}},
	})
	if !errors.Is(err, domain.ErrInvalid) || !strings.Contains(err.Error(), "hard-linked") {
		t.Fatalf("hard link should be rejected, got %v", err)
	}
}

// TestCreateRejectsSpecialFiles pins the regular-files-only rule for
// the non-symlink special kinds: a FIFO planted in the workspace must
// refuse the snapshot (never be opened — opening a FIFO for read
// blocks until a writer appears, which is exactly the hang the Lstat
// gate exists to prevent), both as a direct entry and inside a walked
// directory.
func TestCreateRejectsSpecialFiles(t *testing.T) {
	svc := newTestService(t)
	root := newWorkspace(t, map[string]string{"data/a.txt": "x"})
	fifo := filepath.Join(root.Path, "data", "pipe")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("mkfifo unsupported here: %v", err)
	}
	for _, entries := range [][]Entry{
		{{Source: "data/pipe", Dest: "d/pipe"}},
		{{Source: "data", Dest: "d"}},
	} {
		_, err := svc.Create(context.Background(), Spec{ProjectRoot: root, Entries: entries})
		if !errors.Is(err, domain.ErrInvalid) || !strings.Contains(err.Error(), "FIFO") {
			t.Fatalf("FIFO via %v should be rejected, got %v", entries, err)
		}
	}
}

// fakeInfo is the minimal fs.FileInfo for changedDuringCopy's inputs.
type fakeInfo struct {
	size int64
	mod  time.Time
}

func (f fakeInfo) Name() string       { return "f" }
func (f fakeInfo) Size() int64        { return f.size }
func (f fakeInfo) Mode() fs.FileMode  { return 0o644 }
func (f fakeInfo) ModTime() time.Time { return f.mod }
func (f fakeInfo) IsDir() bool        { return false }
func (f fakeInfo) Sys() any           { return nil }

// TestChangedDuringCopy pins the concurrent-mutation abort's decision
// table — the doc's headline "aborts on concurrent mutation" claim.
// The race itself cannot be scheduled deterministically, so the guard
// is tested as a function of its three observable inputs.
func TestChangedDuringCopy(t *testing.T) {
	t0 := time.Unix(1700000000, 0)
	t1 := t0.Add(time.Second)
	before := fakeInfo{size: 10, mod: t0}
	cases := []struct {
		name   string
		after  fakeInfo
		copied int64
		want   bool
	}{
		{"steady file", fakeInfo{size: 10, mod: t0}, 10, false},
		{"grew during copy (long read)", fakeInfo{size: 12, mod: t1}, 11, true},
		{"truncated during copy (short read)", fakeInfo{size: 3, mod: t1}, 3, true},
		{"rewritten after copy (size moved)", fakeInfo{size: 12, mod: t1}, 10, true},
		{"touched after copy (mtime moved)", fakeInfo{size: 10, mod: t1}, 10, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := changedDuringCopy(before, tc.after, tc.copied); got != tc.want {
				t.Fatalf("changedDuringCopy = %v, want %v", got, tc.want)
			}
		})
	}
}

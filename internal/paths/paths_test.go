package paths

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"vibe/internal/domain"
)

func TestNewLayout(t *testing.T) {
	home := t.TempDir()
	layout, err := NewLayout(home)
	if err != nil {
		t.Fatal(err)
	}
	if layout.Root != filepath.Join(home, ".vibe") {
		t.Fatalf("root %q", layout.Root)
	}
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{layout.Projects, layout.Candidates, layout.Snapshots, layout.Locks, layout.Staging} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Fatalf("%s has mode %o, want 0700", dir, info.Mode().Perm())
		}
	}
	if _, err := NewLayout("relative/home"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("relative home should be invalid, got %v", err)
	}
}

func makeProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".vibe"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestRelPath), []byte("schema: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDiscover(t *testing.T) {
	dir := makeProject(t)
	nested := filepath.Join(dir, "src", "deep")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	root, err := Discover(nested)
	if err != nil {
		t.Fatal(err)
	}
	canonical, _ := filepath.EvalSymlinks(dir)
	if root.Path != canonical {
		t.Fatalf("discovered %q, want %q", root.Path, canonical)
	}
	if root.Identity == (FileIdentity{}) {
		t.Fatal("identity not captured")
	}

	if _, err := Discover(t.TempDir()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("no manifest should be not-found, got %v", err)
	}
}

func TestDiscoverIgnoresManifestDirectory(t *testing.T) {
	dir := t.TempDir()
	// A directory named vibe.yaml is not a manifest.
	if err := os.MkdirAll(filepath.Join(dir, ".vibe", "vibe.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(dir); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("directory manifest should be ignored, got %v", err)
	}
}

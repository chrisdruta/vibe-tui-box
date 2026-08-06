package registry

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"vibe/internal/domain"
	"vibe/internal/lock"
	"vibe/internal/paths"
)

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()
	dir := t.TempDir()
	locks := filepath.Join(dir, "locks")
	records := filepath.Join(dir, "projects")
	for _, d := range []string{locks, records} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	r, err := New(records, lock.NewFlock(locks), fixedClock{t: time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func newProjectRoot(t *testing.T) paths.Root {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".vibe"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, paths.ManifestRelPath), []byte("schema: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := paths.Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestCreateResolveConflict(t *testing.T) {
	r := newTestRegistry(t)
	ctx := context.Background()
	root := newProjectRoot(t)

	rec, err := r.Create(ctx, NewRecord{Root: root, DisplayName: "alpha", Mode: ModeRelease})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Revision != 1 || rec.Format != RecordFormat {
		t.Fatalf("new record wrong: %+v", rec)
	}

	got, err := r.Resolve(ctx, root)
	if err != nil || got.ID != rec.ID {
		t.Fatalf("resolve: %+v, %v", got, err)
	}

	if _, err := r.Create(ctx, NewRecord{Root: root, DisplayName: "dup", Mode: ModeRelease}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate registration should conflict, got %v", err)
	}

	other := newProjectRoot(t)
	if _, err := r.Resolve(ctx, other); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unregistered root should be not-found, got %v", err)
	}
}

func TestUpdateCAS(t *testing.T) {
	r := newTestRegistry(t)
	ctx := context.Background()
	rec, err := r.Create(ctx, NewRecord{Root: newProjectRoot(t), DisplayName: "p", Mode: ModeRelease})
	if err != nil {
		t.Fatal(err)
	}

	updated, err := r.Update(ctx, rec.ID, rec.Revision, func(rc *Record) error {
		rc.DisplayName = "renamed"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != rec.Revision+1 || updated.DisplayName != "renamed" {
		t.Fatalf("update wrong: %+v", updated)
	}

	// Stale revision must fail.
	if _, err := r.Update(ctx, rec.ID, rec.Revision, func(*Record) error { return nil }); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale update should conflict, got %v", err)
	}

	if err := r.Delete(ctx, rec.ID, rec.Revision); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale delete should conflict, got %v", err)
	}
	if err := r.Delete(ctx, rec.ID, updated.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get(ctx, rec.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted record should be gone, got %v", err)
	}
}

func TestListOrder(t *testing.T) {
	r := newTestRegistry(t)
	ctx := context.Background()
	for _, name := range []string{"zeta", "alpha", "midway"} {
		if _, err := r.Create(ctx, NewRecord{Root: newProjectRoot(t), DisplayName: name, Mode: ModeRelease}); err != nil {
			t.Fatal(err)
		}
	}
	records, err := r.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 || records[0].DisplayName != "alpha" || records[1].DisplayName != "midway" || records[2].DisplayName != "zeta" {
		t.Fatalf("list order wrong: %+v", records)
	}
}

func TestUnknownFieldsRejected(t *testing.T) {
	r := newTestRegistry(t)
	ctx := context.Background()
	rec, err := r.Create(ctx, NewRecord{Root: newProjectRoot(t), DisplayName: "p", Mode: ModeRelease})
	if err != nil {
		t.Fatal(err)
	}
	path := r.recordPath(rec.ID)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte(`{"extra_field": true,`), data[1:]...)
	if err := os.WriteFile(path, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get(ctx, rec.ID); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unknown field should be invalid, got %v", err)
	}
}

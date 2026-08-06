package store

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

func newTestStore(t *testing.T) *Store {
	t.Helper()
	layout, err := paths.NewLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	s, err := New(layout, lock.NewFlock(layout.Locks))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func stageTree(t *testing.T, s *Store, files map[string]string) (string, domain.Digest) {
	t.Helper()
	dir, err := s.NewStaging("test")
	if err != nil {
		t.Fatal(err)
	}
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	digest, _, err := DigestTree(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir, digest
}

func TestDigestTreeDeterministic(t *testing.T) {
	s := newTestStore(t)
	files := map[string]string{"a.txt": "alpha", "sub/b.txt": "beta"}
	_, d1 := stageTree(t, s, files)
	_, d2 := stageTree(t, s, files)
	if d1 != d2 {
		t.Fatalf("identical trees digest differently: %s vs %s", d1, d2)
	}
	_, d3 := stageTree(t, s, map[string]string{"a.txt": "alpha", "sub/b.txt": "CHANGED"})
	if d1 == d3 {
		t.Fatal("different content, same digest")
	}
}

func TestDigestTreeRejectsSymlink(t *testing.T) {
	s := newTestStore(t)
	dir, _ := stageTree(t, s, map[string]string{"a.txt": "x"})
	if err := os.Symlink("a.txt", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := DigestTree(dir); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("symlink should be rejected, got %v", err)
	}
}

func TestPublishIdempotentAndConflict(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	files := map[string]string{"plan.json": "{}\n"}

	staged, digest := stageTree(t, s, files)
	obj, err := s.Publish(ctx, SnapshotObject, staged, digest)
	if err != nil {
		t.Fatal(err)
	}
	if exists, _ := s.Exists(ctx, SnapshotObject, digest); !exists {
		t.Fatal("published object does not exist")
	}
	if _, err := os.Stat(filepath.Join(obj.Path, "plan.json")); err != nil {
		t.Fatal(err)
	}

	// Identical republication is success and consumes the staging dir.
	staged2, _ := stageTree(t, s, files)
	if _, err := s.Publish(ctx, SnapshotObject, staged2, digest); err != nil {
		t.Fatalf("idempotent publish failed: %v", err)
	}
	if _, err := os.Stat(staged2); !os.IsNotExist(err) {
		t.Fatal("staging not cleaned up after idempotent publish")
	}

	// Claiming a digest the content does not match is invalid.
	staged3, _ := stageTree(t, s, map[string]string{"plan.json": "other\n"})
	if _, err := s.Publish(ctx, SnapshotObject, staged3, digest); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("mismatched digest should be invalid, got %v", err)
	}
}

func TestOpenLease(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	staged, digest := stageTree(t, s, map[string]string{"f": "x"})
	if _, err := s.Publish(ctx, SnapshotObject, staged, digest); err != nil {
		t.Fatal(err)
	}
	lease, err := s.Open(ctx, SnapshotObject, digest)
	if err != nil {
		t.Fatal(err)
	}
	if lease.Object.Digest != digest {
		t.Fatal("lease has wrong digest")
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal("double close should be a no-op")
	}
	missing := domain.SHA256([]byte("missing"))
	if _, err := s.Open(ctx, SnapshotObject, missing); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing object should be not-found, got %v", err)
	}
}

func TestCandidateRecordRoundTrip(t *testing.T) {
	s := newTestStore(t)
	rec := CandidateRecord{
		Digest:    domain.SHA256([]byte("candidate")),
		ProjectID: "abcdefghijklmnopqrstuvwxyz",
		Kind:      RuntimeCandidate,
		Snapshot:  domain.SHA256([]byte("snap")),
		Plan:      domain.SHA256([]byte("plan")),
		CreatedAt: time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC),
	}
	if err := s.WriteCandidateRecord(rec); err != nil {
		t.Fatal(err)
	}
	// Identical rewrite is success.
	if err := s.WriteCandidateRecord(rec); err != nil {
		t.Fatal(err)
	}
	// Different content under the same digest is a conflict.
	changed := rec
	changed.Kind = BuildCandidate
	if err := s.WriteCandidateRecord(changed); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("conflicting record should fail, got %v", err)
	}
	got, err := s.ReadCandidateRecord(rec.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProjectID != rec.ProjectID || got.Kind != rec.Kind || got.Snapshot != rec.Snapshot {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if _, err := s.ReadCandidateRecord(domain.SHA256([]byte("nope"))); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing record should be not-found, got %v", err)
	}
}

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.json")
	if err := WriteFileAtomic(p, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(p, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil || string(data) != "two" {
		t.Fatalf("read %q, %v", data, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
}

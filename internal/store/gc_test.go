package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"vibe/internal/domain"
)

func publishTree(t *testing.T, s *Store, kind ObjectKind, files map[string]string) domain.Digest {
	t.Helper()
	dir, digest := stageTree(t, s, files)
	if _, err := s.Publish(context.Background(), kind, dir, digest); err != nil {
		t.Fatal(err)
	}
	return digest
}

func TestListObjectsSkipsRecords(t *testing.T) {
	s := newTestStore(t)
	d1 := publishTree(t, s, SnapshotObject, map[string]string{"a": "1"})
	d2 := publishTree(t, s, SnapshotObject, map[string]string{"a": "2"})
	// A record file beside candidate objects must not list as an object.
	d3 := publishTree(t, s, CandidateObject, map[string]string{"plan.json": "{}"})
	if err := s.WriteCandidateRecord(CandidateRecord{Digest: d3, ProjectID: "p1", Kind: RuntimeCandidate}); err != nil {
		t.Fatal(err)
	}

	snaps, err := s.ListObjects(SnapshotObject)
	if err != nil || len(snaps) != 2 {
		t.Fatalf("snapshots: %v, %v", snaps, err)
	}
	got := map[domain.Digest]bool{snaps[0]: true, snaps[1]: true}
	if !got[d1] || !got[d2] {
		t.Fatalf("missing published snapshots: %v", snaps)
	}
	cands, err := s.ListObjects(CandidateObject)
	if err != nil || len(cands) != 1 || cands[0] != d3 {
		t.Fatalf("candidates: %v, %v", cands, err)
	}
}

func TestRemoveObjectRefusesLeases(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	d := publishTree(t, s, SnapshotObject, map[string]string{"a": "held"})

	lease, err := s.Open(ctx, SnapshotObject, d)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveObject(ctx, SnapshotObject, d); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("leased removal should conflict, got %v", err)
	}
	if ok, _ := s.Exists(ctx, SnapshotObject, d); !ok {
		t.Fatal("leased object vanished")
	}
	lease.Close()

	if err := s.RemoveObject(ctx, SnapshotObject, d); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Exists(ctx, SnapshotObject, d); ok {
		t.Fatal("object survived removal")
	}
}

func TestRemoveObjectDropsRecord(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	d := publishTree(t, s, CandidateObject, map[string]string{"plan.json": "{}"})
	if err := s.WriteCandidateRecord(CandidateRecord{Digest: d, ProjectID: "p1", Kind: RuntimeCandidate}); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveObject(ctx, CandidateObject, d); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadCandidateRecord(d); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("record survived removal: %v", err)
	}
	// Orphan record (object already gone): removal still cleans it.
	if err := s.WriteCandidateRecord(CandidateRecord{Digest: d, ProjectID: "p1", Kind: RuntimeCandidate}); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveObject(ctx, CandidateObject, d); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadCandidateRecord(d); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("orphan record survived: %v", err)
	}
}

func TestObjectStat(t *testing.T) {
	s := newTestStore(t)
	d := publishTree(t, s, SnapshotObject, map[string]string{"a": "12345"})
	stat, err := s.ObjectStat(SnapshotObject, d)
	if err != nil || stat.Bytes != 5 || stat.ModTime.IsZero() {
		t.Fatalf("stat: %+v, %v", stat, err)
	}
	if _, err := s.ObjectStat(SnapshotObject, domain.SHA256([]byte("missing"))); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing stat: %v", err)
	}
}

func TestCleanStaging(t *testing.T) {
	s := newTestStore(t)
	old, err := s.NewStaging("old")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "junk"), []byte("xxxx"), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	fresh, err := s.NewStaging("fresh")
	if err != nil {
		t.Fatal(err)
	}

	// Dry run counts without removing.
	n, bytes, err := s.CleanStaging(time.Now().Add(-time.Hour), true)
	if err != nil || n != 1 || bytes != 4 {
		t.Fatalf("dry clean: n=%d bytes=%d err=%v", n, bytes, err)
	}
	if _, err := os.Lstat(old); err != nil {
		t.Fatal("dry run removed the entry")
	}

	n, _, err = s.CleanStaging(time.Now().Add(-time.Hour), false)
	if err != nil || n != 1 {
		t.Fatalf("clean: n=%d err=%v", n, err)
	}
	if _, err := os.Lstat(old); !os.IsNotExist(err) {
		t.Fatal("old staging entry survived")
	}
	if _, err := os.Lstat(fresh); err != nil {
		t.Fatal("fresh staging entry was removed")
	}
}

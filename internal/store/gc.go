package store

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/lock"
)

// Explicit garbage collection. The store never removes anything on its
// own: callers (app.GC) decide what is unreferenced; these primitives
// make each removal safe against concurrent use — a shared lease
// (Open) defeats the exclusive flock taken here, and the tree leaves
// its published path in one rename before deletion.

func (s *Store) kindDir(kind ObjectKind) (string, error) {
	switch kind {
	case ArtifactObject:
		return s.layout.Artifacts, nil
	case CandidateObject:
		return s.layout.Candidates, nil
	case SnapshotObject:
		return s.layout.Snapshots, nil
	default:
		return "", fmt.Errorf("%w: object kind %q", domain.ErrInvalid, kind)
	}
}

// ListObjects returns every published digest of one kind.
func (s *Store) ListObjects(kind ObjectKind) ([]domain.Digest, error) {
	dir, err := s.kindDir(kind)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []domain.Digest
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		d, err := domain.ParseDigest("sha256:" + e.Name())
		if err != nil {
			continue // records and strays are not objects
		}
		out = append(out, d)
	}
	return out, nil
}

// ObjectStat reports one published object's age anchor and total size.
type ObjectStat struct {
	ModTime time.Time
	Bytes   int64
}

func (s *Store) ObjectStat(kind ObjectKind, digest domain.Digest) (ObjectStat, error) {
	p, err := s.objectPath(kind, digest)
	if err != nil {
		return ObjectStat{}, err
	}
	info, err := os.Lstat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return ObjectStat{}, fmt.Errorf("%w: %s %s", domain.ErrNotFound, kind, digest)
		}
		return ObjectStat{}, err
	}
	return ObjectStat{ModTime: info.ModTime(), Bytes: treeBytes(p)}, nil
}

// RemoveObject unpublishes one object: it refuses anything currently
// leased (ErrConflict), moves the tree out of its published path in a
// single rename, deletes it, and drops the record file. A missing
// object with a lingering record cleans up just the record.
func (s *Store) RemoveObject(ctx context.Context, kind ObjectKind, digest domain.Digest) error {
	p, err := s.objectPath(kind, digest)
	if err != nil {
		return err
	}
	held, err := s.locks.Acquire(ctx, lock.Object(string(kind), digest.Hex()))
	if err != nil {
		return err
	}
	defer held.Release()

	dir, err := os.OpenFile(p, os.O_RDONLY, 0)
	switch {
	case os.IsNotExist(err):
		return s.removeRecord(kind, digest) // orphan record
	case err != nil:
		return fmt.Errorf("gc %s: %w", kind, err)
	}
	defer dir.Close()
	if err := syscall.Flock(int(dir.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("%w: %s %s is in use", domain.ErrConflict, kind, digest)
	}

	trash, err := os.MkdirTemp(s.layout.Staging, "gc-"+string(kind)+"-*")
	if err != nil {
		return fmt.Errorf("gc %s: %w", kind, err)
	}
	if err := os.Rename(p, filepath.Join(trash, "obj")); err != nil {
		os.RemoveAll(trash)
		return fmt.Errorf("gc %s: %w", kind, err)
	}
	if err := os.RemoveAll(trash); err != nil {
		return fmt.Errorf("gc %s: %w", kind, err)
	}
	return s.removeRecord(kind, digest)
}

func (s *Store) removeRecord(kind ObjectKind, digest domain.Digest) error {
	var path string
	switch kind {
	case ArtifactObject:
		path = s.artifactRecordPath(digest)
	case CandidateObject:
		path = s.candidateRecordPath(digest)
	default:
		return nil // snapshots have no records
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ListCandidateRecords loads every candidate record.
func (s *Store) ListCandidateRecords() ([]CandidateRecord, error) {
	entries, err := os.ReadDir(s.layout.Candidates)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var records []CandidateRecord
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".record.json")
		if !ok || e.IsDir() {
			continue
		}
		digest, err := domain.ParseDigest("sha256:" + name)
		if err != nil {
			continue
		}
		rec, err := s.ReadCandidateRecord(digest)
		if err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	return records, nil
}

// CleanStaging removes staging leftovers older than the cutoff (or
// only counts them when dryRun) and reports entries and bytes.
func (s *Store) CleanStaging(olderThan time.Time, dryRun bool) (int, int64, error) {
	entries, err := os.ReadDir(s.layout.Staging)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	removed, bytes := 0, int64(0)
	for _, e := range entries {
		p := filepath.Join(s.layout.Staging, e.Name())
		info, err := os.Lstat(p)
		if err != nil || !info.ModTime().Before(olderThan) {
			continue
		}
		bytes += treeBytes(p)
		if !dryRun {
			if err := os.RemoveAll(p); err != nil {
				return removed, bytes, err
			}
		}
		removed++
	}
	return removed, bytes, nil
}

// treeBytes sums regular-file sizes; errors count as zero — sizes are
// reporting, not accounting.
func treeBytes(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // best-effort size reporting
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

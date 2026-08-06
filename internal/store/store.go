// Package store owns immutable host objects: release artifacts, build
// and runtime candidates, and input snapshots, all addressed by
// SHA-256 tree digest. Content is written to same-filesystem staging
// and published with one atomic rename; identical republication is
// success, conflicting content is an error.
package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"vibe/internal/domain"
	"vibe/internal/lock"
	"vibe/internal/paths"
)

type ObjectKind string

const (
	ArtifactObject  ObjectKind = "artifact"
	CandidateObject ObjectKind = "candidate"
	SnapshotObject  ObjectKind = "snapshot"
)

// Object is one published immutable directory tree.
type Object struct {
	Kind   ObjectKind
	Digest domain.Digest
	Path   string
}

type Store struct {
	layout paths.Layout
	locks  lock.Locker
}

func New(layout paths.Layout, locks lock.Locker) (*Store, error) {
	if err := layout.Validate(); err != nil {
		return nil, err
	}
	if locks == nil {
		return nil, fmt.Errorf("%w: store requires a locker", domain.ErrInvalid)
	}
	return &Store{layout: layout, locks: locks}, nil
}

func (s *Store) objectPath(kind ObjectKind, digest domain.Digest) (string, error) {
	if digest.IsZero() {
		return "", fmt.Errorf("%w: zero digest", domain.ErrInvalid)
	}
	switch kind {
	case ArtifactObject:
		return filepath.Join(s.layout.Artifacts, digest.Hex()), nil
	case CandidateObject:
		return filepath.Join(s.layout.Candidates, digest.Hex()), nil
	case SnapshotObject:
		return filepath.Join(s.layout.Snapshots, digest.Hex()), nil
	default:
		return "", fmt.Errorf("%w: object kind %q", domain.ErrInvalid, kind)
	}
}

// Publish verifies that the staged directory's tree digest matches
// digest, syncs it, and renames it into place. Publishing content that
// already exists is success; the staging directory is then discarded.
func (s *Store) Publish(ctx context.Context, kind ObjectKind, staged string, digest domain.Digest) (Object, error) {
	final, err := s.objectPath(kind, digest)
	if err != nil {
		return Object{}, err
	}
	got, _, err := DigestTree(staged)
	if err != nil {
		return Object{}, fmt.Errorf("verify staged %s: %w", kind, err)
	}
	if got != digest {
		return Object{}, fmt.Errorf("%w: staged %s digest %s does not match claimed %s", domain.ErrInvalid, kind, got, digest)
	}

	held, err := s.locks.Acquire(ctx, lock.Object(string(kind), digest.Hex()))
	if err != nil {
		return Object{}, err
	}
	defer held.Release()

	if _, err := os.Lstat(final); err == nil {
		// Identical by construction: the path is the content address and
		// the staged content was just verified against it.
		os.RemoveAll(staged)
		return Object{Kind: kind, Digest: digest, Path: final}, nil
	}
	if err := syncTree(staged); err != nil {
		return Object{}, fmt.Errorf("sync staged %s: %w", kind, err)
	}
	if err := os.Rename(staged, final); err != nil {
		if errors.Is(err, os.ErrExist) {
			os.RemoveAll(staged)
			return Object{Kind: kind, Digest: digest, Path: final}, nil
		}
		return Object{}, fmt.Errorf("publish %s: %w", kind, err)
	}
	if err := syncDir(filepath.Dir(final)); err != nil {
		return Object{}, fmt.Errorf("publish %s: %w", kind, err)
	}
	return Object{Kind: kind, Digest: digest, Path: final}, nil
}

// Exists reports whether the object is published.
func (s *Store) Exists(ctx context.Context, kind ObjectKind, digest domain.Digest) (bool, error) {
	p, err := s.objectPath(kind, digest)
	if err != nil {
		return false, err
	}
	if _, err := os.Lstat(p); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// NewStaging creates a fresh mode-0700 staging directory on the store
// filesystem. Callers either Publish it or remove it; orphans are
// reclaimed by explicit GC.
func (s *Store) NewStaging(pattern string) (string, error) {
	dir, err := os.MkdirTemp(s.layout.Staging, pattern+"-*")
	if err != nil {
		return "", fmt.Errorf("create staging: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		os.RemoveAll(dir)
		return "", fmt.Errorf("create staging: %w", err)
	}
	return dir, nil
}

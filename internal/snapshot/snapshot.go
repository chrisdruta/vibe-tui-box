// Package snapshot freezes workspace inputs into immutable,
// content-addressed host state before any validation, diff, or Docker
// operation reads them.
//
// The walker opens every path relative to the canonical project-root
// directory via os.Root, which confines resolution beneath the root
// descriptor (openat2(RESOLVE_BENEATH) on Linux, openat component walks
// on macOS). Symlinks, hard links, and special files are rejected
// outright, and file metadata is re-checked after copying so concurrent
// mutation aborts the snapshot.
package snapshot

import (
	"context"
	"fmt"
	"os"

	"vibe/internal/domain"
	"vibe/internal/paths"
	"vibe/internal/store"
)

// Entry selects one workspace path for the snapshot. Source is
// workspace-relative; Dest is the slash-relative location inside the
// snapshot; Optional entries may be absent.
type Entry struct {
	Source   string
	Dest     string
	Optional bool
}

// Spec is one snapshot request: an explicit allowlist, never a
// recursive copy of the repository.
type Spec struct {
	ProjectRoot paths.Root
	Entries     []Entry
	Limits      Limits
}

// File is one regular file captured in a snapshot.
type File struct {
	Path   string // slash-relative inside the snapshot
	Size   int64
	Mode   uint32 // normalized 0644/0755
	Digest domain.Digest
}

// Result describes one published snapshot.
type Result struct {
	Digest domain.Digest
	Path   string // published immutable directory
	Files  []File
	Bytes  int64
}

// Service creates snapshot content and publishes it through the store.
type Service struct {
	store *store.Store
}

func NewService(st *store.Store) (*Service, error) {
	if st == nil {
		return nil, fmt.Errorf("%w: snapshot service requires a store", domain.ErrInvalid)
	}
	return &Service{store: st}, nil
}

// Create walks the spec's entries into store staging, digests the tree,
// and publishes it as an immutable snapshot object.
func (s *Service) Create(ctx context.Context, spec Spec) (Result, error) {
	if len(spec.Entries) == 0 {
		return Result{}, fmt.Errorf("%w: snapshot spec has no entries", domain.ErrInvalid)
	}
	staging, err := s.store.NewStaging("snapshot")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(staging)

	w, err := newWalker(spec.ProjectRoot, staging, spec.Limits)
	if err != nil {
		return Result{}, err
	}
	defer w.Close()
	for _, entry := range spec.Entries {
		if err := ctx.Err(); err != nil {
			return Result{}, fmt.Errorf("%w: snapshot: %v", domain.ErrCanceled, context.Cause(ctx))
		}
		if err := w.copyEntry(entry); err != nil {
			return Result{}, err
		}
	}

	digest, entries, err := store.DigestTree(staging)
	if err != nil {
		return Result{}, fmt.Errorf("digest snapshot: %w", err)
	}
	obj, err := s.store.Publish(ctx, store.SnapshotObject, staging, digest)
	if err != nil {
		return Result{}, err
	}

	res := Result{Digest: obj.Digest, Path: obj.Path}
	for _, e := range entries {
		if e.Type == 'f' {
			res.Files = append(res.Files, File{Path: e.Path, Size: e.Size, Mode: e.Mode, Digest: e.Digest})
			res.Bytes += e.Size
		}
	}
	return res, nil
}

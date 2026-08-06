//go:build unix

package store

import (
	"context"
	"fmt"
	"os"
	"syscall"

	"vibe/internal/domain"
)

// Lease marks a published object as in use. It holds a shared flock on
// the object directory plus an open descriptor, so explicit GC (which
// takes the exclusive lock) cannot remove a leased object, and the
// content stays reachable through the descriptor even mid-removal.
type Lease struct {
	Object Object
	dir    *os.File
}

// Open leases a published object for use.
func (s *Store) Open(ctx context.Context, kind ObjectKind, digest domain.Digest) (*Lease, error) {
	p, err := s.objectPath(kind, digest)
	if err != nil {
		return nil, err
	}
	dir, err := os.OpenFile(p, os.O_RDONLY, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s %s", domain.ErrNotFound, kind, digest)
		}
		return nil, fmt.Errorf("open %s: %w", kind, err)
	}
	if err := syscall.Flock(int(dir.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		dir.Close()
		return nil, fmt.Errorf("%w: %s %s is exclusively locked", domain.ErrConflict, kind, digest)
	}
	return &Lease{
		Object: Object{Kind: kind, Digest: digest, Path: p},
		dir:    dir,
	}, nil
}

func (l *Lease) Close() error {
	if l.dir == nil {
		return nil
	}
	dir := l.dir
	l.dir = nil
	return dir.Close() // closing releases the shared flock
}

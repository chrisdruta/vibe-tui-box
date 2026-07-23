//go:build unix

package lock

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// FlockLocker implements Locker with flock(2) files in one directory.
type FlockLocker struct {
	dir string
}

// NewFlock returns a Locker writing lock files under dir, which must
// already exist.
func NewFlock(dir string) *FlockLocker {
	return &FlockLocker{dir: dir}
}

// pollInterval bounds how long a canceled caller waits, since flock has
// no native cancellation.
const pollInterval = 50 * time.Millisecond

func (l *FlockLocker) Acquire(ctx context.Context, name string) (Lock, error) {
	path := filepath.Join(l.dir, name+".lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock %s: %w", name, err)
	}
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return &flock{file: f}, nil
		}
		if err != syscall.EWOULDBLOCK {
			f.Close()
			return nil, fmt.Errorf("acquire lock %s: %w", name, err)
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, fmt.Errorf("acquire lock %s: %w", name, context.Cause(ctx))
		case <-time.After(pollInterval):
		}
	}
}

type flock struct {
	file *os.File
}

func (l *flock) Release() error {
	// Closing the descriptor drops the flock; unlock explicitly first so
	// close errors cannot leave the lock held.
	if err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN); err != nil {
		l.file.Close()
		return fmt.Errorf("release lock: %w", err)
	}
	return l.file.Close()
}

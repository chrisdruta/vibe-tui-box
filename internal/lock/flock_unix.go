//go:build unix

package lock

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// FlockLocker implements Locker with flock(2) files in one directory.
//
// Every acquisition first passes through the name's intent gate and
// holds it until the main lock is acquired. Without the gate, the
// nonblocking poll loop below makes exclusive-waiter starvation
// systematic under overlapping shared holders: a poller only wins if
// the lock happens to be free at a poll instant. With it, an exclusive
// waiter that holds intent while draining existing shared holders
// cannot be overtaken by newly arriving ones — they queue at intent.
type FlockLocker struct {
	dir string

	mu    sync.Mutex
	gates map[string]*intentGate
}

// NewFlock returns a Locker writing lock files under dir, which must
// already exist.
func NewFlock(dir string) *FlockLocker {
	return &FlockLocker{dir: dir, gates: map[string]*intentGate{}}
}

// pollInterval bounds how long a canceled caller waits, since flock has
// no native cancellation.
const pollInterval = 50 * time.Millisecond

func (l *FlockLocker) Acquire(ctx context.Context, name string) (Lock, error) {
	return l.acquire(ctx, name, syscall.LOCK_EX)
}

func (l *FlockLocker) AcquireShared(ctx context.Context, name string) (Lock, error) {
	return l.acquire(ctx, name, syscall.LOCK_SH)
}

func (l *FlockLocker) acquire(ctx context.Context, name string, how int) (Lock, error) {
	gate, err := l.gate(name)
	if err != nil {
		return nil, fmt.Errorf("acquire intent %s: %w", name, err)
	}
	grant, err := gate.acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire intent %s: %w", name, err)
	}
	lk, err := l.poll(ctx, name, how)
	grant.release()
	return lk, err
}

func (l *FlockLocker) gate(name string) (*intentGate, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if g, ok := l.gates[name]; ok {
		return g, nil
	}
	f, err := os.OpenFile(filepath.Join(l.dir, name+".intent.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	g := newIntentGate(f)
	l.gates[name] = g
	return g, nil
}

func (l *FlockLocker) poll(ctx context.Context, name string, how int) (Lock, error) {
	path := filepath.Join(l.dir, name+".lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock %s: %w", name, err)
	}
	for {
		err := syscall.Flock(int(f.Fd()), how|syscall.LOCK_NB)
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

// intentGate is the per-process singleton intent waiter for one lock
// name. Callers queue FIFO on the mutex-protected coordinator state; a
// single worker goroutine performs the blocking flock, so a canceled
// caller holds no thread, descriptor, or kernel wait slot of its own —
// the process contributes at most one kernel waiter and one descriptor
// regardless of request or cancellation volume. The kernel lock is
// released after every logical holder and reacquired for the next, so
// local callers are never batched under one kernel acquisition and
// other processes contend at each hand-off. Kernel wake order among
// processes is unspecified; that is an availability property only — no
// correctness claim depends on who wins.
type intentGate struct {
	file *os.File

	mu           sync.Mutex
	queue        []*intentReq
	active       bool          // worker is draining the queue
	broken       error         // a failed kernel unlock poisons the gate
	acquisitions int           // kernel flock attempts, for tests of the per-holder release
	wake         chan struct{} // buffered 1: signals the worker
}

type intentReq struct {
	granted  chan error    // buffered 1: worker's grant (nil) or acquisition error
	done     chan struct{} // closed by the holder to release intent
	canceled bool          // set under gate.mu; worker skips it
}

func newIntentGate(f *os.File) *intentGate {
	g := &intentGate{file: f, wake: make(chan struct{}, 1)}
	go g.worker()
	return g
}

func (g *intentGate) acquire(ctx context.Context) (*intentReq, error) {
	req := &intentReq{granted: make(chan error, 1), done: make(chan struct{})}
	g.mu.Lock()
	if g.broken != nil {
		err := g.broken
		g.mu.Unlock()
		return nil, err
	}
	g.queue = append(g.queue, req)
	if !g.active {
		g.active = true
		select {
		case g.wake <- struct{}{}:
		default:
		}
	}
	g.mu.Unlock()

	select {
	case err := <-req.granted:
		if err != nil {
			return nil, err
		}
		return req, nil
	case <-ctx.Done():
		// The grant may have raced the cancellation: both the worker's
		// grant hand-off and this check run under gate.mu, so exactly
		// one side wins. A stale grant is released, never kept.
		g.mu.Lock()
		select {
		case err := <-req.granted:
			g.mu.Unlock()
			if err == nil {
				req.release()
			}
		default:
			req.canceled = true
			g.mu.Unlock()
		}
		return nil, context.Cause(ctx)
	}
}

// release hands intent back; the worker unlocks the kernel flock and
// serves the next queued caller under a fresh acquisition.
func (r *intentReq) release() {
	close(r.done)
}

func (g *intentGate) worker() {
	for range g.wake {
		for g.pendingWork() {
			// Blocking acquisition: this goroutine's thread sits in the
			// kernel wait queue, contending symmetrically with other
			// processes at every hand-off.
			g.mu.Lock()
			g.acquisitions++
			g.mu.Unlock()
			var flockErr error
			for {
				flockErr = syscall.Flock(int(g.file.Fd()), syscall.LOCK_EX)
				if flockErr != syscall.EINTR {
					break
				}
			}
			head, release := g.grantHead(flockErr)
			if release && !g.unlock() {
				return // gate poisoned; acquire() now fails fast
			}
			if head == nil {
				continue
			}
			<-head.done // holder releases once the main lock is held
			if !g.unlock() {
				return
			}
		}
	}
}

// unlock releases the kernel flock between logical holders. A failure
// poisons the gate — every queued and future acquisition fails fast —
// because an unreleased intent lock would wedge other processes with
// no detection otherwise. The descriptor is closed as well: closing
// drops the flock even when an explicit LOCK_UN failed, so a possibly
// still-held kernel lock is never left behind an abandoned fd to
// wedge other processes or locker instances.
func (g *intentGate) unlock() bool {
	if err := syscall.Flock(int(g.file.Fd()), syscall.LOCK_UN); err != nil {
		g.mu.Lock()
		g.broken = fmt.Errorf("release intent flock: %w", err)
		for _, r := range g.queue {
			if !r.canceled {
				r.granted <- g.broken
			}
		}
		g.queue = nil
		g.active = false
		g.file.Close()
		g.mu.Unlock()
		return false
	}
	return true
}

// pendingWork reports whether live requests remain, marking the worker
// idle (so the next enqueue wakes it) when none do.
func (g *intentGate) pendingWork() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.dropCanceled()
	if len(g.queue) == 0 {
		g.active = false
		return false
	}
	return true
}

// grantHead hands the acquisition outcome to the head request entirely
// under the mutex, so cancellation cannot race the hand-off: a caller
// either receives the grant or marks itself canceled first, never
// both. Returns the head only when it now holds intent (the worker
// must wait for its release). When the head this acquisition was for
// canceled while the worker was blocked, the kernel lock is NOT
// transferred to the next live caller — release says true and the
// worker reacquires freshly, so other processes contend at the
// cancellation seam exactly as at every other hand-off.
func (g *intentGate) grantHead(flockErr error) (head *intentReq, release bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	dropped := g.dropCanceled()
	if len(g.queue) == 0 || dropped > 0 {
		if len(g.queue) == 0 {
			g.active = false
		}
		return nil, flockErr == nil
	}
	head = g.queue[0]
	g.queue = g.queue[1:]
	if flockErr != nil {
		head.granted <- fmt.Errorf("intent flock: %w", flockErr)
		return nil, false
	}
	head.granted <- nil
	return head, false
}

func (g *intentGate) dropCanceled() int {
	dropped := 0
	for len(g.queue) > 0 && g.queue[0].canceled {
		g.queue = g.queue[1:]
		dropped++
	}
	return dropped
}

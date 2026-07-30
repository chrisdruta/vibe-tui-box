package lock

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestAcquireRelease(t *testing.T) {
	l := NewFlock(t.TempDir())
	ctx := context.Background()

	held, err := l.Acquire(ctx, Project("p1"))
	if err != nil {
		t.Fatal(err)
	}

	// A second exclusive acquire must block until the first releases.
	timeout, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if _, err := l.Acquire(timeout, Project("p1")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("contended acquire should time out, got %v", err)
	}

	// A different name is independent.
	other, err := l.Acquire(ctx, Project("p2"))
	if err != nil {
		t.Fatal(err)
	}
	other.Release()

	if err := held.Release(); err != nil {
		t.Fatal(err)
	}
	reacquired, err := l.Acquire(ctx, Project("p1"))
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	reacquired.Release()
}

func TestLockNamesEncodeOrder(t *testing.T) {
	names := []string{StoreGlobal(), Object("candidate", "ff"), Project("id")}
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Fatalf("lock order prefixes not ascending: %q >= %q", names[i-1], names[i])
		}
	}
}

// TestSharedExclusiveExclusion pins the reader/writer semantics:
// shared holders coexist, exclusive excludes both directions.
func TestSharedExclusiveExclusion(t *testing.T) {
	l := NewFlock(t.TempDir())
	ctx := context.Background()

	s1, err := l.AcquireShared(ctx, StoreGlobal())
	if err != nil {
		t.Fatal(err)
	}
	s2, err := l.AcquireShared(ctx, StoreGlobal())
	if err != nil {
		t.Fatalf("shared holders must coexist: %v", err)
	}

	timeout, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	if _, err := l.Acquire(timeout, StoreGlobal()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("exclusive under shared holders should time out, got %v", err)
	}
	cancel()

	s1.Release()
	s2.Release()
	ex, err := l.Acquire(ctx, StoreGlobal())
	if err != nil {
		t.Fatal(err)
	}
	timeout, cancel = context.WithTimeout(ctx, 300*time.Millisecond)
	if _, err := l.AcquireShared(timeout, StoreGlobal()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shared under exclusive holder should time out, got %v", err)
	}
	cancel()
	ex.Release()
}

// TestExclusiveNotStarvedBySharedChurn is the starvation regression:
// overlapping shared holders churn continuously, and the exclusive
// acquirer must still get in — new shared entrants queue at the intent
// gate behind it instead of overtaking the poll loop.
func TestExclusiveNotStarvedBySharedChurn(t *testing.T) {
	l := NewFlock(t.TempDir())
	ctx := context.Background()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				lk, err := l.AcquireShared(ctx, StoreGlobal())
				if err != nil {
					return
				}
				time.Sleep(20 * time.Millisecond)
				lk.Release()
			}
		}()
	}
	// Give the churn a head start so the lock is continuously held.
	time.Sleep(100 * time.Millisecond)

	exCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	lk, err := l.Acquire(exCtx, StoreGlobal())
	if err != nil {
		t.Fatalf("exclusive acquisition starved by shared churn: %v", err)
	}
	lk.Release()
	close(stop)
	wg.Wait()
}

// TestCanceledIntentWaiterDrains pins that a canceled waiter leaves no
// residue: after the holder releases, the next acquisition succeeds.
func TestCanceledIntentWaiterDrains(t *testing.T) {
	l := NewFlock(t.TempDir())
	ctx := context.Background()

	held, err := l.Acquire(ctx, StoreGlobal())
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		timeout, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
		if _, err := l.Acquire(timeout, StoreGlobal()); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("blocked acquire should time out, got %v", err)
		}
		cancel()
	}
	held.Release()

	after, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	lk, err := l.Acquire(after, StoreGlobal())
	if err != nil {
		t.Fatalf("acquisition after canceled waiters must succeed: %v", err)
	}
	lk.Release()
}

// TestCanceledHeadHandsOffToLiveSuccessor pins the cancellation seam:
// a waiter that cancels while at the head of the intent queue must not
// wedge or absorb the grant — the next live waiter acquires once the
// current holder releases.
func TestCanceledHeadHandsOffToLiveSuccessor(t *testing.T) {
	l := NewFlock(t.TempDir())
	ctx := context.Background()

	held, err := l.Acquire(ctx, StoreGlobal())
	if err != nil {
		t.Fatal(err)
	}

	// Waiter A blocks polling the main lock (holding intent); cancel it.
	aCtx, aCancel := context.WithTimeout(ctx, 200*time.Millisecond)
	aDone := make(chan error, 1)
	go func() {
		_, err := l.Acquire(aCtx, StoreGlobal())
		aDone <- err
	}()
	// Waiter B queues behind A at the intent gate.
	bDone := make(chan error, 1)
	bCtx, bCancel := context.WithTimeout(ctx, 5*time.Second)
	defer bCancel()
	go func() {
		lk, err := l.Acquire(bCtx, StoreGlobal())
		if err == nil {
			lk.Release()
		}
		bDone <- err
	}()

	if err := <-aDone; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiter A should have timed out, got %v", err)
	}
	aCancel()
	held.Release()
	if err := <-bDone; err != nil {
		t.Fatalf("live successor must acquire after the canceled head: %v", err)
	}
}

// TestIntentGateCanceledHeadReacquires pins, at the gate level, that a
// head canceled while the worker was blocked does NOT hand its kernel
// acquisition to the next live waiter: the worker releases and
// reacquires, observable as a second flock attempt.
func TestIntentGateCanceledHeadReacquires(t *testing.T) {
	dir := t.TempDir()
	open := func() *os.File {
		f, err := os.OpenFile(filepath.Join(dir, "gate.lock"), os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	holder := newIntentGate(open())
	gate := newIntentGate(open())
	ctx := context.Background()

	hg, err := holder.acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// A queues on the contended gate; its worker blocks in flock.
	aCtx, aCancel := context.WithCancel(ctx)
	aDone := make(chan error, 1)
	go func() {
		_, err := gate.acquire(aCtx)
		aDone <- err
	}()
	time.Sleep(100 * time.Millisecond)
	// B queues behind A, then A cancels while the worker is parked.
	bDone := make(chan *intentReq, 1)
	go func() {
		r, err := gate.acquire(ctx)
		if err != nil {
			t.Error(err)
		}
		bDone <- r
	}()
	time.Sleep(50 * time.Millisecond)
	aCancel()
	if err := <-aDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter should report cancellation, got %v", err)
	}

	hg.release()
	r := <-bDone
	gate.mu.Lock()
	n := gate.acquisitions
	gate.mu.Unlock()
	if n < 2 {
		t.Fatalf("canceled head must trigger release+reacquire, saw %d flock attempts", n)
	}
	r.release()
}

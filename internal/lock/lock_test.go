package lock

import (
	"context"
	"errors"
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
	names := []string{StoreGlobal(), Object("candidate", "ff"), Project("id"), BrokerRequest("id", "req")}
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Fatalf("lock order prefixes not ascending: %q >= %q", names[i-1], names[i])
		}
	}
}

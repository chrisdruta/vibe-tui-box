package app

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
)

func TestWatchRequiresInputsAndDeps(t *testing.T) {
	a, _ := newTestApp(t)
	a.deps.Tmux = &recordingTmux{}
	ctx := context.Background()

	if err := a.Watch(ctx, WatchRequest{}); err == nil {
		t.Fatal("missing cache and project must be invalid")
	}
	if err := a.Watch(ctx, WatchRequest{CacheDir: t.TempDir()}); err == nil {
		t.Fatal("missing project must be invalid")
	}
	a.deps.Tmux = nil
	if err := a.Watch(ctx, WatchRequest{CacheDir: t.TempDir(), Project: "p"}); err == nil {
		t.Fatal("nil tmux must be unavailable")
	}
}

func TestWatchLockIsExclusivePerPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch.p.lock")
	first, err := acquireWatchLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireWatchLock(path); err == nil {
		t.Fatal("second acquire must fail while the first is held")
	}
	first.Close()
	second, err := acquireWatchLock(path)
	if err != nil {
		t.Fatalf("the lock must free on close: %v", err)
	}
	second.Close()
}

func TestWatchPublishWritesCacheAndBumpsStateSerial(t *testing.T) {
	a, _ := newTestApp(t)
	rt := &recordingTmux{}
	a.deps.Tmux = rt
	cache := t.TempDir()

	a.watchPublish(context.Background(), WatchRequest{CacheDir: cache, Project: "p"})

	// An empty fleet is still a publish: the cache file exists (empty —
	// the read side treats that as "no agents", not "no cache") and the
	// FRAME serial moved. The engine serial must stay put: bumping it
	// would tell the sidebar to refetch what was just fetched.
	if _, err := os.Stat(filepath.Join(cache, "agents")); err != nil {
		t.Fatalf("publish must write the agents cache: %v", err)
	}
	if _, ok := rt.globalValue("@vibe_state_serial"); !ok {
		t.Fatal("publish must bump @vibe_state_serial")
	}
	if _, ok := rt.globalValue("@vibe_engine_serial"); ok {
		t.Fatal("publish must never bump @vibe_engine_serial (refetch loop)")
	}

	// The temp file never lingers.
	if _, err := os.Stat(filepath.Join(cache, "agents.watch.tmp")); !os.IsNotExist(err) {
		t.Fatal("publish must not leave its temp file behind")
	}
}

// countingTmux is a goroutine-safe stand-in for the coalescing test:
// the fetch loop bumps from its own goroutine while the test polls.
type countingTmux struct {
	recordingTmux
	mu    sync.Mutex
	state int
}

func (c *countingTmux) SetGlobalOption(_ context.Context, option, value string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if option == "@vibe_state_serial" {
		c.state++
	}
	return nil
}

func (c *countingTmux) stateBumps() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

func TestWatchFetchLoopCoalescesBursts(t *testing.T) {
	a, _ := newTestApp(t)
	ct := &countingTmux{}
	a.deps.Tmux = ct
	cache := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		a.watchFetchLoop(ctx, WatchRequest{CacheDir: cache, Project: domain.ProjectID("p")}, events)
		close(done)
	}()

	// A spaced burst (the sentinel's shape: one line per change): the
	// first publish fires immediately, everything landing inside the
	// gap window coalesces into ONE follow-up.
	for range 5 {
		select {
		case events <- struct{}{}:
		default:
		}
		time.Sleep(watchFetchGap / 10)
	}
	deadline := time.After(2 * watchFetchGap)
	for ct.stateBumps() < 2 {
		select {
		case <-deadline:
			t.Fatalf("want the burst to settle at 2 publishes, got %d", ct.stateBumps())
		case <-time.After(50 * time.Millisecond):
		}
	}
	cancel()
	<-done
	if got := ct.stateBumps(); got != 2 {
		t.Fatalf("a 5-signal burst must publish exactly twice, got %d", got)
	}
}

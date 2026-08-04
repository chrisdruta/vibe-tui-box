package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/chrisdruta/vibe-tui-box/internal/dockerapi"
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

	// The temp files never linger.
	if leftovers, _ := filepath.Glob(filepath.Join(cache, "*.fetch.tmp")); len(leftovers) != 0 {
		t.Fatalf("publish must not leave temp files behind: %v", leftovers)
	}
}

// countingTmux is a goroutine-safe stand-in for the coalescing tests:
// the daemon loops bump from their own goroutines while the test polls.
type countingTmux struct {
	recordingTmux
	mu     sync.Mutex
	state  int
	engine int
}

func (c *countingTmux) SetGlobalOption(_ context.Context, option, value string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch option {
	case "@vibe_state_serial":
		c.state++
	case "@vibe_engine_serial":
		c.engine++
	}
	return nil
}

func (c *countingTmux) stateBumps() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

func (c *countingTmux) engineBumps() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.engine
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

// TestWatchContainerEventsBumpEngineSerial pins the container-level
// half of the push channel: a managed-container lifecycle event must
// bump @vibe_engine_serial (the refetch signal — container truth lives
// in the fleet/agents caches, so the frame-only state serial would
// repaint stale data), a burst must coalesce like the fetch loop, and
// the subscription must ask for exactly the lifecycle actions — never
// the exec_* chatter the daemon's own fetches generate.
func TestWatchContainerEventsBumpEngineSerial(t *testing.T) {
	a, docker := newTestApp(t)
	ct := &countingTmux{}
	a.deps.Tmux = ct
	docker.EventsCh = make(chan dockerapi.ContainerEvent, 8)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		a.watchContainerEvents(ctx)
		close(done)
	}()

	// A spaced burst (a container flapping): the first bump fires
	// immediately, everything inside the gap window coalesces into ONE
	// follow-up.
	for range 5 {
		docker.EventsCh <- dockerapi.ContainerEvent{Action: "die", Name: "vibe-p-dev", Project: "p"}
		time.Sleep(watchFetchGap / 10)
	}
	deadline := time.After(2 * watchFetchGap)
	for ct.engineBumps() < 2 {
		select {
		case <-deadline:
			t.Fatalf("want the burst to settle at 2 bumps, got %d", ct.engineBumps())
		case <-time.After(50 * time.Millisecond):
		}
	}
	cancel()
	<-done
	if got := ct.engineBumps(); got != 2 {
		t.Fatalf("a 5-event burst must bump exactly twice, got %d", got)
	}
	if got := ct.stateBumps(); got != 0 {
		t.Fatalf("the events path must never bump the frame serial, got %d bumps", got)
	}

	calls := docker.CallsTo("Events")
	if len(calls) == 0 {
		t.Fatal("no Events subscription recorded")
	}
	want := dockerapi.EventsRequest{Actions: watchEventActions}
	if !reflect.DeepEqual(calls[0].Request, want) {
		t.Fatalf("subscription request = %+v, want %+v", calls[0].Request, want)
	}
}

// TestWatchShimDrifted pins the daemon's self-upgrade trigger: only a
// shim that resolves to a DIFFERENT binary is drift — pointing at this
// daemon's own binary, missing entirely, or dangling all keep the
// daemon alive (running stale beats not running).
func TestWatchShimDrifted(t *testing.T) {
	a, _ := newTestApp(t)
	self := filepath.Join(a.deps.Layout.Bin, "vibe-aaaaaaaaaaaa")
	other := filepath.Join(a.deps.Layout.Bin, "vibe-bbbbbbbbbbbb")
	for _, p := range []string{self, other} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("bin"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	resolved, err := filepath.EvalSymlinks(self)
	if err != nil {
		t.Fatal(err)
	}
	a.deps.Executable = resolved

	shim := filepath.Join(a.deps.Layout.Bin, "vibe")
	if a.watchShimDrifted() {
		t.Fatal("no shim at all must not read as drift")
	}
	if err := os.Symlink(self, shim); err != nil {
		t.Fatal(err)
	}
	if a.watchShimDrifted() {
		t.Fatal("a shim resolving to this binary is not drift")
	}
	if err := os.Remove(shim); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, shim); err != nil {
		t.Fatal(err)
	}
	if !a.watchShimDrifted() {
		t.Fatal("a shim resolving elsewhere is the handoff signal")
	}
	if err := os.Remove(other); err != nil { // dangling: unresolvable
		t.Fatal(err)
	}
	if err := os.Remove(shim); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, shim); err != nil {
		t.Fatal(err)
	}
	if a.watchShimDrifted() {
		t.Fatal("a dangling shim must not read as drift")
	}
}

// TestWatchContainerEventsRetriesAndStops pins the accelerator-never-
// dependency contract: a daemon without event support keeps the loop
// resubscribing on backoff (the slow tick stays in charge meanwhile),
// and ctx cancel ends it promptly.
func TestWatchContainerEventsRetriesAndStops(t *testing.T) {
	a, docker := newTestApp(t)
	a.deps.Tmux = &countingTmux{}
	docker.EventsErr = context.DeadlineExceeded

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		a.watchContainerEvents(ctx)
		close(done)
	}()

	deadline := time.After(3 * time.Second)
	for len(docker.CallsTo("Events")) < 2 {
		select {
		case <-deadline:
			t.Fatalf("want a resubscribe after failure, got %d calls", len(docker.CallsTo("Events")))
		case <-time.After(50 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cancel must end the events loop")
	}
}

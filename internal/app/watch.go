package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"vibe/internal/dockerapi"
	"vibe/internal/domain"
	"vibe/internal/model"
)

// The watch channel (docs/tui-layout.md "The watch channel"): `vibe
// _watch` is a host-side daemon holding ONE long-lived docker exec on
// the container sentinel (payload/container/agent-watch.sh). The
// sentinel emits a line when the inner tmux topology or the agent
// state records change; the daemon reacts by re-running the agents
// fetch, rewriting the cache the sidebar/tray/chooser read, and
// bumping @vibe_state_serial so the sidebar frames on its next tick.
// Inner change → cache truth in ~1-2s, instead of the 30s slow-tick
// poll (which sidebar.sh keeps as the fallback cadence — this daemon
// is an accelerator, never a dependency).

// WatchRequest drives the hidden `vibe _watch` daemon.
type WatchRequest struct {
	CacheDir string
	Project  domain.ProjectID
	Width    int
}

const (
	// watchFetchGap floors the time between fetches: state records
	// churn while an agent works, and each fetch is a docker exec.
	watchFetchGap = 2 * time.Second
	// watchStaleLimit reaps a stream that stopped speaking — the
	// sentinel heartbeats (~15s), so silence this long means a wedged
	// exec, not a quiet container.
	watchStaleLimit = 60 * time.Second
	// watchRetryMax caps the reconnect backoff (container down,
	// stream died).
	watchRetryMax = 10 * time.Second
	// watchTuiPoll is how often the daemon confirms the tmux server
	// it serves still exists; the server dying is the exit signal.
	watchTuiPoll = 10 * time.Second
)

// Watch runs the daemon until the tmux server dies or ctx cancels.
// Exactly one watcher runs per (cache dir, project): a flock beside
// the cache makes redundant spawns (every sidebar retries on its slow
// tick) exit quietly, so the shell side never needs a pidfile dance.
func (a *App) Watch(ctx context.Context, req WatchRequest) error {
	if req.CacheDir == "" || req.Project == "" {
		return opError("_watch", req.Project, fmt.Errorf("%w: _watch requires --cache and --project", domain.ErrInvalid))
	}
	if a.deps.Tmux == nil || a.deps.Docker == nil || a.deps.Registry == nil {
		return opError("_watch", req.Project, fmt.Errorf("%w: _watch needs tmux, docker, and the registry", domain.ErrUnavailable))
	}

	lock, err := acquireWatchLock(filepath.Join(req.CacheDir, "watch."+string(req.Project)+".lock"))
	if err != nil {
		return nil // another daemon owns this project — not our error
	}
	defer lock.Close()

	// The tmux server owns this daemon's lifetime: when it dies, so do
	// we (and the canceled ctx tears the exec stream down, which is
	// what ends the container-side sentinel via its stdin leash).
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		t := time.NewTicker(watchTuiPoll)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if _, err := a.deps.Tmux.ListSessions(ctx); err != nil && ctx.Err() == nil {
					cancel()
					return
				}
				if a.watchShimDrifted() {
					cancel()
					return
				}
			}
		}
	}()

	events := make(chan struct{}, 1)
	go a.watchFetchLoop(ctx, req, events)
	go a.watchContainerEvents(ctx)

	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return nil
		}
		streamed, err := a.watchStream(ctx, req.Project, events)
		if ctx.Err() != nil {
			return nil
		}
		if err == nil && streamed {
			backoff = time.Second // a healthy run resets the ladder
		} else {
			backoff = min(backoff*2, watchRetryMax)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
	}
}

// watchShimDrifted is the daemon's half of the self-upgrade contract
// (the sidebar script execs itself on payload drift; a resident daemon
// must DIE to be replaced — its flock blocks any successor): true when
// the host `vibe` shim no longer resolves to this daemon's own binary,
// meaning a handoff (dev on/sync, dev off, update) landed. The caller
// exits, freeing the flock, and the sidebar's slow tick respawns the
// daemon from the restamped @vibe_exe. A missing or unreadable shim is
// never drift — running stale beats not running.
func (a *App) watchShimDrifted() bool {
	shim, err := filepath.EvalSymlinks(filepath.Join(a.deps.Layout.Bin, "vibe"))
	return err == nil && shim != a.deps.Executable
}

// acquireWatchLock takes the singleton flock, non-blocking. The lock
// dies with the process (kernel-held), so a crashed daemon never
// wedges the slot the way a stale pidfile would.
func acquireWatchLock(path string) (*os.File, error) {
	return acquireFlock(path, false)
}

// acquireFlock is the shared kernel-held lock: non-blocking callers
// (redundant spawns, the sidebar's fetch trigger) lose quietly and
// exit; blocking callers (the watch daemon's owed publish) wait —
// fetches are short by design.
func acquireFlock(path string, block bool) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	how := syscall.LOCK_EX
	if !block {
		how |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(f.Fd()), how); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// watchStream connects one sentinel exec and pumps its lines into the
// event channel until the stream ends. Returns whether any line ever
// arrived (the backoff's health signal).
func (a *App) watchStream(ctx context.Context, project domain.ProjectID, events chan<- struct{}) (bool, error) {
	rec, err := a.deps.Registry.Get(ctx, project)
	if err != nil {
		return false, err
	}
	name, err := a.requireDevContainer(ctx, rec)
	if err != nil {
		return false, err // container down: the caller's backoff is the retry
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	pr, pw := io.Pipe()
	// Stdin is the sentinel's leash: held open for the stream's life,
	// closed by the hijack teardown when this side goes away — the
	// sentinel reads EOF and exits instead of orphan-polling forever.
	leashR, leashW := io.Pipe()
	defer leashW.Close()

	done := make(chan error, 1)
	go func() {
		_, err := a.deps.Docker.Exec(ctx, dockerapi.ExecRequest{
			Container: name,
			User:      devContainerUser,
			Argv:      []string{"bash", model.PayloadAgentWatch},
			Stdin:     true,
			Streams:   dockerapi.Streams{In: leashR, Out: pw},
		})
		pw.CloseWithError(io.EOF)
		done <- err
	}()

	// Staleness watchdog: the sentinel heartbeats, so a silent stream
	// is a wedged one — cancel and let the caller reconnect.
	var lastLine atomic.Int64
	lastLine.Store(time.Now().UnixNano())
	go func() {
		t := time.NewTicker(watchStaleLimit / 2)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if time.Now().UnixNano()-lastLine.Load() > int64(watchStaleLimit) {
					cancel()
					return
				}
			}
		}
	}()

	streamed := false
	sc := bufio.NewScanner(pr)
	for sc.Scan() {
		streamed = true
		lastLine.Store(time.Now().UnixNano())
		if strings.TrimSpace(sc.Text()) != "E" {
			continue // heartbeats feed the watchdog, not the fetcher
		}
		select {
		case events <- struct{}{}:
		default: // a fetch is already owed; one signal covers all
		}
	}
	// A scan error and a stream error are the same verdict here — the
	// connection is over and the caller's backoff owns what's next —
	// but the exec's error names the cause better.
	if err := <-done; err != nil {
		return streamed, err
	}
	return streamed, sc.Err()
}

// watchFetchLoop is the sentinel stream's coalescing consumer: each
// inner change owes one fetch-and-publish.
func (a *App) watchFetchLoop(ctx context.Context, req WatchRequest, events <-chan struct{}) {
	watchCoalesce(ctx, events, func() { a.watchPublish(ctx, req) })
}

// watchCoalesce is the shared consumer shape: every signal owes one
// fire, bursts collapse into one fire per gap window, and a signal
// that lands mid-fire is honored by the next pass.
func watchCoalesce(ctx context.Context, signals <-chan struct{}, fire func()) {
	var last time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-signals:
		}
		if wait := watchFetchGap - time.Since(last); wait > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			// The window absorbed any burst; the fire below speaks
			// for all of it.
			select {
			case <-signals:
			default:
			}
		}
		last = time.Now()
		fire()
	}
}

// watchEventActions is the server-side event filter: the lifecycle
// transitions the sidebar renders (up/down/exit/rename), never the
// exec_* chatter — the daemon's own agents fetch execs into every
// running container, and reacting to those would make each fetch
// trigger the next.
var watchEventActions = []string{
	"create", "start", "restart", "die", "stop", "oom", "destroy", "pause", "unpause", "rename",
}

// watchContainerEvents is the container-level half of the push channel:
// the sentinel exec reports inner changes, but a dying container takes
// its sentinel with it — the docker event stream is the only voice
// left (out-of-band deaths used to ride the 30s slow tick). Each
// managed-container lifecycle event bumps @vibe_engine_serial — the
// same "engine truth moved" signal every state-mutating command sends
// — so every sidebar refetches fleet+agents+detail on its next 2s
// tick. The subscription is label-filtered fleet-wide, not
// per-project: the fleet pane shows other projects' up/down truth too,
// and concurrent daemons double-bumping is harmless (sidebars compare
// serial inequality per tick). Coalesced like the fetch loop; failures
// back off and resubscribe — an accelerator, never a dependency (a
// daemon without event support just leaves the slow tick in charge).
func (a *App) watchContainerEvents(ctx context.Context) {
	signals := make(chan struct{}, 1)
	go watchCoalesce(ctx, signals, func() {
		bumpCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		nonce := fmt.Sprintf("e%d.%d", a.deps.Clock.Now().UnixNano(), a.tuiSerial.Add(1))
		_ = a.deps.Tmux.SetGlobalOption(bumpCtx, "@vibe_engine_serial", nonce)
	})

	var streamed atomic.Bool
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		streamed.Store(false)
		err := a.deps.Docker.Events(ctx, dockerapi.EventsRequest{Actions: watchEventActions},
			func(dockerapi.ContainerEvent) {
				streamed.Store(true)
				select {
				case signals <- struct{}{}:
				default: // a bump is already owed; one signal covers all
				}
			})
		if ctx.Err() != nil {
			return
		}
		if err == nil || streamed.Load() {
			backoff = time.Second // a healthy stream resets the ladder
		} else {
			backoff = min(backoff*2, watchRetryMax)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
	}
}

// watchPublish runs the shared single-pass cache producer (`_fetch`,
// blocking-lock mode — this publish is OWED by an event) and lets it
// bump @vibe_state_serial: a frame-only signal, deliberately NOT
// @vibe_engine_serial — that one tells the sidebar to fetch, and the
// whole point is that the fetch already happened here. One deliberate
// cost note: the full pass adds one git-churn subprocess per running
// project per coalesced event (the 2s floor bounds it).
func (a *App) watchPublish(ctx context.Context, req WatchRequest) {
	_, _ = a.FetchCaches(ctx, FetchRequest{CacheDir: req.CacheDir, Width: req.Width, Block: true})
}

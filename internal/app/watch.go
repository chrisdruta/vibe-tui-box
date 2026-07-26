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

	"github.com/chrisdruta/vibe-tui-box/internal/dockerapi"
	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/model"
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
			}
		}
	}()

	events := make(chan struct{}, 1)
	go a.watchFetchLoop(ctx, req, events)

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
		} else if backoff < watchRetryMax {
			backoff *= 2
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
	}
}

// acquireWatchLock takes the singleton flock, non-blocking. The lock
// dies with the process (kernel-held), so a crashed daemon never
// wedges the slot the way a stale pidfile would.
func acquireWatchLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
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

// watchFetchLoop is the coalescing consumer: every event owes one
// fetch, bursts collapse into one fetch per gap window, and a signal
// that lands mid-fetch is honored by the next pass.
func (a *App) watchFetchLoop(ctx context.Context, req WatchRequest, events <-chan struct{}) {
	var last time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-events:
		}
		if wait := watchFetchGap - time.Since(last); wait > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			// The window absorbed any burst; the fetch below speaks
			// for all of it.
			select {
			case <-events:
			default:
			}
		}
		last = time.Now()
		a.watchPublish(ctx, req)
	}
}

// watchPublish re-runs the fleet agents fetch, replaces the cache the
// sidebar/tray/chooser read (tmp+rename, own tmp name — the sidebar's
// slow-tick fetch writes beside us and last rename wins), and bumps
// @vibe_state_serial: a frame-only signal, deliberately NOT
// @vibe_engine_serial — that one tells the sidebar to fetch, and the
// whole point is that the fetch already happened here.
func (a *App) watchPublish(ctx context.Context, req WatchRequest) {
	res, err := a.RenderAgents(ctx, RenderRequest{Width: req.Width})
	if err != nil {
		return
	}
	body := ""
	if len(res.Lines) > 0 {
		body = strings.Join(res.Lines, "\n") + "\n"
	}
	tmp := filepath.Join(req.CacheDir, "agents.watch.tmp")
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		return
	}
	if err := os.Rename(tmp, filepath.Join(req.CacheDir, "agents")); err != nil {
		os.Remove(tmp)
		return
	}
	bumpCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	nonce := fmt.Sprintf("w%d.%d", a.deps.Clock.Now().UnixNano(), a.tuiSerial.Add(1))
	_ = a.deps.Tmux.SetGlobalOption(bumpCtx, "@vibe_state_serial", nonce)
}

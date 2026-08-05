package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/tmuxui"
)

// The fetch path (`vibe _fetch`): ONE engine pass produces every cache
// the sidebar/tray/chooser read — fleet, agents, and the per-project
// detail lines — where the previous shape shelled three renderers per
// cycle, paid runtime.Status three times per project, and let two
// independent writers race on the agents cache (the sidebar's slow
// tick vs the watch daemon, "last rename wins"). One pass, one writer,
// one repaint signal.

// FetchRequest drives the hidden `vibe _fetch` cache producer.
type FetchRequest struct {
	CacheDir string
	// Width bounds ONLY the pre-rendered detail lines — the one
	// width-baked cache (the frame splices them verbatim). The fleet
	// and agents caches are data: they write at the engine default and
	// their consumers re-truncate for display.
	Width int
	// Block waits for the fetch flock instead of yielding — the watch
	// daemon's publish is OWED (an event fired); the sidebar-triggered
	// call yields quietly when another fetch is already in flight, the
	// same lost-spawn contract as the daemon flock.
	Block bool
}

type FetchResult struct {
	Projects int
	// Skipped reports a non-blocking call that lost the flock: another
	// fetch was mid-flight and speaks for this one.
	Skipped bool
}

// FetchCaches runs the single fetch pass and publishes the caches:
// per project one runtime.Status (feeding the fleet line, the detail
// lines, AND the sidecar roster rows), one agent-ps exec and one
// git-churn subprocess when the dev container runs, every file
// tmp+renamed, stale detail files for forgotten projects aged out, and
// ONE @vibe_state_serial bump after the last rename — the frame-only
// repaint signal. Never @vibe_engine_serial: that stays "truth moved,
// go refetch" (mutating commands and docker events), and this IS the
// refetch.
func (a *App) FetchCaches(ctx context.Context, req FetchRequest) (FetchResult, error) {
	fail := opFail[FetchResult]("_fetch", "")
	if req.CacheDir == "" {
		return fail(fmt.Errorf("%w: _fetch requires --cache", domain.ErrInvalid))
	}
	lock, err := acquireFlock(filepath.Join(req.CacheDir, "fetch.lock"), req.Block)
	if err != nil {
		return FetchResult{Skipped: true}, nil
	}
	defer lock.Close()

	records, err := a.deps.Registry.List(ctx)
	if err != nil {
		return fail(err)
	}
	views := make([]tmuxui.ProjectView, 0, len(records))
	var entries []tmuxui.AgentEntry
	detail := make(map[string][]string, len(records))
	for _, rec := range records {
		state, serr := a.runtime.Status(ctx, rec)
		statusOK := serr == nil
		view := a.viewFrom(rec, state, statusOK)
		running := false
		for _, c := range view.Containers {
			if c.Running {
				running = true
				break
			}
		}
		// Churn only for in-use projects — the one real subprocess in
		// the pass, and the reason `_fetch` (like the renderers it
		// replaces) rides the fetch cadence, never a frame.
		if running {
			view.Churn = a.gitChurn(ctx, rec.Root)
		}
		views = append(views, view)
		entries = append(entries, a.agentEntriesFor(ctx, rec, state, statusOK)...)
		detail[string(rec.ID)] = tmuxui.Sidebar(view, req.Width)
	}

	if err := writeCache(req.CacheDir, "fleet", tmuxui.Fleet(views, 0)); err != nil {
		return fail(err)
	}
	if err := writeCache(req.CacheDir, "agents", tmuxui.Agents(entries, 0)); err != nil {
		return fail(err)
	}
	// Detail for EVERY project, not just a caller's: the old
	// stamp-guarded shape refreshed only the winning pane's
	// detail.<proj>, leaving every other project's a full cycle stale.
	// Files for forgotten projects age out here — nothing else ever
	// cleaned them.
	for id, lines := range detail {
		if err := writeCache(req.CacheDir, "detail."+id, lines); err != nil {
			return fail(err)
		}
	}
	if existing, err := filepath.Glob(filepath.Join(req.CacheDir, "detail.*")); err == nil {
		for _, path := range existing {
			id := strings.TrimPrefix(filepath.Base(path), "detail.")
			if _, ok := detail[id]; !ok {
				_ = os.Remove(path)
			}
		}
	}
	a.bumpStateSerial(ctx)
	return FetchResult{Projects: len(records)}, nil
}

// writeCache publishes one cache file tmp+rename; a failed write keeps
// the last good cache, the contract every reader already assumes.
func writeCache(dir, name string, lines []string) error {
	body := ""
	if len(lines) > 0 {
		body = strings.Join(lines, "\n") + "\n"
	}
	tmp := filepath.Join(dir, name+".fetch.tmp")
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// bumpStateSerial signals every sidebar to REPAINT from the caches just
// published — the frame-only serial, deliberately not
// @vibe_engine_serial (which would tell them to refetch what was just
// fetched). Fire-and-forget like every serial bump.
func (a *App) bumpStateSerial(ctx context.Context) {
	if a.deps.Tmux == nil {
		return
	}
	bumpCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	nonce := fmt.Sprintf("f%d.%d", a.deps.Clock.Now().UnixNano(), a.tuiSerial.Add(1))
	_ = a.deps.Tmux.SetGlobalOption(bumpCtx, "@vibe_state_serial", nonce)
}

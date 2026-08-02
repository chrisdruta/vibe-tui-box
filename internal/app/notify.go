package app

import (
	"context"
	"fmt"
	"time"

	"github.com/chrisdruta/vibe-tui-box/internal/registry"
)

// bumpTuiSerial signals the tui that engine truth moved: state-mutating
// commands set @vibe_engine_serial on the vibe-engine socket, and the
// sidebar's render loop refetches the engine renderers on its next tick
// (payload/host/scripts/sidebar.sh). A separate serial from the
// tmux-local @vibe_state_serial on purpose — that one means "agent dots
// moved, redraw cheaply", this one means "an engine refetch is worth
// its Docker round trips".
//
// Fire-and-forget by contract: a nil tmux, a dead server (set-option is
// not a start-server command and fails in milliseconds), or a wedged
// socket must never fail or stall the operation that did the real work,
// and a Ctrl-C'd operation that already mutated state still owes the
// tui its signal — hence WithoutCancel plus a hard timeout.
func (a *App) bumpTuiSerial(ctx context.Context) {
	if a.deps.Tmux == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	// Only inequality with the previous value matters (the shell side
	// compares, never parses); the counter keeps same-process bumps
	// distinct even under a frozen test clock.
	nonce := fmt.Sprintf("%d.%d", a.deps.Clock.Now().UnixNano(), a.tuiSerial.Add(1))
	_ = a.deps.Tmux.SetGlobalOption(ctx, "@vibe_engine_serial", nonce)
}

// restampTui lands a binary handoff on a RUNNING tui server: whenever a
// command repoints the host `vibe` shim (dev on/sync, dev off, update),
// the server's @vibe_exe and @vibe_payload_dir still name whatever
// artifact `vibe tui` joined with — @vibe_exe because the join stamps
// its own os.Executable(), a resolved digest-addressed path no symlink
// flip can move, and @vibe_payload_dir because nothing but the join
// ever wrote it (2026-08-01, Chris — a dev sync landed and NOTHING in
// the live UI changed). Restamping both is what makes the handoff
// live: engine calls and click-time script resolution go through the
// options on every use, and the sidebar render loops see the
// payload-dir drift and exec the new script on their slow tick. exe is
// the SYMLINK path installBinary returned, not a resolved binary — the
// symlink survives the next handoff too. The conf is re-materialized
// first (with that same exe in its prologue) and re-sourced like the
// attach-time heal: the heal only fires on a JOIN, and after a sync the
// operator is usually already attached — exactly when stale bindings
// used to linger until someone remembered prefix+R.
//
// Same fire-and-forget contract as bumpTuiSerial, which callers still
// bump AFTER this so the refetch it triggers already runs the new
// binary: a nil tmux, a dead server, or an artifact without a tui conf
// must never fail the operation that did the real work.
func (a *App) restampTui(ctx context.Context, rec registry.Record, exe string) {
	if a.deps.Tmux == nil || exe == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	conf, hostDir, err := a.materializeTuiConf(ctx, rec, exe)
	if err != nil || conf == "" {
		return
	}
	_ = a.deps.Tmux.SetGlobalOption(ctx, "@vibe_exe", exe)
	_ = a.deps.Tmux.SetGlobalOption(ctx, "@vibe_payload_dir", hostDir)
	_ = a.deps.Tmux.SourceFile(ctx, conf)
}

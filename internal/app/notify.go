package app

import (
	"context"
	"fmt"
	"time"
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

// Package app is the dependency container and command surface. Methods
// return typed results and typed errors; they never print, never read
// ambient environment, and never fall back to package globals.
package app

import (
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"github.com/chrisdruta/vibe-tui-box/internal/builder"
	"github.com/chrisdruta/vibe-tui-box/internal/dev"
	"github.com/chrisdruta/vibe-tui-box/internal/dockerapi"
	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/lock"
	"github.com/chrisdruta/vibe-tui-box/internal/paths"
	"github.com/chrisdruta/vibe-tui-box/internal/payload"
	"github.com/chrisdruta/vibe-tui-box/internal/registry"
	"github.com/chrisdruta/vibe-tui-box/internal/release"
	"github.com/chrisdruta/vibe-tui-box/internal/runner"
	"github.com/chrisdruta/vibe-tui-box/internal/runtime"
	"github.com/chrisdruta/vibe-tui-box/internal/snapshot"
	"github.com/chrisdruta/vibe-tui-box/internal/store"
	"github.com/chrisdruta/vibe-tui-box/internal/terminal"
	"github.com/chrisdruta/vibe-tui-box/internal/tmux"
	"github.com/chrisdruta/vibe-tui-box/internal/version"
)

// Clock supplies timestamps; tests inject a fixed one.
type Clock interface {
	Now() time.Time
}

// SystemClock is the production clock.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

// Dependencies lists everything the command surface may touch. Fields
// for later slices (Docker, tmux, release, payload) are added as those
// slices land, so construction can keep validating that every declared
// dependency is present.
type Dependencies struct {
	Clock     Clock
	Layout    paths.Layout
	Locks     lock.Locker
	Store     *store.Store
	Registry  *registry.Registry
	Snapshots *snapshot.Service
	Docker    dockerapi.Client
	Runner    runner.Runner
	// Executables are host tool paths resolved at startup.
	Executables runner.Executables
	// Prompt asks the operator for approvals; nil means non-interactive
	// (approvals then require explicit flags or fail).
	Prompt terminal.Prompt
	// Tmux drives the terminal UI; nil makes `vibe tui` unavailable.
	Tmux    tmux.Client
	Version version.Info
	// Payload is the embedded payload bundle; nil in payload-less test
	// builds (provision then reports unavailable).
	Payload *payload.Bundle
	// Release acquires remote release artifacts; nil disables update.
	Release *release.Service
	// Executable is the absolute path of the running binary, used by
	// provision.
	Executable string
	// Progress receives pull/build events for presentation; nil discards.
	Progress dockerapi.ProgressSink
	// LifecycleOut receives project hook output during up/rebuild (raw
	// container bytes, like exec); nil discards.
	LifecycleOut io.Writer
}

type App struct {
	deps    Dependencies
	runtime *runtime.Service
	builder *builder.Service
	dev     *dev.Service
	// tuiSerial keeps same-process @vibe_engine_serial bumps distinct
	// even under a frozen test clock (notify.go).
	tuiSerial atomic.Uint64
}

func New(deps Dependencies) (*App, error) {
	switch {
	case deps.Clock == nil:
		return nil, fmt.Errorf("%w: app requires a clock", domain.ErrInvalid)
	case deps.Locks == nil:
		return nil, fmt.Errorf("%w: app requires a locker", domain.ErrInvalid)
	case deps.Store == nil:
		return nil, fmt.Errorf("%w: app requires a store", domain.ErrInvalid)
	case deps.Registry == nil:
		return nil, fmt.Errorf("%w: app requires a registry", domain.ErrInvalid)
	case deps.Snapshots == nil:
		return nil, fmt.Errorf("%w: app requires a snapshot service", domain.ErrInvalid)
	case deps.Docker == nil:
		return nil, fmt.Errorf("%w: app requires a docker client", domain.ErrInvalid)
	case deps.Runner == nil:
		return nil, fmt.Errorf("%w: app requires a runner", domain.ErrInvalid)
	}
	if err := deps.Layout.Validate(); err != nil {
		return nil, err
	}
	rt, err := runtime.NewService(deps.Docker, deps.Store, deps.Locks)
	if err != nil {
		return nil, err
	}
	bld, err := builder.NewService(deps.Docker)
	if err != nil {
		return nil, err
	}
	platform, _ := domain.CurrentPlatform()
	devSvc, err := dev.NewService(deps.Snapshots, deps.Store, deps.Docker, platform)
	if err != nil {
		return nil, err
	}
	return &App{deps: deps, runtime: rt, builder: bld, dev: devSvc}, nil
}

// Version reports build metadata.
func (a *App) Version() version.Info { return a.deps.Version }

// opFail returns the typed failure constructor every (T, error) method
// funnels its returns through: `fail := opFail[T](op, "")` before the
// project resolves, rebound with the record's ID after. One idiom, so
// the op name and project attach uniformly.
func opFail[T any](op string, project domain.ProjectID) func(error) (T, error) {
	return func(err error) (T, error) {
		var zero T
		return zero, &domain.OpError{Op: op, Project: project, Err: err}
	}
}

// opError is opFail's shape for error-only methods.
func opError(op string, project domain.ProjectID, err error) error {
	return &domain.OpError{Op: op, Project: project, Err: err}
}

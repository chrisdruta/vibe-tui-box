// Command vibe is the compiled host engine of the vibe harness
// (docs/engine-internals.md). main performs wiring only: signal-aware
// context, host layout, dependency construction, and dispatch through
// the CLI. Business logic lives under internal/.
//
// The default output name collides with the bash launcher at the repo
// root, so build with an explicit output path while the launcher still
// exists:
//
//	go build -o bin/vibe ./cmd/vibe
package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"golang.org/x/term"

	assets "github.com/chrisdruta/vibe-tui-box"
	"github.com/chrisdruta/vibe-tui-box/internal/app"
	"github.com/chrisdruta/vibe-tui-box/internal/cli"
	"github.com/chrisdruta/vibe-tui-box/internal/dockerapi"
	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/lock"
	"github.com/chrisdruta/vibe-tui-box/internal/paths"
	"github.com/chrisdruta/vibe-tui-box/internal/payload"
	"github.com/chrisdruta/vibe-tui-box/internal/progressui"
	"github.com/chrisdruta/vibe-tui-box/internal/registry"
	"github.com/chrisdruta/vibe-tui-box/internal/release"
	"github.com/chrisdruta/vibe-tui-box/internal/runner"
	"github.com/chrisdruta/vibe-tui-box/internal/snapshot"
	"github.com/chrisdruta/vibe-tui-box/internal/store"
	"github.com/chrisdruta/vibe-tui-box/internal/terminal"
	"github.com/chrisdruta/vibe-tui-box/internal/tmux"
	"github.com/chrisdruta/vibe-tui-box/internal/version"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a, err := construct()
	if err != nil {
		fmt.Fprintf(os.Stderr, "vibe: %v\n", err)
		return cli.ExitFailure
	}
	return cli.Run(ctx, a, os.Args[1:])
}

func construct() (*app.App, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}
	layout, err := paths.NewLayout(home)
	if err != nil {
		return nil, err
	}
	if err := layout.Ensure(); err != nil {
		return nil, err
	}

	locks := lock.NewFlock(layout.Locks)
	st, err := store.New(layout, locks)
	if err != nil {
		return nil, err
	}
	clock := app.SystemClock{}
	reg, err := registry.New(layout.Projects, locks, clock)
	if err != nil {
		return nil, err
	}
	snaps, err := snapshot.NewService(st)
	if err != nil {
		return nil, err
	}
	executables, err := runner.Resolve()
	if err != nil {
		return nil, err
	}
	docker, err := dockerapi.NewSDK()
	if err != nil {
		return nil, err
	}

	payloadFS, err := fs.Sub(assets.PayloadFS, "payload")
	if err != nil {
		return nil, err
	}
	bundle, err := payload.New(payloadFS)
	if err != nil {
		return nil, fmt.Errorf("embedded payload: %w", err)
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}

	var releases *release.Service
	if platform, err := domain.CurrentPlatform(); err == nil {
		releases, err = release.NewService(
			release.NewHTTPSource(release.DefaultBaseURL),
			release.ChecksumVerifier{},
			st,
			platform,
			func() int64 { return clock.Now().Unix() },
		)
		if err != nil {
			return nil, err
		}
	}

	var tmuxClient tmux.Client
	if executables.Tmux != "" {
		if b, err := tmux.NewBinary(executables.Tmux, runner.NewHost()); err == nil {
			tmuxClient = b
		}
	}

	return app.New(app.Dependencies{
		Clock:       clock,
		Layout:      layout,
		Locks:       locks,
		Store:       st,
		Registry:    reg,
		Snapshots:   snaps,
		Docker:      docker,
		Runner:      runner.NewHost(),
		Executables: executables,
		Prompt:      &terminal.StdioPrompt{In: os.Stdin, Out: os.Stderr},
		Tmux:        tmuxClient,
		Version:     version.Get(),
		Payload:     bundle,
		Release:     releases,
		Executable:  executable,
		Progress:    stderrProgress(),
		// Hook output is diagnostic side-band like progress; stdout stays
		// reserved for command results.
		LifecycleOut: os.Stderr,
	})
}

// stderrProgress selects the progress presentation for this process:
// the raw stream under --verbose (any output), the live bill-of-
// materials renderer when stderr is a terminal, silence otherwise.
// The flag is peeked from argv because the sink is wired before the
// command parser runs; the cli layer declares the same flag so parsing
// accepts it.
func stderrProgress() dockerapi.ProgressSink {
	for _, arg := range os.Args[1:] {
		if arg == "--verbose" || arg == "-verbose" {
			return progressui.Verbose(os.Stderr)
		}
	}
	fd := int(os.Stderr.Fd())
	if !term.IsTerminal(fd) {
		return nil
	}
	width := func() int {
		if w, _, err := term.GetSize(fd); err == nil && w > 0 {
			return w
		}
		return 80
	}
	return progressui.New(os.Stderr, width).Sink()
}

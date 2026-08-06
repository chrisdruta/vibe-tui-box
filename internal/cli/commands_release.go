package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"golang.org/x/term"

	"vibe/internal/app"
)

// releaseCommands covers project setup, artifacts, and health.
var releaseCommands = map[string]Command{
	"init": {
		Name:    "init",
		Summary: "seed .vibe/ from a preset and register the project",
		Usage:   "vibe init [--preset NAME] [--auto-memory[=BOOL]] [--json]",
		Parse: func(args []string) (Request, error) {
			var req InitRequest
			fs := newFlagSet("init", &req.Options)
			fs.StringVar(&req.Preset, "preset", "", "preset name (default: minimal)")
			autoMemory := fs.Bool("auto-memory", false, "enable Claude auto memory (skips the question)")
			if err := parseArgs(fs, args); err != nil {
				return nil, err
			}
			if fs.NArg() > 0 {
				return nil, fmt.Errorf("unexpected argument %q", fs.Arg(0))
			}
			// Tri-state: only a flag the user actually typed decides; its
			// absence lets an interactive init ask instead.
			fs.Visit(func(f *flag.Flag) {
				if f.Name == "auto-memory" {
					req.AutoMemory = autoMemory
				}
			})
			return &req, nil
		},
		Run: func(ctx context.Context, a *app.App, req Request, dir string) (Result, error) {
			r := req.(*InitRequest)
			res, err := a.Init(ctx, app.InitRequest{
				Dir:        dir,
				Preset:     r.Preset,
				AutoMemory: r.AutoMemory,
				// The question needs a human on both ends: stdin to
				// answer, stderr to see it; --json implies scripted.
				Interactive: !r.JSON &&
					term.IsTerminal(int(os.Stdin.Fd())) &&
					term.IsTerminal(int(os.Stderr.Fd())),
			})
			if err != nil {
				return nil, err
			}
			return &initResult{Result: res}, nil
		},
	},
	"doctor": {
		Name:    "doctor",
		Summary: "check host, project, artifact, and container health",
		Usage:   "vibe doctor [--json]",
		Parse: func(args []string) (Request, error) {
			var opts Options
			return parseInto(args, "doctor", &opts, nil)
		},
		Run: func(ctx context.Context, a *app.App, req Request, dir string) (Result, error) {
			res, err := a.Doctor(ctx, app.DoctorRequest{Dir: dir})
			if err != nil {
				return nil, err
			}
			return &doctorResult{Result: res}, nil
		},
	},
	"bootstrap": {
		Name:    "bootstrap",
		Summary: "verify the manifest's required tools inside the dev container",
		Usage:   "vibe bootstrap [--json]",
		Parse: func(args []string) (Request, error) {
			var opts Options
			return parseInto(args, "bootstrap", &opts, nil)
		},
		Run: func(ctx context.Context, a *app.App, req Request, dir string) (Result, error) {
			res, err := a.Bootstrap(ctx, app.BootstrapRequest{Dir: dir})
			if err != nil {
				return nil, err
			}
			return &bootstrapResult{Result: res}, nil
		},
	},
	"provision": {
		Name:    "provision",
		Summary: "install this binary and its embedded payload as an artifact",
		Usage:   "vibe provision [--json]",
		Parse: func(args []string) (Request, error) {
			var opts Options
			return parseInto(args, "provision", &opts, nil)
		},
		Run: func(ctx context.Context, a *app.App, req Request, dir string) (Result, error) {
			res, err := a.Provision(ctx, app.ProvisionRequest{Dir: dir})
			if err != nil {
				return nil, err
			}
			return &provisionResult{Result: res}, nil
		},
	},
	"gc": {
		Name:    "gc",
		Summary: "remove unreferenced artifacts, candidates, and snapshots",
		Usage:   "vibe gc [--dry-run] [--min-age DURATION] [--json]",
		// Host-global store maintenance: no project, no cwd needed.
		NoCwd: true,
		Parse: func(args []string) (Request, error) {
			var req GCRequest
			return parseInto(args, "gc", &req.Options, func(fs *flag.FlagSet) any {
				fs.BoolVar(&req.DryRun, "dry-run", false, "report what would be removed, remove nothing")
				fs.DurationVar(&req.MinAge, "min-age", time.Hour, "never touch objects younger than this")
				return &req
			})
		},
		Run: func(ctx context.Context, a *app.App, req Request, dir string) (Result, error) {
			r := req.(*GCRequest)
			res, err := a.GC(ctx, app.GCRequest{DryRun: r.DryRun, MinAge: r.MinAge})
			if err != nil {
				return nil, err
			}
			return &gcResult{Result: res}, nil
		},
	},
	"update": {
		Name:    "update",
		Summary: "download, verify, and install a release",
		Usage:   "vibe update --version vX.Y.Z [--json]",
		Parse: func(args []string) (Request, error) {
			var req UpdateRequest
			return parseInto(args, "update", &req.Options, func(fs *flag.FlagSet) any {
				fs.StringVar(&req.Version, "version", "", "release version to install (e.g. v2.0.0)")
				return &req
			})
		},
		Run: func(ctx context.Context, a *app.App, req Request, dir string) (Result, error) {
			r := req.(*UpdateRequest)
			res, err := a.Update(ctx, app.UpdateRequest{Dir: dir, Version: r.Version})
			if err != nil {
				return nil, err
			}
			return &updateResult{Result: res}, nil
		},
	},
}

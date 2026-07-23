package cli

import (
	"context"
	"flag"

	"github.com/chrisdruta/vibe-tui-box/internal/app"
)

// releaseCommands covers project setup, artifacts, and health.
var releaseCommands = map[string]Command{
	"init": {
		Name:    "init",
		Summary: "seed .vibe/ from a preset and register the project",
		Usage:   "vibe init [--preset NAME] [--json]",
		Parse: func(args []string) (Request, error) {
			var req InitRequest
			return parseInto(args, "init", &req.Options, func(fs *flag.FlagSet) any {
				fs.StringVar(&req.Preset, "preset", "", "preset name (default: minimal)")
				return &req
			})
		},
		Run: func(ctx context.Context, a *app.App, req Request) (Result, error) {
			r := req.(*InitRequest)
			res, err := a.Init(ctx, app.InitRequest{Dir: mustCwd(), Preset: r.Preset})
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
			var req DoctorRequest
			return parseInto(args, "doctor", &req.Options, nil)
		},
		Run: func(ctx context.Context, a *app.App, req Request) (Result, error) {
			res, err := a.Doctor(ctx, app.DoctorRequest{Dir: mustCwd()})
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
			var req BootstrapRequest
			return parseInto(args, "bootstrap", &req.Options, nil)
		},
		Run: func(ctx context.Context, a *app.App, req Request) (Result, error) {
			res, err := a.Bootstrap(ctx, app.BootstrapRequest{Dir: mustCwd()})
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
			var req ProvisionRequest
			return parseInto(args, "provision", &req.Options, nil)
		},
		Run: func(ctx context.Context, a *app.App, req Request) (Result, error) {
			res, err := a.Provision(ctx, app.ProvisionRequest{Dir: mustCwd()})
			if err != nil {
				return nil, err
			}
			return &provisionResult{Result: res}, nil
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
		Run: func(ctx context.Context, a *app.App, req Request) (Result, error) {
			r := req.(*UpdateRequest)
			res, err := a.Update(ctx, app.UpdateRequest{Dir: mustCwd(), Version: r.Version})
			if err != nil {
				return nil, err
			}
			return &updateResult{Result: res}, nil
		},
	},
}

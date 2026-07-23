package cli

import (
	"context"
	"flag"

	"github.com/chrisdruta/vibe-tui-box/internal/app"
)

// lifecycleCommands covers container lifecycle and in-container
// execution.
var lifecycleCommands = map[string]Command{
	"up": {
		Name:    "up",
		Summary: "compile a candidate and start the project containers",
		Usage:   "vibe up [--json]",
		Parse: func(args []string) (Request, error) {
			var req UpRequest
			return parseInto(args, "up", &req.Options, nil)
		},
		Run: func(ctx context.Context, a *app.App, req Request) (Result, error) {
			res, err := a.Up(ctx, app.UpRequest{Dir: mustCwd()})
			if err != nil {
				return nil, err
			}
			return &upResult{Result: res, Op: "up"}, nil
		},
	},
	"rebuild": {
		Name:    "rebuild",
		Summary: "recreate containers from freshly compiled inputs",
		Usage:   "vibe rebuild [--json]",
		Parse: func(args []string) (Request, error) {
			var req RebuildRequest
			return parseInto(args, "rebuild", &req.Options, nil)
		},
		Run: func(ctx context.Context, a *app.App, req Request) (Result, error) {
			res, err := a.Up(ctx, app.UpRequest{Dir: mustCwd(), Force: true})
			if err != nil {
				return nil, err
			}
			return &upResult{Result: res, Op: "rebuild"}, nil
		},
	},
	"down": {
		Name:    "down",
		Summary: "stop and remove the project containers",
		Usage:   "vibe down [--volumes] [--json]",
		Parse: func(args []string) (Request, error) {
			var req DownRequest
			return parseInto(args, "down", &req.Options, func(fs *flag.FlagSet) any {
				fs.BoolVar(&req.Volumes, "volumes", false, "also remove project volumes (agent state!)")
				return &req
			})
		},
		Run: func(ctx context.Context, a *app.App, req Request) (Result, error) {
			r := req.(*DownRequest)
			res, err := a.Down(ctx, app.DownRequest{Dir: mustCwd(), RemoveVolumes: r.Volumes})
			if err != nil {
				return nil, err
			}
			return &downResult{Result: res}, nil
		},
	},
	"status": {
		Name:    "status",
		Summary: "show the project's runtime state",
		Usage:   "vibe status [--json]",
		Parse: func(args []string) (Request, error) {
			var req StatusRequest
			return parseInto(args, "status", &req.Options, nil)
		},
		Run: func(ctx context.Context, a *app.App, req Request) (Result, error) {
			res, err := a.Status(ctx, app.StatusRequest{Dir: mustCwd()})
			if err != nil {
				return nil, err
			}
			return &statusResult{Result: res}, nil
		},
	},
	"exec": {
		Name:    "exec",
		Summary: "run a command in the dev container (explicit env only)",
		Usage:   "vibe exec [-u USER] [-w DIR] [-e KEY=VALUE]... -- CMD [ARGS...]",
		Parse:   parseExecStyle("exec", true),
		Run: func(ctx context.Context, a *app.App, req Request) (Result, error) {
			r := req.(*ExecRequest)
			cmd, restore := r.containerCommand()
			defer restore()
			res, err := a.Exec(ctx, cmd)
			if err != nil {
				return nil, err
			}
			return &execResult{Code: res.ExitCode}, nil
		},
	},
	"run": {
		Name:    "run",
		Summary: "run a command with the project's frozen env file",
		Usage:   "vibe run [-u USER] [-w DIR] [-e KEY=VALUE]... -- CMD [ARGS...]",
		Parse:   parseExecStyle("run", true),
		Run: func(ctx context.Context, a *app.App, req Request) (Result, error) {
			r := req.(*ExecRequest)
			cmd, restore := r.containerCommand()
			defer restore()
			res, err := a.Run(ctx, cmd)
			if err != nil {
				return nil, err
			}
			return &execResult{Code: res.ExitCode}, nil
		},
	},
	"shell": {
		Name:    "shell",
		Summary: "open an interactive shell in the dev container",
		Usage:   "vibe shell [-u USER]",
		Parse:   parseExecStyle("shell", false),
		Run: func(ctx context.Context, a *app.App, req Request) (Result, error) {
			r := req.(*ExecRequest)
			cmd, restore := r.containerCommand()
			defer restore()
			res, err := a.Shell(ctx, cmd)
			if err != nil {
				return nil, err
			}
			return &execResult{Code: res.ExitCode}, nil
		},
	},
	"attach": {
		Name:    "attach",
		Summary: "attach to the dev container's main process",
		Usage:   "vibe attach",
		Parse:   parseExecStyle("attach", false),
		Run: func(ctx context.Context, a *app.App, req Request) (Result, error) {
			r := req.(*ExecRequest)
			cmd, restore := r.containerCommand()
			defer restore()
			if err := a.Attach(ctx, cmd); err != nil {
				return nil, err
			}
			return nil, nil
		},
	},
}

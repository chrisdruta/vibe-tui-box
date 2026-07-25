package cli

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/chrisdruta/vibe-tui-box/internal/app"
	"github.com/chrisdruta/vibe-tui-box/internal/dockerapi"
)

// lifecycleCommands covers container lifecycle and in-container
// execution.
var lifecycleCommands = map[string]Command{
	"up": {
		Name:    "up",
		Summary: "compile a candidate and start the project containers",
		Usage:   "vibe up [--refresh-agents] [--json]",
		Parse: func(args []string) (Request, error) {
			var req UpRequest
			return parseInto(args, "up", &req.Options, func(fs *flag.FlagSet) any {
				fs.BoolVar(&req.RefreshAgents, "refresh-agents", false,
					"re-pull the channel-tracking agents (claude, codex, grok) to latest")
				return &req
			})
		},
		Run: func(ctx context.Context, a *app.App, req Request) (Result, error) {
			r := req.(*UpRequest)
			res, err := a.Up(ctx, app.UpRequest{Dir: mustCwd(), RefreshAgents: r.RefreshAgents})
			if err != nil {
				return nil, err
			}
			return &upResult{Result: res, Op: "up"}, nil
		},
	},
	"rebuild": {
		Name:    "rebuild",
		Summary: "recreate containers from freshly compiled inputs",
		Usage:   "vibe rebuild [--refresh-agents] [--json]",
		Parse: func(args []string) (Request, error) {
			var req RebuildRequest
			return parseInto(args, "rebuild", &req.Options, func(fs *flag.FlagSet) any {
				fs.BoolVar(&req.RefreshAgents, "refresh-agents", false,
					"re-pull the channel-tracking agents (claude, codex, grok) to latest")
				return &req
			})
		},
		Run: func(ctx context.Context, a *app.App, req Request) (Result, error) {
			r := req.(*RebuildRequest)
			res, err := a.Up(ctx, app.UpRequest{Dir: mustCwd(), Force: true, RefreshAgents: r.RefreshAgents})
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
	"logs": {
		Name:    "logs",
		Summary: "stream container logs (dev container, or a named sidecar)",
		Usage:   "vibe logs [SERVICE] [-f] [--tail N]",
		Parse: func(args []string) (Request, error) {
			var req LogsRequest
			fs := newFlagSet("logs", &req.Options)
			fs.BoolVar(&req.Follow, "f", false, "follow new output")
			fs.BoolVar(&req.Follow, "follow", false, "alias for -f")
			fs.IntVar(&req.Tail, "tail", 0, "last N lines only (default: everything)")
			if err := parseArgs(fs, args); err != nil {
				return nil, err
			}
			rest := fs.Args()
			switch len(rest) {
			case 0:
			case 1:
				req.Service = rest[0]
			default:
				return nil, fmt.Errorf("at most one SERVICE argument")
			}
			return &req, nil
		},
		Run: func(ctx context.Context, a *app.App, req Request) (Result, error) {
			r := req.(*LogsRequest)
			err := a.Logs(ctx, app.LogsRequest{
				Dir:     mustCwd(),
				Service: r.Service,
				Follow:  r.Follow,
				Tail:    r.Tail,
				Streams: dockerapi.Streams{Out: os.Stdout, Err: os.Stderr},
			})
			return nil, err
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
		Summary: "attach to the main process, or a named in-container session",
		Usage:   "vibe attach [SESSION] [-u USER]",
		Parse: func(args []string) (Request, error) {
			var req AttachCmdRequest
			fs := newFlagSet("attach", &req.Options)
			fs.StringVar(&req.User, "u", "", "container user (default vscode)")
			if err := parseArgs(fs, args); err != nil {
				return nil, err
			}
			switch rest := fs.Args(); len(rest) {
			case 0:
			case 1:
				req.Session = rest[0]
			default:
				return nil, fmt.Errorf("at most one SESSION argument")
			}
			return &req, nil
		},
		Run: func(ctx context.Context, a *app.App, req Request) (Result, error) {
			r := req.(*AttachCmdRequest)
			cmd, restore := r.containerCommand()
			defer restore()
			res, err := a.Attach(ctx, app.AttachRequest{ContainerCommand: cmd, Session: r.Session})
			if err != nil {
				return nil, err
			}
			return &execResult{Code: res.ExitCode}, nil
		},
	},
}

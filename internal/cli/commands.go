package cli

import (
	"context"
	"flag"
	"fmt"
	"maps"
	"os"
	"strings"
	"time"

	"github.com/chrisdruta/vibe-tui-box/internal/app"
	"github.com/chrisdruta/vibe-tui-box/internal/envfile"
)

var commandOrder = []string{
	"version", "init", "register", "config", "up", "rebuild", "down", "status",
	"logs", "exec", "run", "shell", "attach", "ps", "forget",
	"provision", "update", "doctor", "bootstrap", "gc",
	"tui", "agent", "clip", "request", "dev",
}

var commandTable = map[string]Command{
	"version": {
		Name:    "version",
		Summary: "print engine version",
		Usage:   "vibe version [--json]",
		NoCwd:   true,
		Parse: func(args []string) (Request, error) {
			var req VersionRequest
			return parseInto(args, "version", &req.Options, nil)
		},
		Run: func(ctx context.Context, a *app.App, req Request, dir string) (Result, error) {
			return &versionResult{Info: a.Version()}, nil
		},
	},
	"register": {
		Name:    "register",
		Summary: "register the current project with the host engine",
		Usage:   "vibe register [--name NAME] [--json]",
		Parse: func(args []string) (Request, error) {
			var req RegisterRequest
			return parseInto(args, "register", &req.Options, func(fs *flag.FlagSet) any {
				fs.StringVar(&req.Name, "name", "", "display name (default: root directory name)")
				return &req
			})
		},
		Run: func(ctx context.Context, a *app.App, req Request, dir string) (Result, error) {
			r := req.(*RegisterRequest)
			res, err := a.Register(ctx, app.RegisterRequest{Dir: dir, DisplayName: r.Name})
			if err != nil {
				return nil, err
			}
			return &registerResult{Result: res}, nil
		},
	},
	"config": {
		Name:    "config",
		Summary: "print the canonical engine plan as JSON",
		Usage:   "vibe config",
		Parse: func(args []string) (Request, error) {
			var req ConfigRequest
			return parseInto(args, "config", &req.Options, nil)
		},
		Run: func(ctx context.Context, a *app.App, req Request, dir string) (Result, error) {
			res, err := a.Config(ctx, app.ConfigRequest{Dir: dir})
			if err != nil {
				return nil, err
			}
			return &configResult{Result: res}, nil
		},
	},
	"ps": {
		Name:    "ps",
		Summary: "list registered projects and the current project's agents",
		Usage:   "vibe ps [--json]",
		Parse: func(args []string) (Request, error) {
			var req PSRequest
			return parseInto(args, "ps", &req.Options, nil)
		},
		Run: func(ctx context.Context, a *app.App, req Request, dir string) (Result, error) {
			res, err := a.PS(ctx, app.PSRequest{Dir: dir})
			if err != nil {
				return nil, err
			}
			return &psResult{Result: res}, nil
		},
	},
	"forget": {
		Name:    "forget",
		Summary: "remove the current project's registration (workspace untouched)",
		Usage:   "vibe forget [--json]",
		Parse: func(args []string) (Request, error) {
			var req ForgetRequest
			return parseInto(args, "forget", &req.Options, nil)
		},
		Run: func(ctx context.Context, a *app.App, req Request, dir string) (Result, error) {
			res, err := a.Forget(ctx, app.ForgetRequest{Dir: dir})
			if err != nil {
				return nil, err
			}
			return &forgetResult{Result: res}, nil
		},
	},
}

// The remaining command groups live in commands_lifecycle.go,
// commands_release.go, and commands_ui.go; this is the single merge
// point.
func init() {
	for _, group := range []map[string]Command{lifecycleCommands, releaseCommands, uiCommands} {
		maps.Copy(commandTable, group)
	}
}

// Typed requests. Each embeds Options so the dispatcher can select the
// output mode without re-parsing.

type VersionRequest struct{ Options }

type RegisterRequest struct {
	Options
	Name string
}

type ConfigRequest struct{ Options }

type PSRequest struct{ Options }

type ForgetRequest struct{ Options }

type UpRequest struct{ Options }

type ProvisionRequest struct{ Options }

type InitRequest struct {
	Options
	Preset string
	// AutoMemory is tri-state: nil means the flag was not given and an
	// interactive init may ask instead.
	AutoMemory *bool
}

type DoctorRequest struct{ Options }

type BootstrapRequest struct{ Options }

type UpdateRequest struct {
	Options
	Version string
}

type GCRequest struct {
	Options
	DryRun bool
	MinAge time.Duration
}

type RebuildRequest struct{ Options }

type DownRequest struct {
	Options
	Volumes bool
}

type StatusRequest struct{ Options }

type LogsRequest struct {
	Options
	Service string
	Follow  bool
	Tail    int
}

// ExecRequest is shared by exec/run/shell/attach.
type ExecRequest struct {
	Options
	User    string
	Workdir string
	Env     envList
	Argv    []string
}

// AttachCmdRequest adds attach's optional session target.
type AttachCmdRequest struct {
	ExecRequest
	Session string
	Window  string
}

// containerCommand wires terminal streams into an app request. The
// returned restore function resets the terminal and must run before any
// further output.
func (r *ExecRequest) containerCommand(dir string) (app.ContainerCommand, func()) {
	streams, tty, restore := setupStreams(true)
	env := r.Env
	// The container process talks to THIS terminal through the exec
	// pty, but docker defaults the exec's TERM to plain "xterm" — an
	// inner tmux then misjudges its real outer terminal and drops
	// capabilities at the boundary (sync died there: the 3.7b
	// ghost-cursor flicker). Forward the truth; an explicit -e TERM
	// still wins (later duplicate entries override).
	if term := os.Getenv("TERM"); term != "" && tty {
		env = append([]envfile.Entry{{Key: "TERM", Value: term}}, r.Env...)
	}
	return app.ContainerCommand{
		Dir:     dir,
		User:    r.User,
		Workdir: r.Workdir,
		Env:     env,
		Argv:    r.Argv,
		TTY:     tty,
		Stdin:   true,
		Streams: streams,
	}, restore
}

// parseExecStyle builds the parser for exec-shaped commands. withArgv
// commands require a trailing command line.
func parseExecStyle(name string, withArgv bool) func(args []string) (Request, error) {
	return func(args []string) (Request, error) {
		var req ExecRequest
		fs := newFlagSet(name, &req.Options)
		fs.StringVar(&req.User, "u", "", "container user (default vscode)")
		if withArgv {
			fs.StringVar(&req.Workdir, "w", "", "working directory inside the container")
			fs.Var(&req.Env, "e", "environment entry KEY=VALUE (repeatable)")
		}
		if err := parseArgs(fs, args); err != nil {
			return nil, err
		}
		req.Argv = fs.Args()
		if withArgv && len(req.Argv) == 0 {
			return nil, fmt.Errorf("no command given (separate it with --)")
		}
		if !withArgv && len(req.Argv) > 0 {
			return nil, fmt.Errorf("unexpected argument %q", req.Argv[0])
		}
		return &req, nil
	}
}

// envList collects repeated -e KEY=VALUE flags as validated entries.
type envList []envfile.Entry

func (e *envList) String() string { return "" }

func (e *envList) Set(v string) error {
	key, value, ok := strings.Cut(v, "=")
	if !ok || key == "" {
		return fmt.Errorf("want KEY=VALUE, got %q", v)
	}
	*e = append(*e, envfile.Entry{Key: key, Value: value})
	return nil
}

// parseInto builds a flag set with the shared global flags, lets setup
// register command flags, and rejects positional leftovers.
func parseInto(args []string, name string, opts *Options, setup func(*flag.FlagSet) any) (Request, error) {
	fs := newFlagSet(name, opts)
	var req any
	if setup != nil {
		req = setup(fs)
	}
	if err := parseArgs(fs, args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if req == nil {
		// Commands without extra flags: the request is just the options.
		return opts, nil
	}
	return req, nil
}

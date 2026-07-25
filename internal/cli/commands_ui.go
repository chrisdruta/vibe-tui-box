package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/chrisdruta/vibe-tui-box/internal/app"
	"github.com/chrisdruta/vibe-tui-box/internal/domain"
)

// uiCommands covers the tmux UI, the rebuild broker, and dev mode.
// Typed requests for these commands follow.

type TuiRequest struct{ Options }

type RequestCmdRequest struct {
	Options
	Sub       string // list | show | approve | reject
	ID        string
	Candidate string
	Message   string
	Yes       bool
}

type DevCmdRequest struct {
	Options
	Sub string // on | sync | off | status
}

type RenderRequest struct {
	Options
	Project string
	Width   int
}

// ClipCmdRequest saves the host clipboard image for the agent: into the
// dev container's /tmp by default, or a workspace-relative DestDir.
type ClipCmdRequest struct {
	Options
	DestDir  string
	PathOnly bool
}

var uiCommands = map[string]Command{
	"tui": {
		Name:    "tui",
		Summary: "open the project's tmux session running the agent",
		Usage:   "vibe tui",
		Parse: func(args []string) (Request, error) {
			var req TuiRequest
			return parseInto(args, "tui", &req.Options, nil)
		},
		Run: func(ctx context.Context, a *app.App, req Request) (Result, error) {
			return nil, a.Tui(ctx, app.TuiRequest{Dir: mustCwd()})
		},
	},
	"agent": {
		Name:    "agent",
		Summary: "run the manifest's agent CLI in the dev container",
		Usage:   "vibe agent [-u USER] [--cold] [-a|--agent CMD] [-s|--session NAME] [--stop|--restart]",
		Parse:   parseAgentCmd,
		Run: func(ctx context.Context, a *app.App, req Request) (Result, error) {
			r := req.(*AgentCmdRequest)
			cmd, restore := r.containerCommand()
			defer restore()
			res, err := a.Agent(ctx, app.AgentRequest{
				ContainerCommand: cmd,
				Cold:             r.Cold,
				Agent:            r.Agent,
				Session:          r.Session,
				Stop:             r.Stop,
				Restart:          r.Restart,
				// The tui conf exports VIBE_NESTED=1 server-wide; the
				// marker rides into the container so its inner tmux
				// client is reapable when the UI dies.
				Nested: os.Getenv("VIBE_NESTED") == "1",
			})
			if err != nil {
				return nil, err
			}
			return &execResult{Code: res.ExitCode}, nil
		},
	},
	"clip": {
		Name:    "clip",
		Summary: "save the host clipboard image where the agent can read it",
		Usage:   "vibe clip [DIR] [--path-only]",
		Parse:   parseClipCmd,
		Run: func(ctx context.Context, a *app.App, req Request) (Result, error) {
			r := req.(*ClipCmdRequest)
			res, err := a.Clip(ctx, app.ClipRequest{
				Dir:      mustCwd(),
				DestDir:  r.DestDir,
				PathOnly: r.PathOnly,
				Env:      clipEnv(),
				Stdout:   os.Stdout,
				Stderr:   os.Stderr,
			})
			if err != nil {
				return nil, err
			}
			return &execResult{Code: res.ExitCode}, nil
		},
	},
	"request": {
		Name:    "request",
		Summary: "list, show, approve, or reject agent rebuild requests",
		Usage:   "vibe request list | show ID | approve DIGEST [--yes] | reject DIGEST [-m WHY]",
		Parse:   parseRequestCmd,
		Run:     runRequestCmd,
	},
	"dev": {
		Name:    "dev",
		Summary: "build and run the engine from source for this project",
		Usage:   "vibe dev on|sync|off|status",
		Parse: func(args []string) (Request, error) {
			var req DevCmdRequest
			fs := flag.NewFlagSet("dev", flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			fs.BoolVar(&req.JSON, "json", false, "")
			fs.BoolVar(&req.Quiet, "quiet", false, "")
			if err := fs.Parse(args); err != nil {
				return nil, err
			}
			rest := fs.Args()
			if len(rest) != 1 {
				return nil, fmt.Errorf("want one of: on, sync, off, status")
			}
			req.Sub = rest[0]
			return &req, nil
		},
		Run: func(ctx context.Context, a *app.App, req Request) (Result, error) {
			r := req.(*DevCmdRequest)
			switch r.Sub {
			case "on", "sync":
				res, err := a.DevOn(ctx, app.DevOnRequest{Dir: mustCwd()})
				if err != nil {
					return nil, err
				}
				return &devOnResult{Result: res}, nil
			case "off":
				res, err := a.DevOff(ctx, app.DevOffRequest{Dir: mustCwd()})
				if err != nil {
					return nil, err
				}
				return &devOffResult{Result: res}, nil
			case "status":
				res, err := a.DevStatus(ctx, app.DevStatusRequest{Dir: mustCwd()})
				if err != nil {
					return nil, err
				}
				return &devStatusResult{Result: res}, nil
			default:
				return nil, &usageError{msg: fmt.Sprintf("unknown dev subcommand %q", r.Sub)}
			}
		},
	},
	"_sidebar": renderCommand("_sidebar", (*app.App).RenderSidebar),
	"_state":   renderCommand("_state", (*app.App).RenderState),
	"_fleet":   renderCommand("_fleet", (*app.App).RenderFleet),
}

// AgentCmdRequest is the exec-shaped agent command plus its session
// pass-throughs: --cold (no repo instruction files), -a/--agent (run a
// different installed agent in its own session), -s/--session (a named
// parallel instance), --stop / --restart (end or replace the addressed
// persistent session instead of reattaching it).
type AgentCmdRequest struct {
	ExecRequest
	Cold    bool
	Agent   string
	Session string
	Stop    bool
	Restart bool
}

func parseAgentCmd(args []string) (Request, error) {
	var req AgentCmdRequest
	fs := flag.NewFlagSet("agent", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&req.JSON, "json", false, "emit versioned JSON")
	fs.BoolVar(&req.Quiet, "quiet", false, "suppress nonessential output")
	fs.StringVar(&req.User, "u", "", "container user (default vscode)")
	fs.BoolVar(&req.Cold, "cold", false, "start without repo instruction files")
	fs.StringVar(&req.Agent, "a", "", "run this installed agent instead of the manifest's")
	fs.StringVar(&req.Agent, "agent", "", "alias for -a")
	fs.StringVar(&req.Session, "s", "", "named parallel session")
	fs.StringVar(&req.Session, "session", "", "alias for -s")
	fs.BoolVar(&req.Stop, "stop", false, "stop the addressed agent session")
	fs.BoolVar(&req.Restart, "restart", false, "replace the addressed agent session")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if req.Stop && req.Restart {
		return nil, fmt.Errorf("--stop and --restart are mutually exclusive")
	}
	return &req, nil
}

// parseClipCmd accepts flags before or after the optional DIR — the v1
// verb allowed `vibe clip shots --path-only`, and stdlib flag parsing
// stops at the first positional, so the trailing form is folded in here.
func parseClipCmd(args []string) (Request, error) {
	var req ClipCmdRequest
	fs := flag.NewFlagSet("clip", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&req.JSON, "json", false, "emit versioned JSON")
	fs.BoolVar(&req.Quiet, "quiet", false, "suppress nonessential output")
	fs.BoolVar(&req.PathOnly, "path-only", false, "print only the container path on stdout")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	for _, arg := range fs.Args() {
		switch {
		case arg == "--path-only" || arg == "-path-only":
			req.PathOnly = true
		case len(arg) > 0 && arg[0] == '-':
			return nil, fmt.Errorf("unknown flag %q", arg)
		case req.DestDir != "":
			return nil, fmt.Errorf("unexpected argument %q", arg)
		default:
			req.DestDir = arg
		}
	}
	return &req, nil
}

// clipEnv curates the host environment clip-image.sh needs: tool
// discovery (powershell.exe / osascript / docker / clip.exe / pbcopy),
// the WSL interop variables without which Windows executables cannot
// launch from WSL, and the docker CLI's connection overrides.
func clipEnv() []string {
	var env []string
	for _, key := range []string{
		"PATH", "HOME", "TMPDIR",
		"WSLENV", "WSL_INTEROP", "WSL_DISTRO_NAME",
		"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG", "DOCKER_CERT_PATH", "DOCKER_TLS_VERIFY",
	} {
		if v := os.Getenv(key); v != "" {
			env = append(env, key+"="+v)
		}
	}
	return env
}

// usageError maps to the usage exit code.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func (e *usageError) Unwrap() error { return domain.ErrInvalid }

func parseRequestCmd(args []string) (Request, error) {
	var req RequestCmdRequest
	if len(args) == 0 {
		return nil, fmt.Errorf("want one of: list, show, approve, reject")
	}
	req.Sub = args[0]
	fs := flag.NewFlagSet("request", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&req.JSON, "json", false, "")
	fs.BoolVar(&req.Quiet, "quiet", false, "")
	fs.BoolVar(&req.Yes, "yes", false, "skip confirmation")
	fs.StringVar(&req.Message, "m", "", "rejection message")
	if err := fs.Parse(args[1:]); err != nil {
		return nil, err
	}
	rest := fs.Args()
	switch req.Sub {
	case "list":
		if len(rest) != 0 {
			return nil, fmt.Errorf("list takes no arguments")
		}
	case "show":
		if len(rest) != 1 {
			return nil, fmt.Errorf("show needs a request ID")
		}
		req.ID = rest[0]
	case "approve", "reject":
		if len(rest) != 1 {
			return nil, fmt.Errorf("%s needs a candidate digest", req.Sub)
		}
		req.Candidate = rest[0]
	default:
		return nil, fmt.Errorf("unknown request subcommand %q", req.Sub)
	}
	return &req, nil
}

func runRequestCmd(ctx context.Context, a *app.App, req Request) (Result, error) {
	r := req.(*RequestCmdRequest)
	switch r.Sub {
	case "list":
		res, err := a.RequestList(ctx, app.RequestListRequest{Dir: mustCwd()})
		if err != nil {
			return nil, err
		}
		return &requestListResult{Result: res}, nil
	case "show":
		id, err := domain.ParseRequestID(r.ID)
		if err != nil {
			return nil, err
		}
		res, err := a.RequestShow(ctx, app.RequestShowRequest{Dir: mustCwd(), ID: id})
		if err != nil {
			return nil, err
		}
		return &requestShowResult{Result: res}, nil
	case "approve", "reject":
		digest, err := domain.ParseDigest(r.Candidate)
		if err != nil {
			return nil, err
		}
		res, err := a.RequestDecide(ctx, app.RequestDecideRequest{
			Dir:       mustCwd(),
			Candidate: digest,
			Approve:   r.Sub == "approve",
			Message:   r.Message,
			Yes:       r.Yes,
		})
		if err != nil {
			return nil, err
		}
		return &requestDecideResult{Result: res}, nil
	}
	return nil, fmt.Errorf("unreachable")
}

// renderCommand builds one hidden renderer command; output is the raw
// protocol lines with no decoration.
func renderCommand(name string, fn func(*app.App, context.Context, app.RenderRequest) (app.RenderResult, error)) Command {
	return Command{
		Name:    name,
		Summary: "internal tmux renderer",
		Usage:   "vibe " + name + " [--project ID] [--width N]",
		Hidden:  true,
		Parse: func(args []string) (Request, error) {
			var req RenderRequest
			return parseInto(args, name, &req.Options, func(fs *flag.FlagSet) any {
				fs.StringVar(&req.Project, "project", "", "project ID")
				fs.IntVar(&req.Width, "width", 0, "width budget")
				return &req
			})
		},
		Run: func(ctx context.Context, a *app.App, req Request) (Result, error) {
			r := req.(*RenderRequest)
			res, err := fn(a, ctx, app.RenderRequest{
				Dir:     mustCwd(),
				Project: domain.ProjectID(r.Project),
				Width:   r.Width,
			})
			if err != nil {
				return nil, err
			}
			return &renderResult{Lines: res.Lines}, nil
		},
	}
}

// renderResult prints protocol lines verbatim in both modes.
type renderResult struct{ Lines []string }

func (r *renderResult) RenderHuman(w io.Writer) error {
	for _, line := range r.Lines {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

func (r *renderResult) RenderJSON(w io.Writer) error {
	return writeJSON(w, "render", r.Lines)
}

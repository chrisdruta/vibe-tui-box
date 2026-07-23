package cli

import (
	"context"
	"flag"
	"fmt"
	"io"

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
	Agent   string
	Message string
	Width   int
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
		Usage:   "vibe agent [-u USER]",
		Parse:   parseExecStyle("agent", false),
		Run: func(ctx context.Context, a *app.App, req Request) (Result, error) {
			r := req.(*ExecRequest)
			cmd, restore := r.containerCommand()
			defer restore()
			res, err := a.Agent(ctx, cmd)
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
	"_sidebar":    renderCommand("_sidebar", (*app.App).RenderSidebar),
	"_state":      renderCommand("_state", (*app.App).RenderState),
	"_fleet":      renderCommand("_fleet", (*app.App).RenderFleet),
	"_statusline": renderCommand("_statusline", (*app.App).RenderStatusline),
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
				fs.StringVar(&req.Agent, "agent", "", "agent name")
				fs.StringVar(&req.Message, "message", "", "status message")
				fs.IntVar(&req.Width, "width", 0, "width budget")
				return &req
			})
		},
		Run: func(ctx context.Context, a *app.App, req Request) (Result, error) {
			r := req.(*RenderRequest)
			res, err := fn(a, ctx, app.RenderRequest{
				Dir:     mustCwd(),
				Project: domain.ProjectID(r.Project),
				Agent:   r.Agent,
				Message: r.Message,
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

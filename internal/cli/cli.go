// Package cli dispatches argv to typed application requests and maps
// typed errors to stable exit codes. Only this layer (and terminal/
// tmuxui) produces user-facing text.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/chrisdruta/vibe-tui-box/internal/app"
	"github.com/chrisdruta/vibe-tui-box/internal/domain"
)

// Stable exit codes.
const (
	ExitOK          = 0
	ExitFailure     = 1
	ExitUsage       = 2
	ExitConfig      = 3
	ExitUnknown     = 4 // project/artifact not registered
	ExitConflict    = 5
	ExitUnavailable = 6
	ExitInterrupted = 130
)

// Request is a fully parsed, typed command request.
type Request any

// Result renders one command outcome as human text or a versioned JSON
// object; both derive from the same data.
type Result interface {
	RenderHuman(w io.Writer) error
	RenderJSON(w io.Writer) error
}

// Command binds one name to a parser and a runner. Parsers return
// typed requests; a partially parsed flag set never crosses into app.
type Command struct {
	Name    string
	Summary string
	Usage   string
	Hidden  bool
	Parse   func(args []string) (Request, error)
	Run     func(ctx context.Context, a *app.App, req Request) (Result, error)
}

// Options carries output mode selected by global flags.
type Options struct {
	JSON  bool
	Quiet bool
}

// Run dispatches args and returns the process exit code.
func Run(ctx context.Context, a *app.App, args []string) int {
	return run(ctx, a, args, os.Stdout, os.Stderr)
}

func run(ctx context.Context, a *app.App, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printHelp(stdout)
		if len(args) == 0 {
			return ExitUsage
		}
		return ExitOK
	}
	cmd, ok := commandTable[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "vibe: unknown command %q (see `vibe help`)\n", args[0])
		return ExitUsage
	}
	req, err := cmd.Parse(args[1:])
	if err != nil {
		fmt.Fprintf(stderr, "vibe %s: %v\nusage: %s\n", cmd.Name, err, cmd.Usage)
		return ExitUsage
	}
	res, err := cmd.Run(ctx, a, req)
	if err != nil {
		fmt.Fprintf(stderr, "vibe %s: %v\n", cmd.Name, err)
		return exitCode(err)
	}
	opts := optionsOf(req)
	if res != nil {
		var renderErr error
		if opts.JSON {
			renderErr = res.RenderJSON(stdout)
		} else if !opts.Quiet {
			renderErr = res.RenderHuman(stdout)
		}
		if renderErr != nil {
			fmt.Fprintf(stderr, "vibe %s: render: %v\n", cmd.Name, renderErr)
			return ExitFailure
		}
		// Container commands propagate the process's exit code verbatim.
		if coder, ok := res.(interface{ ExitCode() int }); ok {
			return coder.ExitCode()
		}
	}
	return ExitOK
}

// optioned is implemented by requests embedding Options.
type optioned interface{ options() Options }

func optionsOf(req Request) Options {
	if o, ok := req.(optioned); ok {
		return o.options()
	}
	return Options{}
}

func (o Options) options() Options { return o }

func exitCode(err error) int {
	switch {
	case errors.Is(err, domain.ErrCanceled), errors.Is(err, context.Canceled):
		return ExitInterrupted
	case errors.Is(err, domain.ErrInvalid):
		return ExitConfig
	case errors.Is(err, domain.ErrNotFound):
		return ExitUnknown
	case errors.Is(err, domain.ErrConflict):
		return ExitConflict
	case errors.Is(err, domain.ErrUnavailable), errors.Is(err, domain.ErrNotSupported):
		return ExitUnavailable
	default:
		return ExitFailure
	}
}

func printHelp(w io.Writer) {
	fmt.Fprintln(w, "vibe — containerized agent development harness")
	fmt.Fprintln(w, "\nCommands:")
	for _, name := range commandOrder {
		cmd := commandTable[name]
		if cmd.Hidden {
			continue
		}
		fmt.Fprintf(w, "  %-10s %s\n", cmd.Name, cmd.Summary)
	}
	fmt.Fprintln(w, "\nGlobal flags: --json (versioned JSON output), --quiet")
}

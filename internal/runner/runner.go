// Package runner isolates host subprocess execution. Commands run with
// an absolute executable path, explicit argv, and a fixed minimal
// environment — never the engine's ambient environment.
package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"

	"vibe/internal/domain"
)

// Invocation is one subprocess request. Path must be absolute (from
// Resolve); Env is the complete child environment.
type Invocation struct {
	Path   string
	Args   []string
	Dir    string
	Env    []string
	Stdin  io.Reader
	Stdout io.Writer // nil captures into Output.Stdout
	Stderr io.Writer // nil captures into Output.Stderr
}

// Output reports a completed subprocess.
type Output struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Runner executes host subprocesses.
type Runner interface {
	Run(ctx context.Context, inv Invocation) (Output, error)
}

// Host is the real Runner.
type Host struct{}

func NewHost() *Host { return &Host{} }

func (h *Host) Run(ctx context.Context, inv Invocation) (Output, error) {
	if !filepath.IsAbs(inv.Path) {
		return Output{}, fmt.Errorf("%w: executable path %q is not absolute", domain.ErrInvalid, inv.Path)
	}
	cmd := exec.CommandContext(ctx, inv.Path, inv.Args...)
	cmd.Dir = inv.Dir
	// os/exec treats a nil Env as "inherit os.Environ()", which would
	// leak the engine's ambient environment; an empty slice means empty.
	cmd.Env = inv.Env
	if cmd.Env == nil {
		cmd.Env = []string{}
	}
	cmd.Stdin = inv.Stdin

	var outBuf, errBuf bytes.Buffer
	if cmd.Stdout = inv.Stdout; cmd.Stdout == nil {
		cmd.Stdout = &outBuf
	}
	if cmd.Stderr = inv.Stderr; cmd.Stderr == nil {
		cmd.Stderr = &errBuf
	}

	err := cmd.Run()
	out := Output{Stdout: outBuf.Bytes(), Stderr: errBuf.Bytes()}
	var exit *exec.ExitError
	switch {
	case err == nil:
		return out, nil
	// Cancellation must win over ExitError: a child killed by the
	// context dies with an ExitError whose code is -1, which would
	// otherwise report as a normally completed run.
	case ctx.Err() != nil:
		return out, fmt.Errorf("%w: %s: %v", domain.ErrCanceled, filepath.Base(inv.Path), context.Cause(ctx))
	case errors.As(err, &exit):
		out.ExitCode = exit.ExitCode()
		return out, nil
	default:
		return out, fmt.Errorf("run %s: %w", filepath.Base(inv.Path), err)
	}
}

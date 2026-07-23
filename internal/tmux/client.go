// Package tmux is a typed client over the tmux binary. Every operation
// is explicit argv against an absolute executable; session identifiers
// derive from project IDs, never display names.
package tmux

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/runner"
)

// SessionID is an engine-generated tmux session name.
type SessionID string

// SessionFor derives the session for a project.
func SessionFor(id domain.ProjectID) SessionID {
	s := string(id)
	if len(s) > 12 {
		s = s[:12]
	}
	return SessionID("vibe-" + s)
}

// SessionSpec creates a detached session running one command.
type SessionSpec struct {
	ID      SessionID
	Workdir string
	Command []string // argv for the initial window
	Window  string
}

type Session struct {
	ID       SessionID
	Attached bool
}

// Client is the tmux surface the engine uses.
type Client interface {
	HasSession(ctx context.Context, id SessionID) (bool, error)
	EnsureSession(ctx context.Context, spec SessionSpec) error
	KillSession(ctx context.Context, id SessionID) error
	Attach(ctx context.Context, id SessionID) error
	SetOption(ctx context.Context, id SessionID, option, value string) error
	ListSessions(ctx context.Context) ([]Session, error)
}

// Binary drives a real tmux executable through the runner.
type Binary struct {
	path string
	run  runner.Runner
}

func NewBinary(path string, run runner.Runner) (*Binary, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: tmux not available", domain.ErrUnavailable)
	}
	return &Binary{path: path, run: run}, nil
}

func (b *Binary) exec(ctx context.Context, interactive bool, args ...string) (runner.Output, error) {
	inv := runner.Invocation{
		Path: b.path,
		Args: args,
		Env:  []string{"TERM=" + os.Getenv("TERM"), "HOME=" + os.Getenv("HOME"), "PATH=" + os.Getenv("PATH")},
	}
	if interactive {
		inv.Stdin = os.Stdin
		inv.Stdout = os.Stdout
		inv.Stderr = os.Stderr
	}
	return b.run.Run(ctx, inv)
}

func (b *Binary) HasSession(ctx context.Context, id SessionID) (bool, error) {
	out, err := b.exec(ctx, false, "has-session", "-t", "="+string(id))
	if err != nil {
		return false, err
	}
	return out.ExitCode == 0, nil
}

func (b *Binary) EnsureSession(ctx context.Context, spec SessionSpec) error {
	exists, err := b.HasSession(ctx, spec.ID)
	if err != nil || exists {
		return err
	}
	args := []string{"new-session", "-d", "-s", string(spec.ID)}
	if spec.Workdir != "" {
		args = append(args, "-c", spec.Workdir)
	}
	if spec.Window != "" {
		args = append(args, "-n", spec.Window)
	}
	args = append(args, spec.Command...)
	out, err := b.exec(ctx, false, args...)
	if err != nil {
		return err
	}
	if out.ExitCode != 0 {
		return fmt.Errorf("%w: tmux new-session: %s", domain.ErrUnavailable, strings.TrimSpace(string(out.Stderr)))
	}
	return nil
}

func (b *Binary) KillSession(ctx context.Context, id SessionID) error {
	out, err := b.exec(ctx, false, "kill-session", "-t", "="+string(id))
	if err != nil {
		return err
	}
	if out.ExitCode != 0 {
		return fmt.Errorf("%w: tmux session %s", domain.ErrNotFound, id)
	}
	return nil
}

func (b *Binary) Attach(ctx context.Context, id SessionID) error {
	out, err := b.exec(ctx, true, "attach-session", "-t", "="+string(id))
	if err != nil {
		return err
	}
	if out.ExitCode != 0 {
		return fmt.Errorf("%w: tmux attach exited %d", domain.ErrUnavailable, out.ExitCode)
	}
	return nil
}

func (b *Binary) SetOption(ctx context.Context, id SessionID, option, value string) error {
	out, err := b.exec(ctx, false, "set-option", "-t", "="+string(id), option, value)
	if err != nil {
		return err
	}
	if out.ExitCode != 0 {
		return fmt.Errorf("%w: tmux set-option %s: %s", domain.ErrInvalid, option, strings.TrimSpace(string(out.Stderr)))
	}
	return nil
}

func (b *Binary) ListSessions(ctx context.Context) ([]Session, error) {
	out, err := b.exec(ctx, false, "list-sessions", "-F", "#{session_name}\x1f#{session_attached}")
	if err != nil {
		return nil, err
	}
	if out.ExitCode != 0 {
		return nil, nil // no server running
	}
	var sessions []Session
	for line := range strings.SplitSeq(strings.TrimSpace(string(out.Stdout)), "\n") {
		name, attached, ok := strings.Cut(line, "\x1f")
		if !ok || !strings.HasPrefix(name, "vibe-") {
			continue
		}
		sessions = append(sessions, Session{ID: SessionID(name), Attached: attached != "0"})
	}
	return sessions, nil
}

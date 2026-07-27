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

// Socket is the engine's dedicated tmux server socket (tmux -L). A
// separate server keeps the TUI conf and sessions away from the user's
// personal tmux. Deliberately not v1's "vibe": a leftover v1 server may
// still hold that socket with an incompatible tmux and conf.
const Socket = "vibe-engine"

// SessionID is an engine-generated tmux session name.
type SessionID string

// EscapeFormat escapes s for literal inclusion in a tmux format
// string: # introduces expansion there (#{…}, #(…), #[…] styles) —
// #() executes a command — and ## renders one literal #. Anything
// operator- or workspace-authored must pass through here before it
// lands in a format.
func EscapeFormat(s string) string { return strings.ReplaceAll(s, "#", "##") }

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
	// ConfigureServer sets the conf file passed with -f on every
	// invocation; tmux applies it only when it starts the server.
	ConfigureServer(confPath string)
	HasSession(ctx context.Context, id SessionID) (bool, error)
	EnsureSession(ctx context.Context, spec SessionSpec) error
	KillSession(ctx context.Context, id SessionID) error
	Attach(ctx context.Context, id SessionID) error
	SetOption(ctx context.Context, id SessionID, option, value string) error
	// SetWindowOption sets a window option on the window holding the
	// given pane id (tmux -w resolution) — the self-stamp channel a
	// pane-resident engine process (`vibe agent`, `vibe attach`) uses
	// to mark its own window.
	SetWindowOption(ctx context.Context, pane, option, value string) error
	SetGlobalOption(ctx context.Context, option, value string) error
	SetEnvironment(ctx context.Context, name, value string) error
	ListSessions(ctx context.Context) ([]Session, error)
	// Version reports the tmux binary's version ("3.7b") — `tmux -V`
	// answers without a server, so this works before any session
	// exists. Doctor uses it for the preview-window sixel floor.
	Version(ctx context.Context) (string, error)
}

// Binary drives a real tmux executable through the runner. All commands
// target the engine's dedicated server socket.
type Binary struct {
	path string
	conf string
	run  runner.Runner
}

func NewBinary(path string, run runner.Runner) (*Binary, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: tmux not available", domain.ErrUnavailable)
	}
	return &Binary{path: path, run: run}, nil
}

func (b *Binary) ConfigureServer(confPath string) { b.conf = confPath }

func (b *Binary) exec(ctx context.Context, interactive bool, args ...string) (runner.Output, error) {
	global := []string{"-L", Socket}
	if b.conf != "" {
		global = append(global, "-f", b.conf)
	}
	env := []string{"TERM=" + os.Getenv("TERM"), "HOME=" + os.Getenv("HOME"), "PATH=" + os.Getenv("PATH")}
	// Locale carries through or tmux treats the client terminal as
	// non-UTF-8 and renders every non-ASCII glyph as "_".
	locale := false
	for _, key := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := os.Getenv(key); v != "" {
			env = append(env, key+"="+v)
			locale = true
		}
	}
	if !locale {
		env = append(env, "LANG=C.UTF-8")
	}
	inv := runner.Invocation{
		Path: b.path,
		Args: append(global, args...),
		Env:  env,
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
	// No "=" exact-match prefix here: set-option takes a target-pane,
	// and tmux 3.7's pane-target resolution rejects "=" ("no such
	// session"). Exact-name resolution still wins for a live session.
	out, err := b.exec(ctx, false, "set-option", "-t", string(id), option, value)
	if err != nil {
		return err
	}
	if out.ExitCode != 0 {
		return fmt.Errorf("%w: tmux set-option %s: %s", domain.ErrInvalid, option, strings.TrimSpace(string(out.Stderr)))
	}
	return nil
}

func (b *Binary) SetWindowOption(ctx context.Context, pane, option, value string) error {
	out, err := b.exec(ctx, false, "set-option", "-w", "-t", pane, option, value)
	if err != nil {
		return err
	}
	if out.ExitCode != 0 {
		return fmt.Errorf("%w: tmux set-option -w %s: %s", domain.ErrInvalid, option, strings.TrimSpace(string(out.Stderr)))
	}
	return nil
}

func (b *Binary) SetGlobalOption(ctx context.Context, option, value string) error {
	out, err := b.exec(ctx, false, "set-option", "-g", option, value)
	if err != nil {
		return err
	}
	if out.ExitCode != 0 {
		return fmt.Errorf("%w: tmux set-option -g %s: %s", domain.ErrInvalid, option, strings.TrimSpace(string(out.Stderr)))
	}
	return nil
}

func (b *Binary) SetEnvironment(ctx context.Context, name, value string) error {
	out, err := b.exec(ctx, false, "set-environment", "-g", name, value)
	if err != nil {
		return err
	}
	if out.ExitCode != 0 {
		return fmt.Errorf("%w: tmux set-environment %s: %s", domain.ErrInvalid, name, strings.TrimSpace(string(out.Stderr)))
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

func (b *Binary) Version(ctx context.Context) (string, error) {
	out, err := b.exec(ctx, false, "-V")
	if err != nil {
		return "", err
	}
	if out.ExitCode != 0 {
		return "", fmt.Errorf("%w: tmux -V exited %d", domain.ErrUnavailable, out.ExitCode)
	}
	return strings.TrimPrefix(strings.TrimSpace(string(out.Stdout)), "tmux "), nil
}

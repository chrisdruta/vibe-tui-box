package runner

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"vibe/internal/domain"
)

func TestRunRejectsRelativePath(t *testing.T) {
	_, err := NewHost().Run(context.Background(), Invocation{Path: "sh"})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("relative path: got %v, want ErrInvalid", err)
	}
}

func TestRunExitCodes(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		code int
	}{
		{"success", []string{"-c", "exit 0"}, 0},
		{"failure", []string{"-c", "exit 7"}, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := NewHost().Run(context.Background(), Invocation{Path: "/bin/sh", Args: tc.args})
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if out.ExitCode != tc.code {
				t.Fatalf("exit code: got %d, want %d", out.ExitCode, tc.code)
			}
		})
	}
}

func TestRunCanceledMidRunIsErrCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	// exec: the shell must BE the sleeper, or the killed shell leaves a
	// grandchild holding the output pipe and Run blocks on the copy.
	_, err := NewHost().Run(ctx, Invocation{Path: "/bin/sh", Args: []string{"-c", "exec sleep 30"}})
	if !errors.Is(err, domain.ErrCanceled) {
		t.Fatalf("canceled mid-run: got %v, want ErrCanceled", err)
	}
}

func TestRunCanceledBeforeStartIsErrCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewHost().Run(ctx, Invocation{Path: "/bin/true"})
	if !errors.Is(err, domain.ErrCanceled) {
		t.Fatalf("canceled before start: got %v, want ErrCanceled", err)
	}
}

func TestRunNilEnvIsEmptyChildEnvironment(t *testing.T) {
	t.Setenv("VIBE_RUNNER_LEAK_PROBE", "leaked")
	out, err := NewHost().Run(context.Background(), Invocation{Path: "/usr/bin/env"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(string(out.Stdout), "VIBE_RUNNER_LEAK_PROBE") {
		t.Fatalf("nil Env inherited ambient environment:\n%s", out.Stdout)
	}
}

func TestRequireTmux(t *testing.T) {
	if _, err := (Executables{}).RequireTmux(); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("empty tmux: got %v, want ErrUnavailable", err)
	}
	path, err := (Executables{Tmux: "/usr/bin/tmux"}).RequireTmux()
	if err != nil || path != "/usr/bin/tmux" {
		t.Fatalf("resolved tmux: got %q, %v", path, err)
	}
}

package runner

import (
	"fmt"
	"os/exec"
	"path/filepath"
)

// Executables holds absolute paths to the host tools the engine may
// invoke, resolved once at startup.
type Executables struct {
	Tmux string // "" when tmux is absent; TUI commands then fail with ErrUnavailable
	Git  string // "" when git is absent; churn display then stays empty
}

// Resolve looks up host executables on the current PATH. Both are
// optional; everything else the engine needs is the Docker API socket,
// not a binary.
func Resolve() (Executables, error) {
	var ex Executables
	if path, err := exec.LookPath("tmux"); err == nil {
		abs, err := filepath.Abs(path)
		if err != nil {
			return Executables{}, fmt.Errorf("resolve tmux: %w", err)
		}
		ex.Tmux = abs
	}
	if path, err := exec.LookPath("git"); err == nil {
		abs, err := filepath.Abs(path)
		if err != nil {
			return Executables{}, fmt.Errorf("resolve git: %w", err)
		}
		ex.Git = abs
	}
	return ex, nil
}

package runner

import (
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
)

// Executables holds absolute paths to the host tools the engine may
// invoke, resolved once at startup.
type Executables struct {
	Tmux string // "" when tmux is absent; TUI commands then fail with ErrUnavailable
}

// Resolve looks up host executables on the current PATH. Only tmux is
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
	return ex, nil
}

// RequireTmux returns the tmux path or a typed unavailable error.
func (e Executables) RequireTmux() (string, error) {
	if e.Tmux == "" {
		return "", fmt.Errorf("%w: tmux not found on PATH", domain.ErrUnavailable)
	}
	return e.Tmux, nil
}

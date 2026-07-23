// Package paths owns the host filesystem layout under ~/.vibe and
// canonical project-root discovery. Callers receive absolute paths from
// here; core packages never clean arbitrary strings themselves.
package paths

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
)

// Layout is the fixed ~/.vibe directory tree. All fields are absolute.
type Layout struct {
	Root       string // ~/.vibe
	Bin        string // engine binaries the shim execs
	Artifacts  string // immutable release artifacts by digest
	State      string // mutable engine state
	Projects   string // registry records
	Candidates string // immutable candidates by digest
	Snapshots  string // immutable input snapshots by digest
	Broker     string // per-project request results
	Locks      string // advisory lock files
	Staging    string // same-filesystem staging for atomic publishes
}

// NewLayout derives the layout from an absolute home directory. It does
// not touch the filesystem; call Ensure to create the tree.
func NewLayout(home string) (Layout, error) {
	if !filepath.IsAbs(home) {
		return Layout{}, fmt.Errorf("%w: home %q is not absolute", domain.ErrInvalid, home)
	}
	root := filepath.Join(home, ".vibe")
	state := filepath.Join(root, "state")
	return Layout{
		Root:       root,
		Bin:        filepath.Join(root, "bin"),
		Artifacts:  filepath.Join(root, "artifacts"),
		State:      state,
		Projects:   filepath.Join(state, "projects"),
		Candidates: filepath.Join(state, "candidates"),
		Snapshots:  filepath.Join(state, "snapshots"),
		Broker:     filepath.Join(state, "broker"),
		Locks:      filepath.Join(state, "locks"),
		Staging:    filepath.Join(state, "staging"),
	}, nil
}

func (l Layout) Validate() error {
	for _, dir := range l.dirs() {
		if dir == "" || !filepath.IsAbs(dir) {
			return fmt.Errorf("%w: layout directory %q is not absolute", domain.ErrInvalid, dir)
		}
	}
	return nil
}

// Ensure creates every layout directory with owner-only permissions.
func (l Layout) Ensure() error {
	if err := l.Validate(); err != nil {
		return err
	}
	for _, dir := range l.dirs() {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("ensure layout: %w", err)
		}
	}
	return nil
}

func (l Layout) dirs() []string {
	return []string{
		l.Root, l.Bin, l.Artifacts, l.State, l.Projects,
		l.Candidates, l.Snapshots, l.Broker, l.Locks, l.Staging,
	}
}

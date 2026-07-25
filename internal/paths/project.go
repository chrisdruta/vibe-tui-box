package paths

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
)

// ManifestRelPath locates the project manifest inside a project root.
const ManifestRelPath = ".vibe/vibe.yaml"

// Root is a canonical project root: an absolute, symlink-resolved path
// plus the platform identity of the directory it named at discovery.
type Root struct {
	Path     string
	Identity FileIdentity
}

// Discover walks physical parent directories from start looking for
// .vibe/vibe.yaml. It returns the canonical absolute root path plus its
// file identity, or ErrNotFound when no manifest exists on the walk.
func Discover(start string) (Root, error) {
	abs, err := filepath.Abs(start)
	if err != nil {
		return Root{}, fmt.Errorf("discover project root: %w", err)
	}
	// Resolve symlinks once at the entry point; the upward walk is then
	// over physical parents.
	canon, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return Root{}, fmt.Errorf("discover project root: %w", err)
	}
	for dir := canon; ; dir = filepath.Dir(dir) {
		info, err := os.Lstat(filepath.Join(dir, ManifestRelPath))
		if err == nil && info.Mode().IsRegular() {
			identity, err := Identity(dir)
			if err != nil {
				return Root{}, err
			}
			return Root{Path: dir, Identity: identity}, nil
		}
		if err != nil && !os.IsNotExist(err) {
			return Root{}, fmt.Errorf("discover project root: %w", err)
		}
		if dir == filepath.Dir(dir) {
			return Root{}, fmt.Errorf("%w: no %s at or above %s", domain.ErrNotFound, ManifestRelPath, canon)
		}
	}
}

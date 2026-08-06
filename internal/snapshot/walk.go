package snapshot

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"vibe/internal/domain"
	"vibe/internal/paths"
)

// walker copies allowlisted entries from an open project root into a
// staging directory, enforcing limits and rejecting anything that is
// not a regular file or directory.
type walker struct {
	root    *os.Root
	staging string
	limits  Limits
	files   int
	total   int64
}

func newWalker(projectRoot paths.Root, staging string, limits Limits) (*walker, error) {
	root, err := os.OpenRoot(projectRoot.Path)
	if err != nil {
		return nil, fmt.Errorf("open project root: %w", err)
	}
	// The root was registered by identity; refuse to snapshot whatever
	// else may now occupy the registered path.
	info, err := root.Stat(".")
	if err != nil {
		root.Close()
		return nil, fmt.Errorf("stat project root: %w", err)
	}
	if got := paths.IdentityFromInfo(info); got != projectRoot.Identity {
		root.Close()
		return nil, fmt.Errorf("%w: project root %s changed identity", domain.ErrConflict, projectRoot.Path)
	}
	return &walker{root: root, staging: staging, limits: limits.withDefaults()}, nil
}

func (w *walker) Close() error { return w.root.Close() }

func (w *walker) copyEntry(e Entry) error {
	if err := validateRel(e.Source); err != nil {
		return fmt.Errorf("%w: snapshot source %q: %v", domain.ErrInvalid, e.Source, err)
	}
	if err := validateRel(e.Dest); err != nil {
		return fmt.Errorf("%w: snapshot dest %q: %v", domain.ErrInvalid, e.Dest, err)
	}
	info, err := w.root.Lstat(e.Source)
	if err != nil {
		if os.IsNotExist(err) {
			if e.Optional {
				return nil
			}
			return fmt.Errorf("%w: snapshot input %s", domain.ErrNotFound, e.Source)
		}
		return fmt.Errorf("stat %s: %w", e.Source, err)
	}
	switch {
	case info.Mode().IsRegular():
		return w.copyFile(e.Source, e.Dest, 1)
	case info.IsDir():
		return w.copyDir(e.Source, e.Dest)
	default:
		return w.reject(e.Source, info.Mode())
	}
}

func (w *walker) copyDir(source, dest string) error {
	sub, err := fs.Sub(w.root.FS(), source)
	if err != nil {
		return fmt.Errorf("open %s: %w", source, err)
	}
	return fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walk %s: %w", source, err)
		}
		depth := 1 + strings.Count(p, "/")
		if depth > w.limits.MaxDepth {
			return fmt.Errorf("%w: %s/%s exceeds depth %d", domain.ErrInvalid, source, p, w.limits.MaxDepth)
		}
		srcPath, destPath := source, dest
		if p != "." {
			srcPath = source + "/" + p
			destPath = dest + "/" + p
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(filepath.Join(w.staging, filepath.FromSlash(destPath)), 0o700)
		case d.Type().IsRegular():
			return w.copyFile(srcPath, destPath, depth)
		default:
			return w.reject(srcPath, d.Type())
		}
	})
}

func (w *walker) copyFile(source, dest string, depth int) error {
	if depth > w.limits.MaxDepth {
		return fmt.Errorf("%w: %s exceeds depth %d", domain.ErrInvalid, source, w.limits.MaxDepth)
	}
	if w.files++; w.files > w.limits.MaxFiles {
		return fmt.Errorf("%w: snapshot exceeds %d files", domain.ErrInvalid, w.limits.MaxFiles)
	}
	src, err := w.root.Open(source)
	if err != nil {
		return fmt.Errorf("open %s: %w", source, err)
	}
	defer src.Close()

	before, err := src.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", source, err)
	}
	// Re-check the opened descriptor: the Lstat/ReadDir result could
	// have been swapped before Open (Open itself stays confined to the
	// root, but the target must still be a regular file).
	if !before.Mode().IsRegular() {
		return w.reject(source, before.Mode())
	}
	if nlink := paths.LinkCount(before); nlink > 1 {
		return fmt.Errorf("%w: %s is hard-linked (%d links)", domain.ErrInvalid, source, nlink)
	}
	if before.Size() > w.limits.MaxFileBytes {
		return fmt.Errorf("%w: %s exceeds %d bytes", domain.ErrInvalid, source, w.limits.MaxFileBytes)
	}
	if w.total += before.Size(); w.total > w.limits.MaxTotalBytes {
		return fmt.Errorf("%w: snapshot exceeds %d total bytes", domain.ErrInvalid, w.limits.MaxTotalBytes)
	}

	target := filepath.Join(w.staging, filepath.FromSlash(dest))
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	perm := os.FileMode(0o644)
	if before.Mode()&0o100 != 0 {
		perm = 0o755
	}
	dst, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return fmt.Errorf("stage %s: %w", dest, err)
	}
	defer dst.Close()
	n, err := io.Copy(dst, io.LimitReader(src, before.Size()+1))
	if err != nil {
		return fmt.Errorf("copy %s: %w", source, err)
	}

	after, err := src.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", source, err)
	}
	if changedDuringCopy(before, after, n) {
		return fmt.Errorf("%w: %s changed during snapshot", domain.ErrConflict, source)
	}
	return nil
}

// changedDuringCopy is the concurrent-mutation abort: the copy must
// have moved exactly the promised bytes, and the open descriptor's
// size and mtime must still match the pre-copy stat. A same-size
// same-mtime rewrite is below this check's resolution — the digest
// then records whichever content the copy actually read, which is
// still one consistent frozen input.
func changedDuringCopy(before, after fs.FileInfo, copied int64) bool {
	return copied != before.Size() ||
		after.Size() != before.Size() ||
		!after.ModTime().Equal(before.ModTime())
}

func (w *walker) reject(p string, mode fs.FileMode) error {
	kind := "special file"
	switch {
	case mode&fs.ModeSymlink != 0:
		kind = "symlink"
	case mode&fs.ModeSocket != 0:
		kind = "socket"
	case mode&fs.ModeDevice != 0:
		kind = "device"
	case mode&fs.ModeNamedPipe != 0:
		kind = "FIFO"
	}
	return fmt.Errorf("%w: %s is a %s; snapshots accept regular files and directories only", domain.ErrInvalid, p, kind)
}

// validateRel accepts clean, slash-separated, workspace-confined
// relative paths.
func validateRel(p string) error {
	switch {
	case p == "" || p == ".":
		return fmt.Errorf("empty path")
	case strings.HasPrefix(p, "/"):
		return fmt.Errorf("must be relative")
	case strings.Contains(p, "\\"):
		return fmt.Errorf("must use forward slashes")
	case path.Clean(p) != p, p == "..", strings.HasPrefix(p, "../"):
		return fmt.Errorf("must be a clean path inside the workspace")
	}
	return nil
}

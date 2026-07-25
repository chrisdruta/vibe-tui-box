package store

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
)

// TreeEntry is one entry in a canonical tree manifest, ordered by
// slash-separated relative path.
type TreeEntry struct {
	Type   byte // 'f' or 'd'
	Mode   uint32
	Size   int64
	Path   string // slash-separated, relative to the tree root
	Digest domain.Digest
}

// DigestTree computes the canonical Merkle digest of a directory tree.
// Each entry contributes one line:
//
//	type NUL mode NUL size NUL relative-path NUL content-digest LF
//
// File modes are normalized to 0644/0755 by the owner-execute bit and
// directories to 0755, so the digest is identical across platforms and
// umasks for identical content. Symlinks and special files are errors.
func DigestTree(root string) (domain.Digest, []TreeEntry, error) {
	var entries []TreeEntry
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		switch {
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}
			digest, err := digestFile(p)
			if err != nil {
				return err
			}
			entries = append(entries, TreeEntry{
				Type:   'f',
				Mode:   normalizeFileMode(info.Mode()),
				Size:   info.Size(),
				Path:   rel,
				Digest: digest,
			})
		case d.IsDir():
			entries = append(entries, TreeEntry{Type: 'd', Mode: 0o755, Path: rel})
		default:
			return fmt.Errorf("%w: %s is not a regular file or directory", domain.ErrInvalid, rel)
		}
		return nil
	})
	if err != nil {
		return domain.Digest{}, nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })

	var manifest strings.Builder
	for _, e := range entries {
		content := "-"
		if e.Type == 'f' {
			content = e.Digest.String()
		}
		manifest.WriteString(string(e.Type))
		manifest.WriteByte(0)
		manifest.WriteString("0" + strconv.FormatUint(uint64(e.Mode), 8))
		manifest.WriteByte(0)
		manifest.WriteString(strconv.FormatInt(e.Size, 10))
		manifest.WriteByte(0)
		manifest.WriteString(e.Path)
		manifest.WriteByte(0)
		manifest.WriteString(content)
		manifest.WriteByte('\n')
	}
	return domain.SHA256([]byte(manifest.String())), entries, nil
}

func normalizeFileMode(m fs.FileMode) uint32 {
	if m&0o100 != 0 {
		return 0o755
	}
	return 0o644
}

func digestFile(p string) (domain.Digest, error) {
	f, err := os.Open(p)
	if err != nil {
		return domain.Digest{}, err
	}
	defer f.Close()
	return domain.SHA256Reader(f)
}

// WriteFileAtomic writes data next to path and renames it into place,
// fsyncing the file and its directory.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	return syncDir(dir)
}

// syncTree fsyncs every file and directory beneath root.
func syncTree(root string) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return fmt.Errorf("%w: %s is not a regular file or directory", domain.ErrInvalid, p)
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		return f.Sync()
	})
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

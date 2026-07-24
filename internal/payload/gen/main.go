// Command gen regenerates payload/manifest.json from the payload tree
// on disk, capturing the file modes go:embed cannot. Run via
// `go generate ./internal/payload`; CI fails when the tracked manifest
// drifts from the tree.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/payload"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "genpayload:", err)
		os.Exit(1)
	}
}

func run() error {
	// go generate runs in internal/payload; the tree is two levels up.
	root := filepath.Join("..", "..", "payload")
	// Theme renderings first, so the walk digests the fresh bytes.
	if err := renderTheme(root); err != nil {
		return err
	}
	var files []payload.File
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == payload.ManifestPath {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", rel)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		content, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		mode := "0644"
		if info.Mode()&0o100 != 0 {
			mode = "0755"
		}
		files = append(files, payload.File{
			Path:   rel,
			Mode:   mode,
			Size:   int64(len(content)),
			Digest: domain.SHA256(content),
		})
		return nil
	})
	if err != nil {
		return err
	}
	encoded, err := payload.EncodeManifest(files)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, payload.ManifestPath), encoded, 0o644)
}

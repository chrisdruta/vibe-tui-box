package payload

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"vibe/internal/domain"
)

//go:generate go run ./gen

// Bundle is the verified embedded payload. Construction parses the
// manifest and confirms every embedded file matches it exactly, so a
// bundle that exists is a bundle that verifies.
type Bundle struct {
	fsys     fs.FS
	manifest Manifest
}

// New wraps the embedded payload filesystem rooted at the directory
// containing manifest.json (pass fs.Sub of the repo embed for
// "payload").
func New(fsys fs.FS) (*Bundle, error) {
	data, err := fs.ReadFile(fsys, ManifestPath)
	if err != nil {
		return nil, fmt.Errorf("embedded payload manifest: %w", err)
	}
	manifest, err := ParseManifest(data)
	if err != nil {
		return nil, err
	}

	// Every embedded file must be in the manifest and vice versa.
	embedded := map[string]bool{}
	err = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || p == ManifestPath {
			return nil
		}
		embedded[p] = true
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk embedded payload: %w", err)
	}
	for _, f := range manifest.Files {
		content, err := fs.ReadFile(fsys, f.Path)
		if err != nil {
			return nil, fmt.Errorf("%w: payload file %s is in the manifest but not embedded", domain.ErrConflict, f.Path)
		}
		if int64(len(content)) != f.Size || domain.SHA256(content) != f.Digest {
			return nil, fmt.Errorf("%w: payload file %s does not match its manifest entry (regenerate with go generate ./internal/payload)", domain.ErrConflict, f.Path)
		}
		delete(embedded, f.Path)
	}
	if len(embedded) > 0 {
		for p := range embedded {
			return nil, fmt.Errorf("%w: payload file %s is embedded but not in the manifest (regenerate with go generate ./internal/payload)", domain.ErrConflict, p)
		}
	}
	return &Bundle{fsys: fsys, manifest: manifest}, nil
}

// Digest is the payload content digest from the manifest.
func (b *Bundle) Digest() domain.Digest { return b.manifest.Digest }

// Extract writes the verified payload into destination (which must not
// contain a previous payload), applying the manifest's modes, and
// returns the payload digest. The manifest itself is written too, so
// extracted payloads are self-describing.
func (b *Bundle) Extract(ctx context.Context, destination string) (domain.Digest, error) {
	for _, f := range b.manifest.Files {
		if err := ctx.Err(); err != nil {
			return domain.Digest{}, fmt.Errorf("%w: extract payload: %v", domain.ErrCanceled, context.Cause(ctx))
		}
		perm, err := f.Perm()
		if err != nil {
			return domain.Digest{}, err
		}
		content, err := fs.ReadFile(b.fsys, f.Path)
		if err != nil {
			return domain.Digest{}, fmt.Errorf("read embedded %s: %w", f.Path, err)
		}
		target := filepath.Join(destination, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return domain.Digest{}, err
		}
		if err := writeExclusive(target, content, perm); err != nil {
			return domain.Digest{}, err
		}
	}
	encoded, err := EncodeManifest(b.manifest.Files)
	if err != nil {
		return domain.Digest{}, err
	}
	if err := writeExclusive(filepath.Join(destination, ManifestPath), encoded, 0o644); err != nil {
		return domain.Digest{}, err
	}
	return b.manifest.Digest, nil
}

func writeExclusive(path string, content []byte, perm fs.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return fmt.Errorf("extract payload: %w", err)
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// presetDir is where presets live inside the payload.
const presetDir = "presets"

// CommonPreset is the shared overlay every preset renders on top of
// (AGENTS.md, hook samples). It is not a selectable preset.
const CommonPreset = "common"

// Preset is one project template set: slash-relative file paths (within
// the preset) to contents. Rendering is the initproject package's job.
type Preset struct {
	Name  string
	Files map[string][]byte
}

// Names lists available presets sorted.
func (b *Bundle) Names() []string {
	seen := map[string]bool{}
	for _, f := range b.manifest.Files {
		if rest, ok := strings.CutPrefix(f.Path, presetDir+"/"); ok {
			name, _, found := strings.Cut(rest, "/")
			if found {
				seen[name] = true
			}
		}
	}
	delete(seen, CommonPreset)
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Preset loads one preset's files.
func (b *Bundle) Preset(name string) (Preset, error) {
	prefix := presetDir + "/" + name + "/"
	preset := Preset{Name: name, Files: map[string][]byte{}}
	for _, f := range b.manifest.Files {
		rel, ok := strings.CutPrefix(f.Path, prefix)
		if !ok {
			continue
		}
		content, err := fs.ReadFile(b.fsys, f.Path)
		if err != nil {
			return Preset{}, fmt.Errorf("read preset file %s: %w", f.Path, err)
		}
		preset.Files[rel] = content
	}
	if len(preset.Files) == 0 {
		return Preset{}, fmt.Errorf("%w: preset %q (available: %s)", domain.ErrNotFound, name, strings.Join(b.Names(), ", "))
	}
	return preset, nil
}

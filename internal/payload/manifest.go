// Package payload exposes the embedded container payload and project
// presets as verified logical resources. The tracked manifest carries
// per-file modes and digests (go:embed loses modes); extraction
// verifies every file against it.
package payload

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strconv"

	"vibe/internal/domain"
)

// ManifestPath is the manifest's location inside the payload tree.
const ManifestPath = "manifest.json"

const manifestFormat = 1

// File is one payload file. Path is slash-relative to the payload root;
// Mode is the octal permission string ("0755").
type File struct {
	Path   string        `json:"path"`
	Mode   string        `json:"mode"`
	Size   int64         `json:"size"`
	Digest domain.Digest `json:"digest"`
}

type Manifest struct {
	Format int           `json:"format"`
	Files  []File        `json:"files"`
	Digest domain.Digest `json:"digest"`
}

// Perm parses the file's octal mode string.
func (f File) Perm() (fs.FileMode, error) {
	n, err := strconv.ParseUint(f.Mode, 8, 32)
	if err != nil || n&^uint64(0o777) != 0 {
		return 0, fmt.Errorf("%w: payload mode %q for %s", domain.ErrInvalid, f.Mode, f.Path)
	}
	return fs.FileMode(n), nil
}

// ComputeDigest derives the payload digest from the manifest's own
// entries using the store's canonical line format:
//
//	f NUL mode NUL size NUL path NUL digest LF
//
// sorted by path. The manifest file itself is not an entry.
func ComputeDigest(files []File) domain.Digest {
	sorted := append([]File(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	var buf bytes.Buffer
	for _, f := range sorted {
		buf.WriteString("f")
		buf.WriteByte(0)
		buf.WriteString(f.Mode)
		buf.WriteByte(0)
		buf.WriteString(strconv.FormatInt(f.Size, 10))
		buf.WriteByte(0)
		buf.WriteString(f.Path)
		buf.WriteByte(0)
		buf.WriteString(f.Digest.String())
		buf.WriteByte('\n')
	}
	return domain.SHA256(buf.Bytes())
}

// ParseManifest decodes and structurally validates a manifest.
func ParseManifest(data []byte) (Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("%w: payload manifest: %v", domain.ErrInvalid, err)
	}
	if m.Format != manifestFormat {
		return Manifest{}, fmt.Errorf("%w: payload manifest format %d (supported: %d)", domain.ErrNotSupported, m.Format, manifestFormat)
	}
	seen := map[string]bool{}
	for _, f := range m.Files {
		if f.Path == "" || f.Path == ManifestPath || seen[f.Path] {
			return Manifest{}, fmt.Errorf("%w: payload manifest entry %q", domain.ErrInvalid, f.Path)
		}
		if !fs.ValidPath(f.Path) {
			return Manifest{}, fmt.Errorf("%w: payload path %q", domain.ErrInvalid, f.Path)
		}
		if _, err := f.Perm(); err != nil {
			return Manifest{}, err
		}
		if f.Digest.IsZero() {
			return Manifest{}, fmt.Errorf("%w: payload entry %s has no digest", domain.ErrInvalid, f.Path)
		}
		seen[f.Path] = true
	}
	if got := ComputeDigest(m.Files); got != m.Digest {
		return Manifest{}, fmt.Errorf("%w: payload manifest digest %s does not match entries (%s)", domain.ErrConflict, m.Digest, got)
	}
	return m, nil
}

// EncodeManifest produces the deterministic tracked form.
func EncodeManifest(files []File) ([]byte, error) {
	sorted := append([]File(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	m := Manifest{Format: manifestFormat, Files: sorted, Digest: ComputeDigest(sorted)}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

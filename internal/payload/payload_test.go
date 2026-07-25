package payload

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	assets "github.com/chrisdruta/vibe-tui-box"
	"github.com/chrisdruta/vibe-tui-box/internal/domain"
)

func testFS(t *testing.T, files map[string]string) fstest.MapFS {
	t.Helper()
	fsys := fstest.MapFS{}
	var entries []File
	for path, content := range files {
		fsys[path] = &fstest.MapFile{Data: []byte(content)}
		mode := "0644"
		if filepath.Ext(path) == ".sh" {
			mode = "0755"
		}
		entries = append(entries, File{
			Path:   path,
			Mode:   mode,
			Size:   int64(len(content)),
			Digest: domain.SHA256([]byte(content)),
		})
	}
	manifest, err := EncodeManifest(entries)
	if err != nil {
		t.Fatal(err)
	}
	fsys[ManifestPath] = &fstest.MapFile{Data: manifest}
	return fsys
}

func TestBundleVerifiesOnConstruction(t *testing.T) {
	files := map[string]string{
		"container/entrypoint.sh":   "#!/bin/sh\n",
		"presets/minimal/vibe.yaml": "schema: 1\n",
		"presets/python/vibe.yaml":  "schema: 1\n",
		"presets/python/Dockerfile": "FROM x\n",
	}
	b, err := New(testFS(t, files))
	if err != nil {
		t.Fatal(err)
	}
	if b.Digest().IsZero() {
		t.Fatal("bundle digest zero")
	}

	names := b.Names()
	if len(names) != 2 || names[0] != "minimal" || names[1] != "python" {
		t.Fatalf("preset names: %v", names)
	}
	preset, err := b.Preset("python")
	if err != nil || len(preset.Files) != 2 {
		t.Fatalf("preset: %+v, %v", preset, err)
	}
	if _, err := b.Preset("absent"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing preset should be not-found, got %v", err)
	}
}

func TestBundleRejectsTamper(t *testing.T) {
	// Content drift from the manifest.
	fsys := testFS(t, map[string]string{"container/entrypoint.sh": "#!/bin/sh\n"})
	fsys["container/entrypoint.sh"] = &fstest.MapFile{Data: []byte("#!/bin/sh\nevil\n")}
	if _, err := New(fsys); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("tampered file should conflict, got %v", err)
	}

	// Embedded file missing from the manifest.
	fsys = testFS(t, map[string]string{"container/entrypoint.sh": "#!/bin/sh\n"})
	fsys["container/extra.sh"] = &fstest.MapFile{Data: []byte("x")}
	if _, err := New(fsys); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("unmanifested file should conflict, got %v", err)
	}

	if _, err := New(fstest.MapFS{}); err == nil {
		t.Fatal("empty fs should fail")
	}
}

func TestParseManifestRejectsBadEntries(t *testing.T) {
	good := []File{{Path: "a.sh", Mode: "0755", Size: 1, Digest: domain.SHA256([]byte("x"))}}
	cases := map[string][]File{
		"escaping path":  {{Path: "../evil", Mode: "0644", Size: 1, Digest: good[0].Digest}},
		"manifest path":  {{Path: ManifestPath, Mode: "0644", Size: 1, Digest: good[0].Digest}},
		"bad mode":       {{Path: "a", Mode: "7777", Size: 1, Digest: good[0].Digest}},
		"zero digest":    {{Path: "a", Mode: "0644", Size: 1}},
		"duplicate path": {good[0], good[0]},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			// Encode with a correct digest over the (bad) entries so the
			// digest check is not what fails.
			m := Manifest{Format: 1, Files: files, Digest: ComputeDigest(files)}
			data, err := encodeRaw(m)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ParseManifest(data); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}

	// Digest mismatch is its own failure class.
	m := Manifest{Format: 1, Files: good, Digest: domain.SHA256([]byte("wrong"))}
	data, err := encodeRaw(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseManifest(data); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("digest mismatch should conflict, got %v", err)
	}
}

func TestExtractAppliesModes(t *testing.T) {
	b, err := New(testFS(t, map[string]string{
		"container/entrypoint.sh":   "#!/bin/sh\n",
		"presets/minimal/vibe.yaml": "schema: 1\n",
	}))
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "payload")
	digest, err := b.Extract(context.Background(), dest)
	if err != nil {
		t.Fatal(err)
	}
	if digest != b.Digest() {
		t.Fatal("extract digest mismatch")
	}
	info, err := os.Stat(filepath.Join(dest, "container/entrypoint.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("script mode %o, want 0755", info.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(dest, ManifestPath)); err != nil {
		t.Fatal("manifest not written to extraction")
	}
	// Extracting into a non-empty destination must not clobber.
	if _, err := b.Extract(context.Background(), dest); err == nil {
		t.Fatal("re-extract over existing payload should fail")
	}
}

func TestTrackedPayloadIsValid(t *testing.T) {
	// The tracked repository payload must always construct: this is the
	// CI drift gate for go generate ./internal/payload.
	b, err := New(os.DirFS(filepath.Join("..", "..", "payload")))
	if err != nil {
		t.Fatalf("tracked payload invalid (run go generate ./internal/payload): %v", err)
	}
	if len(b.Names()) == 0 {
		t.Fatal("payload has no presets")
	}
	if _, err := b.Preset("minimal"); err != nil {
		t.Fatalf("minimal preset: %v", err)
	}
}

func TestEmbeddedPayloadIsValid(t *testing.T) {
	// The EMBEDDED payload must also construct — os.DirFS above cannot
	// catch go:embed divergence: a plain //go:embed pattern silently
	// drops dot-prefixed entries (the claude plugin's .claude-plugin/
	// directory), which the manifest's filesystem walk keeps, and every
	// shipped binary then dies at its startup parity check while this
	// package's tests stay green. Exactly main.go's wiring.
	payloadFS, err := fs.Sub(assets.PayloadFS, "payload")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(payloadFS); err != nil {
		t.Fatalf("embedded payload invalid (check the all: prefix on go:embed in assets.go): %v", err)
	}
}

// encodeRaw marshals a manifest without EncodeManifest's normalization,
// for constructing invalid fixtures.
func encodeRaw(m Manifest) ([]byte, error) {
	return json.Marshal(m)
}

package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/lock"
	"github.com/chrisdruta/vibe-tui-box/internal/paths"
	"github.com/chrisdruta/vibe-tui-box/internal/payload"
	"github.com/chrisdruta/vibe-tui-box/internal/store"
)

type tarEntry struct {
	name     string
	content  string
	mode     int64
	typeflag byte
	linkname string
}

func buildArchive(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		typeflag := e.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		hdr := &tar.Header{
			Name:     e.name,
			Mode:     e.mode,
			Size:     int64(len(e.content)),
			Typeflag: typeflag,
			Linkname: e.linkname,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(e.content)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// goodEntries builds a valid release: binary + payload with manifest.
func goodEntries(t *testing.T) []tarEntry {
	t.Helper()
	script := "#!/bin/sh\nexec sleep infinity\n"
	files := []payload.File{{
		Path:   "container/entrypoint.sh",
		Mode:   "0755",
		Size:   int64(len(script)),
		Digest: domain.SHA256([]byte(script)),
	}}
	manifest, err := payload.EncodeManifest(files)
	if err != nil {
		t.Fatal(err)
	}
	return []tarEntry{
		{name: "vibe", content: "FAKE-ENGINE-BINARY", mode: 0o755},
		{name: "payload/container/entrypoint.sh", content: script, mode: 0o755},
		{name: "payload/manifest.json", content: string(manifest), mode: 0o644},
	}
}

type fixture struct {
	svc     *Service
	store   *store.Store
	mux     *http.ServeMux
	archive []byte
	sums    string
}

func newFixture(t *testing.T, entries []tarEntry) *fixture {
	t.Helper()
	layout, err := paths.NewLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	st, err := store.New(layout, lock.NewFlock(layout.Locks))
	if err != nil {
		t.Fatal(err)
	}

	f := &fixture{store: st, mux: http.NewServeMux()}
	f.archive = buildArchive(t, entries)
	sum := sha256.Sum256(f.archive)
	f.sums = fmt.Sprintf("%s  vibe_v2.0.0_linux_amd64.tar.gz\n", hex.EncodeToString(sum[:]))

	server := httptest.NewServer(f.mux)
	t.Cleanup(server.Close)
	f.mux.HandleFunc("/v2.0.0/vibe_v2.0.0_linux_amd64.tar.gz", func(w http.ResponseWriter, r *http.Request) {
		w.Write(f.archive)
	})
	f.mux.HandleFunc("/v2.0.0/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, f.sums)
	})

	platform := domain.Platform{OS: "linux", Arch: "amd64"}
	svc, err := NewService(NewHTTPSource(server.URL), ChecksumVerifier{}, st, platform, func() int64 { return 1784000000 })
	if err != nil {
		t.Fatal(err)
	}
	f.svc = svc
	return f
}

func TestAcquire(t *testing.T) {
	f := newFixture(t, goodEntries(t))
	artifact, err := f.svc.Acquire(context.Background(), "v2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	rec := artifact.Record
	if rec.Version != "v2.0.0" || rec.Digest.IsZero() || rec.BinaryDigest.IsZero() || rec.PayloadDigest.IsZero() {
		t.Fatalf("record incomplete: %+v", rec)
	}
	if rec.Release.Attestation != "checksums.txt" || rec.Release.ArchiveHash.IsZero() {
		t.Fatalf("provenance incomplete: %+v", rec.Release)
	}

	// The published object has the artifact layout with executable bits.
	bin, err := os.Stat(artifact.BinaryPath())
	if err != nil || bin.Mode().Perm()&0o100 == 0 {
		t.Fatalf("binary: %v %v", bin, err)
	}
	script, err := os.Stat(filepath.Join(artifact.PayloadPath(), "container/entrypoint.sh"))
	if err != nil || script.Mode().Perm() != 0o755 {
		t.Fatalf("entrypoint mode: %v %v", script, err)
	}

	// Record round-trips through the store.
	loaded, err := f.store.ReadArtifactRecord(rec.Digest)
	if err != nil || loaded.PayloadDigest != rec.PayloadDigest {
		t.Fatalf("record round-trip: %+v, %v", loaded, err)
	}

	// Re-acquire is idempotent.
	again, err := f.svc.Acquire(context.Background(), "v2.0.0")
	if err != nil || again.Record.Digest != rec.Digest {
		t.Fatalf("re-acquire: %+v, %v", again.Record, err)
	}
}

func TestAcquireRejectsBadChecksum(t *testing.T) {
	f := newFixture(t, goodEntries(t))
	f.sums = "0000000000000000000000000000000000000000000000000000000000000000  vibe_v2.0.0_linux_amd64.tar.gz\n"
	if _, err := f.svc.Acquire(context.Background(), "v2.0.0"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("bad checksum should conflict, got %v", err)
	}
	if exists, _ := f.store.Exists(context.Background(), store.ArtifactObject, domain.SHA256(f.archive)); exists {
		t.Fatal("nothing may be published on verification failure")
	}
}

func TestAcquireRejectsMissingChecksumEntry(t *testing.T) {
	f := newFixture(t, goodEntries(t))
	f.sums = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  other_file.tar.gz\n"
	if _, err := f.svc.Acquire(context.Background(), "v2.0.0"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing entry should be not-found, got %v", err)
	}
}

func TestAcquireRejectsHostileArchives(t *testing.T) {
	script := "x"
	cases := map[string][]tarEntry{
		"symlink": append(goodEntries(t), tarEntry{
			name: "payload/link", typeflag: tar.TypeSymlink, linkname: "/etc/passwd",
		}),
		"path escape": {{name: "../evil", content: script, mode: 0o644}},
		"unexpected location": append(goodEntries(t), tarEntry{
			name: "unrelated.txt", content: script, mode: 0o644,
		}),
		"absolute path": {{name: "/etc/evil", content: script, mode: 0o644}},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, entries)
			// Fix the checksum so extraction, not verification, is under test.
			sum := sha256.Sum256(f.archive)
			f.sums = fmt.Sprintf("%s  vibe_v2.0.0_linux_amd64.tar.gz\n", hex.EncodeToString(sum[:]))
			if _, err := f.svc.Acquire(context.Background(), "v2.0.0"); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("hostile archive should be invalid, got %v", err)
			}
		})
	}
}

func TestAcquireRejectsPayloadManifestDrift(t *testing.T) {
	entries := goodEntries(t)
	entries[1].content = "#!/bin/sh\nDIFFERENT\n" // drift from manifest
	f := newFixture(t, entries)
	if _, err := f.svc.Acquire(context.Background(), "v2.0.0"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("payload drift should conflict, got %v", err)
	}
}

func TestAcquireMissingRelease(t *testing.T) {
	f := newFixture(t, goodEntries(t))
	if _, err := f.svc.Acquire(context.Background(), "v9.9.9"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing release should be not-found, got %v", err)
	}
	if _, err := f.svc.Acquire(context.Background(), "2.0.0"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unprefixed version should be invalid, got %v", err)
	}
}

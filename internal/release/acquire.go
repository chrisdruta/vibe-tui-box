package release

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/payload"
	"github.com/chrisdruta/vibe-tui-box/internal/store"
)

// Archive layout: the engine binary at "vibe", the payload under
// "payload/" (with payload/manifest.json).
const (
	archiveBinaryName    = "vibe"
	archivePayloadPrefix = "payload/"
)

// Extraction limits.
const (
	maxArchiveEntries = 10000
	maxEntryBytes     = 512 << 20
	maxTotalBytes     = 1 << 30
)

func unixUTC(sec int64) time.Time { return time.Unix(sec, 0).UTC() }

// Service acquires and installs release artifacts.
type Service struct {
	source   Source
	verify   Verifier
	store    *store.Store
	platform domain.Platform
	now      func() int64 // unix seconds, injectable for tests
}

func NewService(source Source, verify Verifier, st *store.Store, platform domain.Platform, now func() int64) (*Service, error) {
	if source == nil || verify == nil || st == nil || now == nil {
		return nil, fmt.Errorf("%w: release service requires source, verifier, store, and clock", domain.ErrInvalid)
	}
	return &Service{source: source, verify: verify, store: st, platform: platform, now: now}, nil
}

// Acquire downloads, verifies, extracts, validates, and publishes one
// release version, returning the installed artifact.
func (s *Service) Acquire(ctx context.Context, ver string) (store.Artifact, error) {
	desc, err := s.source.Resolve(ctx, ver, s.platform)
	if err != nil {
		return store.Artifact{}, err
	}
	attestation, err := s.source.OpenAttestation(ctx, desc)
	if err != nil {
		return store.Artifact{}, err
	}
	defer attestation.Close()
	archiveHash, finish, err := s.verify.Begin(ctx, desc, attestation)
	if err != nil {
		return store.Artifact{}, err
	}

	archive, err := s.source.OpenArtifact(ctx, desc)
	if err != nil {
		return store.Artifact{}, err
	}
	defer archive.Close()

	staging, err := s.store.NewStaging("release")
	if err != nil {
		return store.Artifact{}, err
	}
	defer os.RemoveAll(staging)

	// Hash exactly the bytes that are extracted.
	if err := extractArchive(ctx, io.TeeReader(archive, archiveHash), staging); err != nil {
		return store.Artifact{}, err
	}
	verified, err := finish()
	if err != nil {
		return store.Artifact{}, err
	}

	binaryDigest, payloadDigest, err := validateExtracted(staging)
	if err != nil {
		return store.Artifact{}, err
	}

	digest, _, err := store.DigestTree(staging)
	if err != nil {
		return store.Artifact{}, err
	}
	obj, err := s.store.Publish(ctx, store.ArtifactObject, staging, digest)
	if err != nil {
		return store.Artifact{}, err
	}
	record := store.ArtifactRecord{
		Digest:        digest,
		Version:       desc.Version,
		Platform:      desc.Platform,
		BinaryDigest:  binaryDigest,
		PayloadDigest: payloadDigest,
		Release: domain.ReleaseProvenance{
			Source:      desc.ArchiveURL,
			Version:     desc.Version,
			Attestation: verified.Method,
			ArchiveHash: verified.ArchiveDigest,
		},
		InstalledAt: unixUTC(s.now()),
	}
	if err := s.store.WriteArtifactRecord(record); err != nil {
		return store.Artifact{}, err
	}
	return store.Artifact{Record: record, Path: obj.Path}, nil
}

// extractArchive unpacks the gzipped tar into staging, accepting only
// regular files and directories at known locations.
func extractArchive(ctx context.Context, r io.Reader, staging string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("%w: release archive is not gzip: %v", domain.ErrInvalid, err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	entries := 0
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%w: extract release: %v", domain.ErrCanceled, context.Cause(ctx))
		}
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("%w: release archive: %v", domain.ErrInvalid, err)
		}
		if entries++; entries > maxArchiveEntries {
			return fmt.Errorf("%w: release archive exceeds %d entries", domain.ErrInvalid, maxArchiveEntries)
		}
		name := path.Clean(hdr.Name)
		if name == "." {
			continue
		}
		dest, mode, err := mapEntry(name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(filepath.Join(staging, dest), 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if hdr.Size > maxEntryBytes {
				return fmt.Errorf("%w: archive entry %s exceeds %d bytes", domain.ErrInvalid, name, int64(maxEntryBytes))
			}
			if total += hdr.Size; total > maxTotalBytes {
				return fmt.Errorf("%w: release archive exceeds %d bytes", domain.ErrInvalid, int64(maxTotalBytes))
			}
			target := filepath.Join(staging, dest)
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			perm := fs.FileMode(0o644)
			if mode == 0o755 || hdr.FileInfo().Mode()&0o100 != 0 {
				perm = 0o755
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
			if err != nil {
				return fmt.Errorf("extract %s: %w", name, err)
			}
			n, err := io.Copy(f, io.LimitReader(tr, hdr.Size+1))
			if closeErr := f.Close(); err == nil {
				err = closeErr
			}
			if err != nil {
				return fmt.Errorf("extract %s: %w", name, err)
			}
			if n != hdr.Size {
				return fmt.Errorf("%w: archive entry %s size mismatch", domain.ErrInvalid, name)
			}
		default:
			return fmt.Errorf("%w: archive entry %s has unsupported type %q (regular files and directories only)",
				domain.ErrInvalid, name, hdr.Typeflag)
		}
	}
	// Drain any trailing gzip padding so the tee'd hash covers the whole
	// download.
	if _, err := io.Copy(io.Discard, r); err != nil {
		return fmt.Errorf("read release archive: %w", err)
	}
	return nil
}

// mapEntry maps an archive path to its staging location; anything
// outside the known layout is rejected.
func mapEntry(name string) (dest string, mode fs.FileMode, err error) {
	if !fs.ValidPath(name) {
		return "", 0, fmt.Errorf("%w: archive entry %q", domain.ErrInvalid, name)
	}
	switch {
	case name == archiveBinaryName:
		return filepath.FromSlash(store.ArtifactBinaryRelPath), 0o755, nil
	case name == "payload" || strings.HasPrefix(name, archivePayloadPrefix):
		return filepath.FromSlash(name), 0o644, nil
	default:
		return "", 0, fmt.Errorf("%w: unexpected archive entry %q", domain.ErrInvalid, name)
	}
}

// validateExtracted checks the staged artifact: binary present, payload
// manifest parses, and every payload file matches its manifest entry.
func validateExtracted(staging string) (binaryDigest, payloadDigest domain.Digest, err error) {
	binaryDigest, _, err = digestFileSized(filepath.Join(staging, store.ArtifactBinaryRelPath))
	if err != nil {
		return domain.Digest{}, domain.Digest{}, fmt.Errorf("%w: release archive has no engine binary", domain.ErrInvalid)
	}

	payloadRoot := filepath.Join(staging, store.ArtifactPayloadRelPath)
	manifestData, err := os.ReadFile(filepath.Join(payloadRoot, payload.ManifestPath))
	if err != nil {
		return domain.Digest{}, domain.Digest{}, fmt.Errorf("%w: release archive has no payload manifest", domain.ErrInvalid)
	}
	manifest, err := payload.ParseManifest(manifestData)
	if err != nil {
		return domain.Digest{}, domain.Digest{}, err
	}
	for _, f := range manifest.Files {
		target := filepath.Join(payloadRoot, filepath.FromSlash(f.Path))
		digest, size, err := digestFileSized(target)
		if err != nil {
			return domain.Digest{}, domain.Digest{}, fmt.Errorf("%w: payload file %s missing from archive", domain.ErrConflict, f.Path)
		}
		if size != f.Size || digest != f.Digest {
			return domain.Digest{}, domain.Digest{}, fmt.Errorf("%w: payload file %s does not match the payload manifest", domain.ErrConflict, f.Path)
		}
		// The manifest's modes are authoritative; tar modes only chose
		// the temporary permissions during extraction.
		perm, err := f.Perm()
		if err != nil {
			return domain.Digest{}, domain.Digest{}, err
		}
		if err := os.Chmod(target, perm); err != nil {
			return domain.Digest{}, domain.Digest{}, err
		}
	}
	return binaryDigest, manifest.Digest, nil
}

// digestFileSized streams a file through the hasher and reports its
// size, so multi-MB artifact members never load into memory whole.
func digestFileSized(path string) (domain.Digest, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return domain.Digest{}, 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return domain.Digest{}, 0, err
	}
	digest, err := domain.SHA256Reader(f)
	if err != nil {
		return domain.Digest{}, 0, err
	}
	return digest, info.Size(), nil
}

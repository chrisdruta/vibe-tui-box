package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
)

// ArtifactRecord is the persistent metadata for one installed release
// artifact, stored beside the artifact object as
// <artifacts>/<hex>.record.json.
type ArtifactRecord struct {
	Format        int                      `json:"format"`
	Digest        domain.Digest            `json:"digest"`
	Version       string                   `json:"version"`
	Platform      domain.Platform          `json:"platform"`
	BinaryDigest  domain.Digest            `json:"binary_digest"`
	PayloadDigest domain.Digest            `json:"payload_digest"`
	Release       domain.ReleaseProvenance `json:"release"`
	InstalledAt   time.Time                `json:"installed_at"`
}

// Artifact pairs an installed artifact's record with its published
// object path. The zero value means "no artifact selected".
type Artifact struct {
	Record ArtifactRecord
	Path   string
}

func (a Artifact) IsZero() bool { return a.Path == "" && a.Record.Digest.IsZero() }

// Artifact object layout: the engine binary plus the extracted payload.
const (
	ArtifactBinaryRelPath  = "bin/vibe"
	ArtifactPayloadRelPath = "payload"
)

// PayloadPath is the extracted payload directory inside the artifact.
func (a Artifact) PayloadPath() string {
	return filepath.Join(a.Path, ArtifactPayloadRelPath)
}

// BinaryPath is the engine binary inside the artifact.
func (a Artifact) BinaryPath() string {
	return filepath.Join(a.Path, ArtifactBinaryRelPath)
}

const artifactRecordFormat = 1

func (s *Store) artifactRecordPath(digest domain.Digest) string {
	return filepath.Join(s.layout.Artifacts, digest.Hex()+".record.json")
}

// WriteArtifactRecord persists the record for a published artifact.
// Identical rewrites are success; divergent content is a conflict.
func (s *Store) WriteArtifactRecord(rec ArtifactRecord) error {
	if rec.Digest.IsZero() {
		return fmt.Errorf("%w: artifact record needs a digest", domain.ErrInvalid)
	}
	rec.Format = artifactRecordFormat
	data, err := marshalRecord(rec)
	if err != nil {
		return err
	}
	path := s.artifactRecordPath(rec.Digest)
	if existing, err := os.ReadFile(path); err == nil {
		if bytes.Equal(existing, data) {
			return nil
		}
		return fmt.Errorf("%w: artifact record %s exists with different content", domain.ErrConflict, rec.Digest)
	}
	return WriteFileAtomic(path, data, 0o600)
}

// ListArtifactRecords returns every installed artifact record, newest
// first.
func (s *Store) ListArtifactRecords() ([]ArtifactRecord, error) {
	entries, err := os.ReadDir(s.layout.Artifacts)
	if err != nil {
		return nil, fmt.Errorf("read artifacts: %w", err)
	}
	var records []ArtifactRecord
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".record.json")
		if !ok || e.IsDir() {
			continue
		}
		digest, err := domain.ParseDigest("sha256:" + name)
		if err != nil {
			continue
		}
		rec, err := s.ReadArtifactRecord(digest)
		if err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	sort.Slice(records, func(i, j int) bool {
		if !records[i].InstalledAt.Equal(records[j].InstalledAt) {
			return records[i].InstalledAt.After(records[j].InstalledAt)
		}
		return records[i].Digest.Hex() < records[j].Digest.Hex()
	})
	return records, nil
}

// ReadArtifactRecord loads and format-checks one artifact record.
func (s *Store) ReadArtifactRecord(digest domain.Digest) (ArtifactRecord, error) {
	data, err := os.ReadFile(s.artifactRecordPath(digest))
	if err != nil {
		if os.IsNotExist(err) {
			return ArtifactRecord{}, fmt.Errorf("%w: artifact %s", domain.ErrNotFound, digest)
		}
		return ArtifactRecord{}, err
	}
	var probe struct {
		Format int `json:"format"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return ArtifactRecord{}, fmt.Errorf("%w: artifact record %s: %v", domain.ErrInvalid, digest, err)
	}
	if probe.Format != artifactRecordFormat {
		return ArtifactRecord{}, fmt.Errorf("%w: artifact record format %d (supported: %d)", domain.ErrNotSupported, probe.Format, artifactRecordFormat)
	}
	var rec ArtifactRecord
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rec); err != nil {
		return ArtifactRecord{}, fmt.Errorf("%w: artifact record %s: %v", domain.ErrInvalid, digest, err)
	}
	return rec, nil
}

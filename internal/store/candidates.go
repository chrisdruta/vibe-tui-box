package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
)

// CandidateKind separates ordinary runtime candidates from extension
// build candidates, which require explicit approval.
type CandidateKind string

const (
	RuntimeCandidate CandidateKind = "runtime"
	BuildCandidate   CandidateKind = "build"
)

// CandidateRecord is the persistent metadata for one immutable
// candidate. It is stored beside the candidate object as
// <candidates>/<hex>.record.json; the candidate object itself holds the
// canonical plan and frozen inputs.
type CandidateRecord struct {
	Format        int               `json:"format"`
	Digest        domain.Digest     `json:"digest"`
	ProjectID     domain.ProjectID  `json:"project_id"`
	Kind          CandidateKind     `json:"kind"`
	Snapshot      domain.Digest     `json:"snapshot"`
	Plan          domain.Digest     `json:"plan"`
	Extension     *domain.Digest    `json:"extension,omitempty"`
	Images        []ResolvedImage   `json:"images,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
	SourceRequest *domain.RequestID `json:"source_request,omitempty"`
}

// ResolvedImage records one image reference resolved to a registry
// digest at candidate-creation time.
type ResolvedImage struct {
	Ref    string        `json:"ref"`
	Digest domain.Digest `json:"digest"`
}

const candidateRecordFormat = 1

func (s *Store) candidateRecordPath(digest domain.Digest) string {
	return filepath.Join(s.layout.Candidates, digest.Hex()+".record.json")
}

// WriteCandidateRecord persists the record for an already-published
// candidate. Writing an identical record again is success.
func (s *Store) WriteCandidateRecord(rec CandidateRecord) error {
	if rec.Digest.IsZero() || rec.ProjectID == "" {
		return fmt.Errorf("%w: candidate record needs digest and project", domain.ErrInvalid)
	}
	rec.Format = candidateRecordFormat
	data, err := marshalRecord(rec)
	if err != nil {
		return err
	}
	path := s.candidateRecordPath(rec.Digest)
	if existing, err := os.ReadFile(path); err == nil {
		if bytes.Equal(existing, data) {
			return nil
		}
		return fmt.Errorf("%w: candidate record %s exists with different content", domain.ErrConflict, rec.Digest)
	}
	return WriteFileAtomic(path, data, 0o600)
}

// ReadCandidateRecord loads and format-checks one candidate record.
func (s *Store) ReadCandidateRecord(digest domain.Digest) (CandidateRecord, error) {
	data, err := os.ReadFile(s.candidateRecordPath(digest))
	if err != nil {
		if os.IsNotExist(err) {
			return CandidateRecord{}, fmt.Errorf("%w: candidate %s", domain.ErrNotFound, digest)
		}
		return CandidateRecord{}, err
	}
	var probe struct {
		Format int `json:"format"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return CandidateRecord{}, fmt.Errorf("%w: candidate record %s: %v", domain.ErrInvalid, digest, err)
	}
	if probe.Format != candidateRecordFormat {
		return CandidateRecord{}, fmt.Errorf("%w: candidate record format %d (supported: %d)", domain.ErrNotSupported, probe.Format, candidateRecordFormat)
	}
	var rec CandidateRecord
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rec); err != nil {
		return CandidateRecord{}, fmt.Errorf("%w: candidate record %s: %v", domain.ErrInvalid, digest, err)
	}
	return rec, nil
}

// marshalRecord produces deterministic JSON: fixed field order from the
// struct, two-space indentation, one trailing newline.
func marshalRecord(v any) ([]byte, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

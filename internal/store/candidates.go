package store

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"vibe/internal/domain"
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
	Format    int              `json:"format"`
	Digest    domain.Digest    `json:"digest"`
	ProjectID domain.ProjectID `json:"project_id"`
	Kind      CandidateKind    `json:"kind"`
	Snapshot  domain.Digest    `json:"snapshot"`
	Plan      domain.Digest    `json:"plan"`
	Extension *domain.Digest   `json:"extension,omitempty"`
	Images    []ResolvedImage  `json:"images,omitempty"`
	CreatedAt time.Time        `json:"created_at"`
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
	return writeRecordOnce(s.candidateRecordPath(rec.Digest), "candidate record "+rec.Digest.String(), rec)
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
	return DecodeRecord[CandidateRecord](data, "candidate record "+digest.String(), candidateRecordFormat)
}

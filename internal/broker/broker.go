// Package broker handles agent rebuild requests. Agents write request
// JSON into the workspace at .vibe/requests/; the engine polls with
// strict bounds, binds each new request to an immutable candidate, and
// records operator decisions as results the container reads through its
// read-only results mount.
package broker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"vibe/internal/domain"
	"vibe/internal/paths"
	"vibe/internal/store"
)

// RequestsRelDir is where agents drop request files inside the
// workspace.
const RequestsRelDir = ".vibe/requests"

// Polling and content limits.
const (
	maxRequests     = 100
	maxRequestBytes = 64 << 10
	maxTextRunes    = 4096
	recordFormat    = 1
)

type Kind string

const RebuildKind Kind = "rebuild"

// Request is the agent-authored file content. Reason and Summary are
// untrusted text; render them only through the terminal encoder.
type Request struct {
	Format  int              `json:"format"`
	ID      domain.RequestID `json:"id"`
	Kind    Kind             `json:"kind"`
	Reason  string           `json:"reason"`
	Summary string           `json:"summary"`
}

// ParseRequest strictly decodes one request file.
func ParseRequest(data []byte) (Request, error) {
	if len(data) > maxRequestBytes {
		return Request{}, fmt.Errorf("%w: request exceeds %d bytes", domain.ErrInvalid, maxRequestBytes)
	}
	var req Request
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return Request{}, fmt.Errorf("%w: request: %v", domain.ErrInvalid, err)
	}
	if req.Format != recordFormat {
		return Request{}, fmt.Errorf("%w: request format %d (supported: %d)", domain.ErrNotSupported, req.Format, recordFormat)
	}
	if _, err := domain.ParseRequestID(string(req.ID)); err != nil {
		return Request{}, err
	}
	if req.Kind != RebuildKind {
		return Request{}, fmt.Errorf("%w: request kind %q", domain.ErrNotSupported, req.Kind)
	}
	if len([]rune(req.Reason)) > maxTextRunes || len([]rune(req.Summary)) > maxTextRunes {
		return Request{}, fmt.Errorf("%w: request text exceeds %d characters", domain.ErrInvalid, maxTextRunes)
	}
	return req, nil
}

// Raw is one request file read from the workspace.
type Raw struct {
	Request Request
	Digest  domain.Digest // content digest for replacement detection
}

// ReadRequests reads request files from the workspace with bounded
// count and size, confined beneath the project root. Malformed files
// return errors keyed by filename rather than aborting the poll.
func ReadRequests(root paths.Root) ([]Raw, []error) {
	osRoot, err := os.OpenRoot(root.Path)
	if err != nil {
		return nil, []error{err}
	}
	defer osRoot.Close()

	entries, err := fs.ReadDir(osRoot.FS(), filepath.ToSlash(RequestsRelDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []error{err}
	}
	if len(entries) > maxRequests {
		return nil, []error{fmt.Errorf("%w: more than %d request files", domain.ErrInvalid, maxRequests)}
	}

	var raws []Raw
	var problems []error
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		f, err := osRoot.Open(RequestsRelDir + "/" + e.Name())
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", e.Name(), err))
			continue
		}
		data, err := io.ReadAll(io.LimitReader(f, maxRequestBytes+1))
		f.Close()
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", e.Name(), err))
			continue
		}
		req, perr := ParseRequest(data)
		if perr != nil {
			problems = append(problems, fmt.Errorf("%s: %w", e.Name(), perr))
			continue
		}
		raws = append(raws, Raw{Request: req, Digest: domain.SHA256(data)})
	}
	sort.Slice(raws, func(i, j int) bool { return raws[i].Request.ID < raws[j].Request.ID })
	return raws, problems
}

type Status string

const (
	StatusPending  Status = "pending"
	StatusApproved Status = "approved"
	StatusRejected Status = "rejected"
)

// Pending binds a polled request to its immutable candidate.
type Pending struct {
	Format    int              `json:"format"`
	RequestID domain.RequestID `json:"request_id"`
	ProjectID domain.ProjectID `json:"project_id"`
	Candidate domain.Digest    `json:"candidate"`
	Content   domain.Digest    `json:"content"`
	Reason    string           `json:"reason"`
	Summary   string           `json:"summary"`
	CreatedAt time.Time        `json:"created_at"`
}

// Result records the decision; it is what the container can read.
type Result struct {
	Format      int              `json:"format"`
	RequestID   domain.RequestID `json:"request_id"`
	ProjectID   domain.ProjectID `json:"project_id"`
	Candidate   *domain.Digest   `json:"candidate,omitempty"`
	Status      Status           `json:"status"`
	Message     string           `json:"message,omitempty"`
	CreatedAt   time.Time        `json:"created_at"`
	CompletedAt *time.Time       `json:"completed_at,omitempty"`
}

// Store keeps pending and result records under the host broker
// directory for one project. Results live in ResultsDir, which the
// engine mounts read-only into the container.
type Store struct {
	dir string
}

func NewStore(brokerRoot string, project domain.ProjectID) (*Store, error) {
	if !filepath.IsAbs(brokerRoot) {
		return nil, fmt.Errorf("%w: broker root %q", domain.ErrInvalid, brokerRoot)
	}
	s := &Store{dir: filepath.Join(brokerRoot, string(project))}
	for _, d := range []string{s.pendingDir(), s.ResultsDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) pendingDir() string { return filepath.Join(s.dir, "pending") }

// ResultsDir is world-readable host state mounted ro into containers.
func (s *Store) ResultsDir() string { return filepath.Join(s.dir, "results") }

func (s *Store) WritePending(p Pending) error {
	p.Format = recordFormat
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return store.WriteFileAtomic(filepath.Join(s.pendingDir(), string(p.RequestID)+".json"), append(data, '\n'), 0o600)
}

func (s *Store) ListPending() ([]Pending, error) {
	return readAll[Pending](s.pendingDir())
}

func (s *Store) DeletePending(id domain.RequestID) error {
	err := os.Remove(filepath.Join(s.pendingDir(), string(id)+".json"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s *Store) WriteResult(r Result) error {
	r.Format = recordFormat
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return store.WriteFileAtomic(filepath.Join(s.ResultsDir(), string(r.RequestID)+".json"), append(data, '\n'), 0o644)
}

func (s *Store) ListResults() ([]Result, error) {
	return readAll[Result](s.ResultsDir())
}

func readAll[T any](dir string) ([]T, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []T
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var v T
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&v); err != nil {
			return nil, fmt.Errorf("%w: broker record %s: %v", domain.ErrInvalid, e.Name(), err)
		}
		out = append(out, v)
	}
	return out, nil
}

package runtime

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/store"
)

// The replacement journal is the durable phase marker for one project's
// container-replacement transaction: present means uncommitted (the
// .prev generation is authoritative and recovery must roll back),
// absent means any .prev/.next leftovers are committed debris. Every
// entry is written before the Docker mutation it describes, so no
// staged or created container can exist unrecorded.

const journalVersion = 1

var nonceRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

type journalEntry struct {
	// Kind is "replace" (old retired to <name>.prev, new swapped in),
	// "create" (fresh container, prior state absent), or "start"
	// (existing same-candidate container started, prior state stopped).
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	OldID      string `json:"old_id,omitempty"`
	WasRunning bool   `json:"was_running,omitempty"`
	NewID      string `json:"new_id,omitempty"`
	ID         string `json:"id,omitempty"`
}

type journal struct {
	Version   int            `json:"version"`
	Candidate string         `json:"candidate"`
	Nonce     string         `json:"nonce"`
	Entries   []journalEntry `json:"entries"`
}

// owns reports whether a container is provably this transaction's own
// creation: the full label conjunction, never name alone — candidate
// labels identify content and other transactions' containers can
// legitimately share them.
func (j *journal) owns(labels map[string]string, project domain.ProjectID) bool {
	return labels[TxnLabel] == j.Nonce &&
		labels[ManagedLabel] == "true" &&
		labels[ProjectLabel] == string(project) &&
		labels[CandidateLabel] == j.Candidate
}

func mintNonce() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("mint transaction nonce: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func (s *Service) journalPath(project domain.ProjectID) string {
	return filepath.Join(s.replaceDir, string(project)+".json")
}

// loadJournal reads and strictly validates a journal. A missing file is
// (nil, nil); anything unreadable, unparsable, or out of contract is an
// error — recovery never guesses through a journal it cannot fully
// trust, and the error names the manual remedy.
func loadJournal(path string) (*journal, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, journalCorrupt(path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var j journal
	if err := dec.Decode(&j); err != nil {
		return nil, journalCorrupt(path, err)
	}
	// A strict EOF check: More() alone misses trailing delimiters like
	// a stray `]`, which only a further Decode attempt reveals.
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return nil, journalCorrupt(path, fmt.Errorf("trailing data after journal document"))
	}
	if j.Version != journalVersion {
		return nil, journalCorrupt(path, fmt.Errorf("unsupported version %d", j.Version))
	}
	if !nonceRe.MatchString(j.Nonce) {
		return nil, journalCorrupt(path, fmt.Errorf("malformed nonce"))
	}
	if _, err := domain.ParseDigest(j.Candidate); err != nil {
		return nil, journalCorrupt(path, fmt.Errorf("malformed candidate digest"))
	}
	for _, e := range j.Entries {
		if e.Name == "" {
			return nil, journalCorrupt(path, fmt.Errorf("entry without a name"))
		}
		// Kind-specific invariants: the fields each undo path keys on
		// are journaled before the entry's action ever runs, so their
		// absence is corruption, not a crash shape.
		switch e.Kind {
		case "replace":
			if e.OldID == "" {
				return nil, journalCorrupt(path, fmt.Errorf("replace entry %s without old_id", e.Name))
			}
		case "create":
		case "start":
			if e.ID == "" {
				return nil, journalCorrupt(path, fmt.Errorf("start entry %s without id", e.Name))
			}
		default:
			return nil, journalCorrupt(path, fmt.Errorf("unknown entry kind %q", e.Kind))
		}
	}
	return &j, nil
}

func journalCorrupt(path string, err error) error {
	return fmt.Errorf("%w: replacement journal %s is unreadable (%v); inspect `docker ps -a` for .prev/.next containers, resolve by hand, then remove the file",
		domain.ErrConflict, path, err)
}

// writeJournal persists the journal durably (atomic write, file and
// parent-directory fsync) BEFORE the mutation each new entry describes.
func (s *Service) writeJournal(project domain.ProjectID, j *journal) error {
	if err := os.MkdirAll(s.replaceDir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	return store.WriteFileAtomic(s.journalPath(project), data, 0o600)
}

// deleteJournal is commit: unlink plus parent-directory fsync, so a
// crash cannot resurrect a committed transaction as uncommitted.
func (s *Service) deleteJournal(project domain.ProjectID) error {
	if err := os.Remove(s.journalPath(project)); err != nil && !os.IsNotExist(err) {
		return err
	}
	dir, err := os.Open(s.replaceDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// sweepTempFiles removes this project's orphaned atomic-write temp
// files (crash debris). Only the current project's prefix is touched:
// another project's in-flight rewrite must never be disrupted.
func (s *Service) sweepTempFiles(project domain.ProjectID) {
	prefix := "." + string(project) + ".json.tmp-"
	entries, err := os.ReadDir(s.replaceDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if len(e.Name()) > len(prefix) && e.Name()[:len(prefix)] == prefix {
			_ = os.Remove(filepath.Join(s.replaceDir, e.Name()))
		}
	}
}

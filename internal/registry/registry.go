package registry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"vibe/internal/domain"
	"vibe/internal/lock"
	"vibe/internal/paths"
	"vibe/internal/store"
)

// Clock supplies record timestamps; tests inject a fixed one.
type Clock interface {
	Now() time.Time
}

type Registry struct {
	dir   string
	locks lock.Locker
	clock Clock
}

func New(dir string, locks lock.Locker, clock Clock) (*Registry, error) {
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("%w: registry dir %q is not absolute", domain.ErrInvalid, dir)
	}
	if locks == nil || clock == nil {
		return nil, fmt.Errorf("%w: registry requires a locker and clock", domain.ErrInvalid)
	}
	return &Registry{dir: dir, locks: locks, clock: clock}, nil
}

func (r *Registry) recordPath(id domain.ProjectID) string {
	return filepath.Join(r.dir, string(id)+".json")
}

// Create registers a project. Registering a root whose identity is
// already registered returns ErrConflict.
func (r *Registry) Create(ctx context.Context, n NewRecord) (Record, error) {
	if err := n.validate(); err != nil {
		return Record{}, err
	}
	id, err := domain.NewProjectID()
	if err != nil {
		return Record{}, err
	}
	held, err := r.locks.Acquire(ctx, lock.Project("registry"))
	if err != nil {
		return Record{}, err
	}
	defer held.Release()

	existing, err := r.list()
	if err != nil {
		return Record{}, err
	}
	for _, rec := range existing {
		if rec.RootIdentity == n.Root.Identity || rec.Root == n.Root.Path {
			return Record{}, fmt.Errorf("%w: %s is already registered as %q (%s)",
				domain.ErrConflict, n.Root.Path, rec.DisplayName, rec.ID)
		}
	}

	now := r.clock.Now().UTC()
	rec := Record{
		Format:         RecordFormat,
		Revision:       1,
		ID:             id,
		Root:           n.Root.Path,
		RootIdentity:   n.Root.Identity,
		DisplayName:    n.DisplayName,
		Artifact:       n.Artifact,
		ReleaseVersion: n.ReleaseVersion,
		Mode:           n.Mode,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := r.write(rec); err != nil {
		return Record{}, err
	}
	return rec, nil
}

func (r *Registry) Get(ctx context.Context, id domain.ProjectID) (Record, error) {
	return r.read(r.recordPath(id))
}

// Resolve maps a discovered project root to its registration by file
// identity first, path second. Two matching records is a conflict, not
// a guess.
func (r *Registry) Resolve(ctx context.Context, root paths.Root) (Record, error) {
	records, err := r.list()
	if err != nil {
		return Record{}, err
	}
	var matches []Record
	for _, rec := range records {
		if rec.RootIdentity == root.Identity || rec.Root == root.Path {
			matches = append(matches, rec)
		}
	}
	switch len(matches) {
	case 0:
		return Record{}, fmt.Errorf("%w: project %s is not registered (run `vibe register`)", domain.ErrNotFound, root.Path)
	case 1:
		return matches[0], nil
	default:
		names := make([]string, len(matches))
		for i, m := range matches {
			names[i] = fmt.Sprintf("%s (%s)", m.DisplayName, m.ID)
		}
		return Record{}, fmt.Errorf("%w: %s matches multiple registrations: %s",
			domain.ErrConflict, root.Path, strings.Join(names, ", "))
	}
}

// Update applies fn to the current record under the project lock. The
// caller's revision must match the stored revision or the update fails
// with ErrConflict.
func (r *Registry) Update(ctx context.Context, id domain.ProjectID, revision uint64, fn func(*Record) error) (Record, error) {
	held, err := r.locks.Acquire(ctx, lock.Project(string(id)))
	if err != nil {
		return Record{}, err
	}
	defer held.Release()

	rec, err := r.read(r.recordPath(id))
	if err != nil {
		return Record{}, err
	}
	if rec.Revision != revision {
		return Record{}, fmt.Errorf("%w: project %s is at revision %d, not %d", domain.ErrConflict, id, rec.Revision, revision)
	}
	if err := fn(&rec); err != nil {
		return Record{}, err
	}
	rec.Format = RecordFormat
	rec.ID = id
	rec.Revision = revision + 1
	rec.UpdatedAt = r.clock.Now().UTC()
	if err := r.write(rec); err != nil {
		return Record{}, err
	}
	return rec, nil
}

// Delete removes a registration at the given revision.
func (r *Registry) Delete(ctx context.Context, id domain.ProjectID, revision uint64) error {
	held, err := r.locks.Acquire(ctx, lock.Project(string(id)))
	if err != nil {
		return err
	}
	defer held.Release()

	rec, err := r.read(r.recordPath(id))
	if err != nil {
		return err
	}
	if rec.Revision != revision {
		return fmt.Errorf("%w: project %s is at revision %d, not %d", domain.ErrConflict, id, rec.Revision, revision)
	}
	return os.Remove(r.recordPath(id))
}

// List returns every registration sorted by display name then ID, so
// fleet output is deterministic.
func (r *Registry) List(ctx context.Context) ([]Record, error) {
	return r.list()
}

func (r *Registry) list() ([]Record, error) {
	dirEntries, err := os.ReadDir(r.dir)
	if err != nil {
		return nil, fmt.Errorf("read registry: %w", err)
	}
	var records []Record
	for _, e := range dirEntries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		rec, err := r.read(filepath.Join(r.dir, e.Name()))
		if err != nil {
			// Resolve and List run lockless by design; a record deleted
			// between ReadDir and the read (a racing `forget`) must not
			// abort the whole listing.
			if errors.Is(err, domain.ErrNotFound) {
				continue
			}
			return nil, err
		}
		records = append(records, rec)
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].DisplayName != records[j].DisplayName {
			return records[i].DisplayName < records[j].DisplayName
		}
		return records[i].ID < records[j].ID
	})
	return records, nil
}

func (r *Registry) read(path string) (Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Record{}, fmt.Errorf("%w: project record %s", domain.ErrNotFound, filepath.Base(path))
		}
		return Record{}, err
	}
	return store.DecodeRecord[Record](data, "project record "+filepath.Base(path), RecordFormat)
}

func (r *Registry) write(rec Record) error {
	data, err := store.MarshalRecord(rec)
	if err != nil {
		return err
	}
	return store.WriteFileAtomic(r.recordPath(rec.ID), data, 0o600)
}

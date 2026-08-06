package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"vibe/internal/dockerapi"
	"vibe/internal/domain"
)

// Name suffixes for the two transient roles a container can hold
// during a replacement transaction. The container name grammar is
// [a-z0-9-], so the dotted suffixes are unambiguous.
const (
	retiredSuffix = ".prev" // old generation, parked until commit
	stagedSuffix  = ".next" // new generation, created but never started
)

// txn tracks one Up's container mutations. Every entry is durably
// journaled before the Docker call it describes executes; a failed
// journal write aborts forward progress immediately (only undo may
// touch Docker afterward). IDs minted by a call are backfilled by a
// journal rewrite right after it returns.
type txn struct {
	svc     *Service
	project domain.ProjectID
	j       *journal
	written bool
}

func newTxn(svc *Service, project domain.ProjectID, candidate domain.Digest) (*txn, error) {
	nonce, err := mintNonce()
	if err != nil {
		return nil, err
	}
	return &txn{
		svc:     svc,
		project: project,
		j:       &journal{Version: journalVersion, Candidate: candidate.String(), Nonce: nonce},
	}, nil
}

// append durably records an entry before its action runs and returns
// its index for backfill.
func (t *txn) append(e journalEntry) (int, error) {
	t.j.Entries = append(t.j.Entries, e)
	if err := t.svc.writeJournal(t.project, t.j); err != nil {
		t.j.Entries = t.j.Entries[:len(t.j.Entries)-1]
		return 0, fmt.Errorf("write replacement journal: %w", err)
	}
	t.written = true
	return len(t.j.Entries) - 1, nil
}

// backfill records an ID minted by the action the entry describes.
func (t *txn) backfill(i int, mutate func(*journalEntry)) error {
	mutate(&t.j.Entries[i])
	if err := t.svc.writeJournal(t.project, t.j); err != nil {
		return fmt.Errorf("write replacement journal: %w", err)
	}
	return nil
}

// commit durably deletes the journal — the transaction's single phase
// marker — after which any .prev/.next leftovers are committed debris.
func (t *txn) commit() error {
	if !t.written {
		return nil
	}
	if err := t.svc.deleteJournal(t.project); err != nil {
		return fmt.Errorf("commit replacement journal: %w", err)
	}
	t.written = false
	return nil
}

// rollback undoes the transaction after cause, returning cause joined
// with any undo failures. On a fully successful undo the journal is
// deleted; otherwise it is retained and the next Up's sweep retries.
func (t *txn) rollback(ctx context.Context, cause error) error {
	if !t.written {
		return cause
	}
	if err := t.svc.undo(ctx, t.project, t.j); err != nil {
		return errors.Join(cause, err)
	}
	if err := t.svc.deleteJournal(t.project); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

// sweep runs at the start of every Up, before any reconciliation: a
// present journal is an uncommitted transaction to roll back; with no
// journal, .prev/.next leftovers are committed debris to clear, along
// with this project's orphaned journal temp files.
func (s *Service) sweep(ctx context.Context, project domain.ProjectID) error {
	j, err := loadJournal(s.journalPath(project))
	if err != nil {
		return err
	}
	if j != nil {
		if err := s.undo(ctx, project, j); err != nil {
			return fmt.Errorf("recover interrupted replacement: %w", err)
		}
		if err := s.deleteJournal(project); err != nil {
			return err
		}
	}
	if err := s.clearDebris(ctx, project); err != nil {
		return err
	}
	s.sweepTempFiles(project)
	return nil
}

// clearDebris removes managed .prev/.next containers. Callers must
// only invoke it with no journal present (committed state).
func (s *Service) clearDebris(ctx context.Context, project domain.ProjectID) error {
	containers, err := s.docker.ListProjectContainers(ctx, project)
	if err != nil {
		return err
	}
	for _, c := range containers {
		name := string(c.Name)
		if c.Labels[ManagedLabel] != "true" {
			continue
		}
		if !strings.HasSuffix(name, retiredSuffix) && !strings.HasSuffix(name, stagedSuffix) {
			continue
		}
		if c.Running {
			if err := s.docker.StopContainer(ctx, c.ID, dockerapi.StopTimeout); err != nil {
				return err
			}
		}
		if err := s.docker.RemoveContainer(ctx, c.ID, dockerapi.RemoveOptions{Force: true}); err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}
	}
	return nil
}

// undo rolls a journal's entries back in reverse order, driven by
// inspection of actual Docker state and matched by recorded ID or the
// full ownership conjunction — never by name alone. Entries that
// cannot be proven safe to undo fail closed. All entries are
// attempted; errors are joined so a retry (the next sweep) can finish
// the remainder.
func (s *Service) undo(ctx context.Context, project domain.ProjectID, j *journal) error {
	var errs []error
	for i := len(j.Entries) - 1; i >= 0; i-- {
		e := j.Entries[i]
		var err error
		switch e.Kind {
		case "replace":
			err = s.undoReplace(ctx, project, j, e)
		case "create":
			err = s.undoCreate(ctx, project, j, e)
		case "start":
			err = s.undoStart(ctx, project, e)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("undo %s %s: %w", e.Kind, e.Name, err))
		}
	}
	return errors.Join(errs...)
}

// undoReplace is stop-park-restore-delete: nothing is removed until
// the old container is proven restored, and the unproven new container
// is only ever renamed aside (parked) — if the old container is gone,
// the new one is kept and the undo fails closed, because an unproven
// container running beats nothing running.
func (s *Service) undoReplace(ctx context.Context, project domain.ProjectID, j *journal, e journalEntry) error {
	containers, err := s.docker.ListProjectContainers(ctx, project)
	if err != nil {
		return err
	}
	old := findByID(containers, e.OldID)
	newC := findNew(containers, j, project, e)

	if old == nil {
		if newC != nil {
			return fmt.Errorf("%w: old container %s (id %s) is gone; keeping unproven replacement %s untouched",
				domain.ErrConflict, e.Name, e.OldID, newC.ID)
		}
		// Neither generation exists: there is nothing to restore and
		// nothing safe to guess. Fail closed — the journal is retained
		// and the error names the manual remedy — rather than deleting
		// the only marker of a replacement that has no usable outcome.
		return fmt.Errorf("%w: neither old container %s (id %s) nor its replacement exists; inspect `docker ps -a`, resolve by hand, then remove the journal",
			domain.ErrConflict, e.Name, e.OldID)
	}

	canonical := dockerapi.ContainerName(e.Name)
	staged := dockerapi.ContainerName(e.Name + stagedSuffix)

	// Stop the new container first: the restored old one must contend
	// with nothing for ports or network aliases.
	if newC != nil && newC.Running {
		if err := s.docker.StopContainer(ctx, newC.ID, dockerapi.StopTimeout); err != nil {
			return err
		}
	}
	// Park: free the canonical name without deleting anything.
	if newC != nil && newC.Name == canonical {
		if foreign := findByName(containers, staged); foreign != nil && foreign.ID != newC.ID {
			return fmt.Errorf("%w: unexpected container %s at %s; not touching it", domain.ErrConflict, foreign.ID, staged)
		}
		if err := s.docker.RenameContainer(ctx, newC.ID, staged); err != nil {
			return err
		}
	}
	// Restore: the canonical name must be free or already the old
	// container; anything else is external interference.
	if occ := findByName(containers, canonical); occ != nil && string(occ.ID) != e.OldID && (newC == nil || occ.ID != newC.ID) {
		return fmt.Errorf("%w: container %s (id %s) occupies %s; not touching it", domain.ErrConflict, occ.Name, occ.ID, canonical)
	}
	if old.Name != canonical {
		if err := s.docker.RenameContainer(ctx, old.ID, canonical); err != nil {
			return err
		}
	}
	if e.WasRunning && !old.Running {
		if err := s.docker.StartContainer(ctx, old.ID); err != nil {
			return err
		}
		st, err := s.docker.InspectContainer(ctx, canonical)
		if err != nil {
			return err
		}
		if !st.Running {
			return fmt.Errorf("restored container %s did not stay running", canonical)
		}
	}
	// Delete last, and only best-effort: once the restore is confirmed
	// the entry counts as undone; a removal failure leaves stopped,
	// nonce-labeled debris for the journal-absent sweep.
	if newC != nil {
		_ = s.docker.RemoveContainer(ctx, newC.ID, dockerapi.RemoveOptions{Force: true})
	}
	return nil
}

func (s *Service) undoCreate(ctx context.Context, project domain.ProjectID, j *journal, e journalEntry) error {
	containers, err := s.docker.ListProjectContainers(ctx, project)
	if err != nil {
		return err
	}
	var target *dockerapi.ContainerState
	if e.NewID != "" {
		target = findByID(containers, e.NewID)
	} else if c := findByName(containers, dockerapi.ContainerName(e.Name)); c != nil && j.owns(c.Labels, project) {
		target = c
	}
	if target == nil {
		return nil
	}
	if target.Running {
		if err := s.docker.StopContainer(ctx, target.ID, dockerapi.StopTimeout); err != nil {
			return err
		}
	}
	if err := s.docker.RemoveContainer(ctx, target.ID, dockerapi.RemoveOptions{Force: true}); err != nil && !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	return nil
}

func (s *Service) undoStart(ctx context.Context, project domain.ProjectID, e journalEntry) error {
	containers, err := s.docker.ListProjectContainers(ctx, project)
	if err != nil {
		return err
	}
	c := findByID(containers, e.ID)
	if c == nil || !c.Running {
		return nil
	}
	return s.docker.StopContainer(ctx, c.ID, dockerapi.StopTimeout)
}

// findNew locates the replacement container: by backfilled ID when
// recorded, else by the ownership conjunction at the canonical or
// staged name (the only ID-less shapes the journal-before-mutation
// invariant permits).
func findNew(containers []dockerapi.ContainerState, j *journal, project domain.ProjectID, e journalEntry) *dockerapi.ContainerState {
	if e.NewID != "" {
		return findByID(containers, e.NewID)
	}
	for _, name := range []string{e.Name, e.Name + stagedSuffix} {
		if c := findByName(containers, dockerapi.ContainerName(name)); c != nil && c.ID != dockerapi.ContainerID(e.OldID) && j.owns(c.Labels, project) {
			return c
		}
	}
	return nil
}

func findByID(containers []dockerapi.ContainerState, id string) *dockerapi.ContainerState {
	for i := range containers {
		if string(containers[i].ID) == id {
			return &containers[i]
		}
	}
	return nil
}

func findByName(containers []dockerapi.ContainerState, name dockerapi.ContainerName) *dockerapi.ContainerState {
	for i := range containers {
		if containers[i].Name == name {
			return &containers[i]
		}
	}
	return nil
}

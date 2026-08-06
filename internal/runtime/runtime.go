package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"vibe/internal/dockerapi"
	"vibe/internal/domain"
	"vibe/internal/lock"
	"vibe/internal/model"
	"vibe/internal/registry"
	"vibe/internal/store"
)

// PlanFileName is the canonical plan inside a candidate object.
const PlanFileName = "plan.json"

// Candidate pairs a published candidate's record with its decoded plan.
type Candidate struct {
	Record store.CandidateRecord
	Plan   model.Plan
}

type Service struct {
	docker dockerapi.Client
	store  *store.Store
	locks  lock.Locker
	// replaceDir holds per-project replacement journals — the durable
	// phase markers for container-replacement transactions.
	replaceDir string
}

func NewService(docker dockerapi.Client, st *store.Store, locks lock.Locker, stateDir string) (*Service, error) {
	if docker == nil || st == nil || locks == nil || stateDir == "" {
		return nil, fmt.Errorf("%w: runtime service requires docker, store, locks, and a state dir", domain.ErrInvalid)
	}
	return &Service{docker: docker, store: st, locks: locks, replaceDir: filepath.Join(stateDir, "replace")}, nil
}

// LoadCandidate leases a published candidate and decodes its plan,
// verifying the stored plan digest. The caller closes the lease.
func (s *Service) LoadCandidate(ctx context.Context, digest domain.Digest) (Candidate, *store.Lease, error) {
	rec, err := s.store.ReadCandidateRecord(digest)
	if err != nil {
		return Candidate{}, nil, err
	}
	lease, err := s.store.Open(ctx, store.CandidateObject, digest)
	if err != nil {
		return Candidate{}, nil, err
	}
	data, err := os.ReadFile(filepath.Join(lease.Object.Path, PlanFileName))
	if err != nil {
		lease.Close()
		return Candidate{}, nil, fmt.Errorf("read candidate plan: %w", err)
	}
	if got := domain.SHA256(data); got != rec.Plan {
		lease.Close()
		return Candidate{}, nil, fmt.Errorf("%w: candidate %s plan digest mismatch", domain.ErrConflict, digest)
	}
	plan, err := model.DecodePlan(data)
	if err != nil {
		lease.Close()
		return Candidate{}, nil, fmt.Errorf("candidate %s: %w", digest, err)
	}
	return Candidate{Record: rec, Plan: plan}, lease, nil
}

// Status reports the project's runtime without taking locks.
func (s *Service) Status(ctx context.Context, rec registry.Record) (State, error) {
	containers, err := s.docker.ListProjectContainers(ctx, rec.ID)
	if err != nil {
		return State{}, err
	}
	var approved domain.Digest
	if rec.Approved != nil {
		approved = *rec.Approved
	}
	return summarize(rec.ID, approved, containers), nil
}

// DownOptions controls teardown scope. Volumes are kept by default so
// agent state survives a down/up cycle.
type DownOptions struct {
	RemoveVolumes bool
}

// Down stops and removes every managed container and the project
// network. With RemoveVolumes it also removes the volumes named by the
// approved candidate's plan.
func (s *Service) Down(ctx context.Context, rec registry.Record, opts DownOptions) error {
	held, err := s.locks.Acquire(ctx, lock.Project(string(rec.ID)))
	if err != nil {
		return err
	}
	defer held.Release()

	containers, err := s.docker.ListProjectContainers(ctx, rec.ID)
	if err != nil {
		return err
	}
	for _, c := range containers {
		// The project label alone is copyable; only ManagedLabel proves
		// ownership. Never destroy a container the engine cannot prove
		// it created — same rule as clearDebris and reconcileContainer.
		if c.Labels[ManagedLabel] != "true" {
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
	// Down removed both generations of any pending replacement itself,
	// under the project lock, so a leftover journal has no outcome left
	// to recover; retaining it would dead-end the next Up's sweep on
	// the neither-generation-exists fail-closed path.
	if err := s.deleteJournal(rec.ID); err != nil {
		return err
	}
	if err := s.docker.RemoveNetwork(ctx, dockerapi.NetworkName(model.NetworkName(rec.ID))); err != nil && !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	if !opts.RemoveVolumes {
		return nil
	}
	volumes := []string{model.AgentStateVolumeName(rec.ID)}
	if rec.Approved != nil {
		// The operator asked to remove volumes; the approved candidate is
		// the only source for the plan-declared sidecar volume names. If
		// it cannot be loaded — corrupt, digest mismatch, or GC'd out from
		// under an Approved pointer — fail loudly rather than silently keep
		// data-bearing volumes, the worst outcome for --volumes.
		cand, lease, err := s.LoadCandidate(ctx, *rec.Approved)
		if err != nil {
			return fmt.Errorf("down --volumes: load approved candidate: %w", err)
		}
		for _, v := range cand.Plan.Volumes {
			volumes = append(volumes, v.Name)
		}
		lease.Close()
	}
	for _, name := range volumes {
		if err := s.docker.RemoveVolume(ctx, dockerapi.VolumeName(name)); err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}
	}
	return nil
}

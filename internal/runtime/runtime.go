package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chrisdruta/vibe-tui-box/internal/dockerapi"
	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/lock"
	"github.com/chrisdruta/vibe-tui-box/internal/model"
	"github.com/chrisdruta/vibe-tui-box/internal/registry"
	"github.com/chrisdruta/vibe-tui-box/internal/store"
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
}

func NewService(docker dockerapi.Client, st *store.Store, locks lock.Locker) (*Service, error) {
	if docker == nil || st == nil || locks == nil {
		return nil, fmt.Errorf("%w: runtime service requires docker, store, and locks", domain.ErrInvalid)
	}
	return &Service{docker: docker, store: st, locks: locks}, nil
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
	var plan model.Plan
	if err := json.Unmarshal(data, &plan); err != nil {
		lease.Close()
		return Candidate{}, nil, fmt.Errorf("%w: candidate %s plan: %v", domain.ErrInvalid, digest, err)
	}
	if ferrs := model.Validate(plan); len(ferrs) > 0 {
		lease.Close()
		return Candidate{}, nil, fmt.Errorf("%w: candidate %s plan invalid: %v", domain.ErrInvalid, digest, ferrs[0])
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
		if c.Running {
			if err := s.docker.StopContainer(ctx, c.ID, dockerapi.StopTimeout); err != nil {
				return err
			}
		}
		if err := s.docker.RemoveContainer(ctx, c.ID, dockerapi.RemoveOptions{Force: true}); err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}
	}
	if err := s.docker.RemoveNetwork(ctx, dockerapi.NetworkName(model.NetworkName(rec.ID))); err != nil && !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	if !opts.RemoveVolumes {
		return nil
	}
	volumes := []string{model.AgentStateVolumeName(rec.ID)}
	if rec.Approved != nil {
		if cand, lease, err := s.LoadCandidate(ctx, *rec.Approved); err == nil {
			for _, v := range cand.Plan.Volumes {
				volumes = append(volumes, v.Name)
			}
			lease.Close()
		}
	}
	for _, name := range volumes {
		if err := s.docker.RemoveVolume(ctx, dockerapi.VolumeName(name)); err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}
	}
	return nil
}

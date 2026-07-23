package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chrisdruta/vibe-tui-box/internal/dockerapi"
	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/envfile"
	"github.com/chrisdruta/vibe-tui-box/internal/lock"
	"github.com/chrisdruta/vibe-tui-box/internal/model"
	"github.com/chrisdruta/vibe-tui-box/internal/store"
)

// UpOptions controls reconciliation.
type UpOptions struct {
	// Force replaces containers even when their candidate label already
	// matches (used by rebuild).
	Force bool
	// Progress receives pull/start events; nil discards them.
	Progress dockerapi.ProgressSink
}

// Up reconciles the project to the candidate's plan: ensure network and
// volumes, then compare each desired container against what exists and
// create, start, or replace as needed — sidecars first, dev container
// last. Pre-existing healthy containers are never removed unless their
// replacement was decided first.
func (s *Service) Up(ctx context.Context, cand Candidate, opts UpOptions) (State, error) {
	plan := cand.Plan
	project := plan.Project.ID

	held, err := s.locks.Acquire(ctx, lock.Project(string(project)))
	if err != nil {
		return State{}, err
	}
	defer held.Release()

	// Hold the candidate, its snapshot, and the artifact through the
	// whole operation.
	candLease, err := s.store.Open(ctx, store.CandidateObject, cand.Record.Digest)
	if err != nil {
		return State{}, err
	}
	defer candLease.Close()
	if !plan.Artifact.Digest.IsZero() {
		artLease, err := s.store.Open(ctx, store.ArtifactObject, plan.Artifact.Digest)
		if err != nil {
			return State{}, err
		}
		defer artLease.Close()
	}
	var snapPath string
	if !cand.Record.Snapshot.IsZero() {
		snapLease, err := s.store.Open(ctx, store.SnapshotObject, cand.Record.Snapshot)
		if err != nil {
			return State{}, err
		}
		defer snapLease.Close()
		snapPath = snapLease.Object.Path
	}

	envEntries, err := s.loadEnvFile(plan, snapPath)
	if err != nil {
		return State{}, err
	}

	for _, n := range plan.Networks {
		if err := s.docker.EnsureNetwork(ctx, dockerapi.NetworkSpec{Name: n.Name, Labels: objectLabels(project)}); err != nil {
			return State{}, err
		}
	}
	for _, v := range plan.Volumes {
		if err := s.docker.EnsureVolume(ctx, dockerapi.VolumeSpec{Name: v.Name, Labels: objectLabels(project)}); err != nil {
			return State{}, err
		}
	}

	// Sidecars first, dev last, so the agent container starts into a
	// working service topology.
	desired := append(append([]model.Container{}, plan.Services...), plan.Dev)
	for _, c := range desired {
		req := s.createRequest(plan, cand.Record, c, envEntries)
		if err := s.reconcileContainer(ctx, req, opts); err != nil {
			return State{}, fmt.Errorf("container %s: %w", c.Name, err)
		}
	}

	containers, err := s.docker.ListProjectContainers(ctx, project)
	if err != nil {
		return State{}, err
	}
	return summarize(project, cand.Record.Digest, containers), nil
}

// loadEnvFile parses the snapshotted env file; its entries are handed
// to Docker as container environment and never to a host process.
func (s *Service) loadEnvFile(plan model.Plan, snapPath string) ([]envfile.Entry, error) {
	if plan.Inputs.EnvFile == "" {
		return nil, nil
	}
	if snapPath == "" {
		return nil, fmt.Errorf("%w: plan declares env file but candidate has no snapshot", domain.ErrInvalid)
	}
	f, err := os.Open(filepath.Join(snapPath, model.SnapshotEnvFilePath))
	if err != nil {
		return nil, fmt.Errorf("open snapshotted env file: %w", err)
	}
	defer f.Close()
	entries, err := envfile.Parse(f, envfile.Limits{})
	if err != nil {
		return nil, fmt.Errorf("env file %s: %w", plan.Inputs.EnvFile, err)
	}
	return entries, nil
}

// createRequest translates one planned container into a Docker create
// request. Env-file entries come first so explicit manifest env wins.
func (s *Service) createRequest(plan model.Plan, rec store.CandidateRecord, c model.Container, envEntries []envfile.Entry) dockerapi.CreateRequest {
	var env []string
	if c.Name == plan.Dev.Name {
		for _, e := range envEntries {
			env = append(env, e.Key+"="+e.Value)
		}
	}
	for _, e := range c.Environment {
		env = append(env, e.Key+"="+e.Value)
	}

	mounts := make([]dockerapi.Mount, 0, len(c.Mounts))
	for _, m := range c.Mounts {
		mounts = append(mounts, dockerapi.Mount{
			Kind:     dockerapi.MountKind(m.Kind),
			Source:   m.Source,
			Target:   m.Target,
			ReadOnly: m.ReadOnly,
		})
	}
	ports := make([]dockerapi.PortBinding, 0, len(c.Ports))
	for _, p := range c.Ports {
		ports = append(ports, dockerapi.PortBinding(p))
	}
	planLabels := make(map[string]string, len(c.Labels))
	for _, l := range c.Labels {
		planLabels[l.Key] = l.Value
	}

	image := c.Image.Ref
	if !c.Image.Digest.IsZero() {
		image = pinnedRef(c.Image.Ref, c.Image.Digest)
	}
	network := ""
	if c.Policy.Network == model.NetworkProject && len(plan.Networks) > 0 {
		network = plan.Networks[0].Name
	}
	return dockerapi.CreateRequest{
		Name:    dockerapi.ContainerName(c.Name),
		Image:   image,
		User:    c.User,
		Command: c.Command,
		Env:     env,
		Labels:  containerLabels(planLabels, rec.Digest, plan.Artifact.Digest),
		Mounts:  mounts,
		Ports:   ports,
		Network: network,
		Policy: dockerapi.Policy{
			DropAllCapabilities: c.Policy.DropAllCapabilities,
			NoNewPrivileges:     c.Policy.NoNewPrivileges,
			ReadonlyRootFS:      c.Policy.ReadonlyRootFS,
		},
	}
}

// pinnedRef rewrites a reference to run by digest while keeping the
// repository name for readability.
func pinnedRef(ref string, digest domain.Digest) string {
	repo, _, _ := strings.Cut(ref, "@")
	if i := strings.LastIndex(repo, ":"); i > strings.LastIndex(repo, "/") {
		repo = repo[:i]
	}
	return repo + "@" + digest.String()
}

// reconcileContainer brings one container to its desired specification.
func (s *Service) reconcileContainer(ctx context.Context, req dockerapi.CreateRequest, opts UpOptions) error {
	existing, err := s.docker.InspectContainer(ctx, req.Name)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return s.createAndStart(ctx, req, opts)
	case err != nil:
		return err
	}

	if existing.Labels[ManagedLabel] != "true" {
		return fmt.Errorf("%w: container %s exists but is not managed by vibe", domain.ErrConflict, req.Name)
	}
	sameCandidate := existing.Labels[CandidateLabel] == req.Labels[CandidateLabel]
	if sameCandidate && !opts.Force {
		if existing.Running {
			return nil
		}
		return s.docker.StartContainer(ctx, existing.ID)
	}

	// Replacement decided: stop and remove, then create fresh.
	if existing.Running {
		if err := s.docker.StopContainer(ctx, existing.ID, dockerapi.StopTimeout); err != nil {
			return err
		}
	}
	if err := s.docker.RemoveContainer(ctx, existing.ID, dockerapi.RemoveOptions{Force: true}); err != nil && !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	return s.createAndStart(ctx, req, opts)
}

// createAndStart creates and starts one container, pulling the image
// once if creation reports it missing.
func (s *Service) createAndStart(ctx context.Context, req dockerapi.CreateRequest, opts UpOptions) error {
	id, err := s.docker.CreateContainer(ctx, req)
	if errors.Is(err, domain.ErrNotFound) {
		pullErr := s.docker.PullImage(ctx, dockerapi.ResolvedImage{Ref: dockerapi.ImageRef(req.Image)}, opts.Progress)
		if pullErr != nil {
			return pullErr
		}
		id, err = s.docker.CreateContainer(ctx, req)
	}
	if err != nil {
		return err
	}
	return s.docker.StartContainer(ctx, id)
}

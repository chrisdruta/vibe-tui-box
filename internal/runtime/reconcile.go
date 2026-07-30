package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	// LifecycleOut receives project hook output (raw container bytes,
	// same residual as exec); nil discards it.
	LifecycleOut io.Writer
}

// containerAction reports what reconciliation did to one container.
type containerAction int

const (
	actionNone    containerAction = iota // already running the candidate
	actionStarted                        // existing container started
	actionCreated                        // created (fresh or replacement)
)

// Up reconciles the project to the candidate's plan: ensure network and
// volumes, then compare each desired container against what exists and
// create, start, or replace as needed — sidecars first, dev container
// last. Replacement is transactional: the old generation is parked
// under .prev names and restored on any failure (including lifecycle
// hooks and the pre-commit liveness check); it is deleted only after
// the journal — the durable phase marker — commits.
func (s *Service) Up(ctx context.Context, cand Candidate, opts UpOptions) (State, error) {
	plan := cand.Plan
	project := plan.Project.ID

	held, err := s.locks.Acquire(ctx, lock.Project(string(project)))
	if err != nil {
		return State{}, err
	}
	defer held.Release()

	// Recover any interrupted transaction and clear committed debris
	// before reconciling on top of it.
	if err := s.sweep(ctx, project); err != nil {
		return State{}, err
	}

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

	// Validate the frozen env file now so a malformed one fails `up`,
	// but never inject it here: env values reach a workload only at
	// exec time (`vibe run`, the agent CLI), never as ambient container
	// environment every shell and hook would inherit.
	if _, err := s.loadEnvFile(plan, snapPath); err != nil {
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

	tx, err := newTxn(s, project, cand.Record.Digest)
	if err != nil {
		return State{}, err
	}

	// Sidecars first, dev last, so the agent container starts into a
	// working service topology.
	devAction := actionNone
	desired := append(append([]model.Container{}, plan.Services...), plan.Dev)
	for _, c := range desired {
		req := s.createRequest(plan, cand.Record, c, tx.j.Nonce)
		action, err := s.reconcileContainer(ctx, tx, req, opts)
		if err != nil {
			return State{}, tx.rollback(ctx, fmt.Errorf("container %s: %w", c.Name, err))
		}
		if c.Name == plan.Dev.Name {
			devAction = action
		}
	}

	if err := s.runLifecycle(ctx, plan, devAction, opts); err != nil {
		return State{}, tx.rollback(ctx, err)
	}
	// Pre-commit liveness: "the daemon accepted start" is not proof; a
	// sidecar that started and immediately exited must fail the
	// transaction while the old generation is still restorable. A
	// point-in-time gate, not a readiness check.
	if err := s.checkLiveness(ctx, desired); err != nil {
		return State{}, tx.rollback(ctx, err)
	}
	if err := tx.commit(); err != nil {
		// The journal stays; the next Up's sweep rolls back. Never
		// delete the .prev generation while the phase marker exists.
		return State{}, err
	}
	// The transaction is committed: .prev leftovers are debris. Removal
	// is best-effort; failures are swept by the next Up.
	_ = s.clearDebris(ctx, project)

	containers, err := s.docker.ListProjectContainers(ctx, project)
	if err != nil {
		return State{}, err
	}
	return summarize(project, cand.Record.Digest, containers), nil
}

// checkLiveness re-inspects every desired container after the
// lifecycle hooks and requires it to still be running.
func (s *Service) checkLiveness(ctx context.Context, desired []model.Container) error {
	for _, c := range desired {
		st, err := s.docker.InspectContainer(ctx, dockerapi.ContainerName(c.Name))
		if err != nil {
			return fmt.Errorf("container %s: %w", c.Name, err)
		}
		if !st.Running {
			return fmt.Errorf("container %s exited after start; failing the reconcile before commit", c.Name)
		}
	}
	return nil
}

// runLifecycle executes the payload lifecycle hooks in the dev
// container: post-create on every reconcile (the script marker-guards
// itself, which also self-heals a previously failed run), post-start
// only after an actual create or start. Plans without a payload mount
// — no artifact installed — have no hooks to run, and payloads
// predating the lifecycle script are probed and skipped. A failing
// hook fails the reconcile; the approved-candidate pointer has not
// moved yet.
func (s *Service) runLifecycle(ctx context.Context, plan model.Plan, action containerAction, opts UpOptions) error {
	if !mountsPayload(plan.Dev) {
		return nil
	}
	name := dockerapi.ContainerName(plan.Dev.Name)
	probe, err := s.docker.Exec(ctx, dockerapi.ExecRequest{
		Container: name,
		User:      plan.Dev.User,
		Argv:      []string{"test", "-r", model.PayloadLifecycle},
	})
	if err != nil {
		return fmt.Errorf("lifecycle probe: %w", err)
	}
	if probe.ExitCode != 0 {
		return nil
	}

	out := opts.LifecycleOut
	if out == nil {
		out = io.Discard
	}
	run := func(mode string) error {
		res, err := s.docker.Exec(ctx, dockerapi.ExecRequest{
			Container: name,
			User:      plan.Dev.User,
			WorkDir:   model.WorkspaceTarget,
			Env: []string{
				"VIBE_PROJECT=" + string(plan.Project.ID),
				"VIBE_PROJECT_NAME=" + plan.Project.DisplayName,
			},
			Argv:    []string{"bash", model.PayloadLifecycle, mode},
			Streams: dockerapi.Streams{Out: out, Err: out},
		})
		if err != nil {
			return fmt.Errorf("%s hook: %w", mode, err)
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("%s hook failed with exit code %d", mode, res.ExitCode)
		}
		return nil
	}
	if err := run("post-create"); err != nil {
		return err
	}
	if action == actionCreated || action == actionStarted {
		return run("post-start")
	}
	return nil
}

func mountsPayload(c model.Container) bool {
	for _, m := range c.Mounts {
		if m.Target == model.PayloadTarget {
			return true
		}
	}
	return false
}

// loadEnvFile parses the snapshotted env file. Reconciliation only
// validates it; the entries are injected per exec by the app layer and
// never become container-ambient or host-process environment.
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
// request. Only planned (manifest) environment is baked in; env-file
// values stay exec-scoped. The transaction nonce is stamped as an
// immutable ownership label so crash recovery can prove which
// containers this transaction created.
func (s *Service) createRequest(plan model.Plan, rec store.CandidateRecord, c model.Container, nonce string) dockerapi.CreateRequest {
	var env []string
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
	planLabels := make(map[string]string, len(c.Labels)+1)
	for _, l := range c.Labels {
		planLabels[l.Key] = l.Value
	}
	planLabels[TxnLabel] = nonce

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

// reconcileContainer brings one container to its desired specification
// and reports what it had to do. Every mutation is journaled through
// the transaction BEFORE it executes, so a crash at any point is
// recoverable by the sweep.
func (s *Service) reconcileContainer(ctx context.Context, tx *txn, req dockerapi.CreateRequest, opts UpOptions) (containerAction, error) {
	existing, err := s.docker.InspectContainer(ctx, req.Name)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return s.createFresh(ctx, tx, req, opts)
	case err != nil:
		return actionNone, err
	}

	if existing.Labels[ManagedLabel] != "true" {
		return actionNone, fmt.Errorf("%w: container %s exists but is not managed by vibe", domain.ErrConflict, req.Name)
	}
	sameCandidate := existing.Labels[CandidateLabel] == req.Labels[CandidateLabel]
	if sameCandidate && !opts.Force {
		if existing.Running {
			return actionNone, nil
		}
		if _, err := tx.append(journalEntry{Kind: "start", Name: string(req.Name), ID: string(existing.ID)}); err != nil {
			return actionNone, err
		}
		return actionStarted, s.docker.StartContainer(ctx, existing.ID)
	}

	// Replacement decided: stage the new container while the old one is
	// still running, then retire and swap. The old generation survives
	// as <name>.prev until the whole transaction commits.
	idx, err := tx.append(journalEntry{
		Kind:       "replace",
		Name:       string(req.Name),
		OldID:      string(existing.ID),
		WasRunning: existing.Running,
	})
	if err != nil {
		return actionNone, err
	}
	staged := req
	staged.Name = req.Name + stagedSuffix
	newID, err := s.createOnly(ctx, staged, opts)
	if err != nil {
		return actionNone, err
	}
	if err := tx.backfill(idx, func(e *journalEntry) { e.NewID = string(newID) }); err != nil {
		return actionNone, err
	}
	if existing.Running {
		if err := s.docker.StopContainer(ctx, existing.ID, dockerapi.StopTimeout); err != nil {
			return actionNone, err
		}
	}
	if err := s.docker.RenameContainer(ctx, existing.ID, req.Name+retiredSuffix); err != nil {
		return actionNone, err
	}
	if err := s.docker.RenameContainer(ctx, newID, req.Name); err != nil {
		return actionNone, err
	}
	return actionCreated, s.docker.StartContainer(ctx, newID)
}

// createFresh journals and creates a container that did not exist.
func (s *Service) createFresh(ctx context.Context, tx *txn, req dockerapi.CreateRequest, opts UpOptions) (containerAction, error) {
	idx, err := tx.append(journalEntry{Kind: "create", Name: string(req.Name)})
	if err != nil {
		return actionNone, err
	}
	id, err := s.createOnly(ctx, req, opts)
	if err != nil {
		return actionNone, err
	}
	if err := tx.backfill(idx, func(e *journalEntry) { e.NewID = string(id) }); err != nil {
		return actionNone, err
	}
	return actionCreated, s.docker.StartContainer(ctx, id)
}

// createOnly creates one container without starting it, pulling the
// image once if creation reports it missing.
func (s *Service) createOnly(ctx context.Context, req dockerapi.CreateRequest, opts UpOptions) (dockerapi.ContainerID, error) {
	id, err := s.docker.CreateContainer(ctx, req)
	if errors.Is(err, domain.ErrNotFound) {
		pullErr := s.docker.PullImage(ctx, dockerapi.ResolvedImage{Ref: dockerapi.ImageRef(req.Image)}, opts.Progress)
		if pullErr != nil {
			return "", pullErr
		}
		id, err = s.docker.CreateContainer(ctx, req)
	}
	return id, err
}

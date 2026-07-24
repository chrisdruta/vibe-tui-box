package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/envfile"
	"github.com/chrisdruta/vibe-tui-box/internal/model"
	"github.com/chrisdruta/vibe-tui-box/internal/paths"
	"github.com/chrisdruta/vibe-tui-box/internal/registry"
	"github.com/chrisdruta/vibe-tui-box/internal/runtime"
	"github.com/chrisdruta/vibe-tui-box/internal/schema"
	"github.com/chrisdruta/vibe-tui-box/internal/snapshot"
)

// ConfigRequest compiles and prints the canonical plan for the project
// containing Dir. It is diagnostic: inputs are frozen exactly as for a
// candidate, but nothing is registered as approved.
type ConfigRequest struct {
	Dir string
}

type ConfigResult struct {
	Plan      model.Plan
	Canonical []byte
	Snapshot  snapshot.Result
}

func (a *App) Config(ctx context.Context, req ConfigRequest) (ConfigResult, error) {
	root, err := paths.Discover(req.Dir)
	if err != nil {
		return ConfigResult{}, &domain.OpError{Op: "config", Err: err}
	}
	rec, err := a.deps.Registry.Resolve(ctx, root)
	if err != nil {
		return ConfigResult{}, &domain.OpError{Op: "config", Err: err}
	}
	frozen, err := a.freezeInputs(ctx, root, rec)
	if err != nil {
		return ConfigResult{}, &domain.OpError{Op: "config", Project: rec.ID, Err: err}
	}
	artifact, releaseArtifact, err := a.loadArtifact(ctx, rec)
	if err != nil {
		return ConfigResult{}, &domain.OpError{Op: "config", Project: rec.ID, Err: err}
	}
	defer releaseArtifact()
	brokerStore, err := a.brokerStore(rec.ID)
	if err != nil {
		return ConfigResult{}, &domain.OpError{Op: "config", Project: rec.ID, Err: err}
	}
	plan, ferrs := model.Compile(model.CompileInput{
		Project:          rec,
		Artifact:         artifact,
		Manifest:         frozen.Manifest,
		Snapshot:         frozen.Snapshot,
		BrokerResultsDir: brokerStore.ResultsDir(),
		// Image references stay unresolved in the diagnostic plan; up
		// resolves them to digests when it builds the real candidate.
	})
	if len(ferrs) > 0 {
		return ConfigResult{}, &domain.OpError{Op: "config", Project: rec.ID, Err: fieldErrs(ferrs)}
	}
	canonical, err := model.CanonicalJSON(plan)
	if err != nil {
		return ConfigResult{}, &domain.OpError{Op: "config", Project: rec.ID, Err: err}
	}
	return ConfigResult{Plan: plan, Canonical: canonical, Snapshot: frozen.Snapshot}, nil
}

// frozenInputs is the outcome of freezing a project's configuration
// inputs into an immutable snapshot: the published snapshot plus the
// manifest as loaded back from that snapshot. No later stage rereads
// workspace paths.
type frozenInputs struct {
	Manifest schema.Manifest
	Snapshot snapshot.Result
	Env      []envfile.Entry
}

// Snapshot layout constants live in the model package
// (model.SnapshotManifestPath and friends) so runtime and app agree.

func (a *App) freezeInputs(ctx context.Context, root paths.Root, rec registry.Record) (frozenInputs, error) {
	// First pass over the workspace manifest only learns which inputs to
	// freeze; every validated read below comes from the snapshot.
	preview, err := loadManifestFile(filepath.Join(root.Path, paths.ManifestRelPath))
	if err != nil {
		return frozenInputs{}, err
	}

	spec := snapshot.Spec{
		ProjectRoot: root,
		Entries: []snapshot.Entry{
			{Source: paths.ManifestRelPath, Dest: model.SnapshotManifestPath},
		},
	}
	if ef := preview.Manifest.EnvFile; ef != "" {
		spec.Entries = append(spec.Entries, snapshot.Entry{Source: ef, Dest: model.SnapshotEnvFilePath})
	}
	if preview.Manifest.Image.Extension {
		spec.Entries = append(spec.Entries,
			snapshot.Entry{Source: ".vibe/Dockerfile", Dest: model.SnapshotDockerfilePath},
			snapshot.Entry{Source: ".vibe/build", Dest: model.SnapshotBuildDir, Optional: true})
	}
	for i, imp := range preview.Manifest.Runtime.Imports {
		spec.Entries = append(spec.Entries, snapshot.Entry{
			Source: imp.Source,
			Dest:   model.SnapshotImportDir + "/" + strconv.Itoa(i),
		})
	}
	snap, err := a.deps.Snapshots.Create(ctx, spec)
	if err != nil {
		return frozenInputs{}, err
	}

	doc, err := loadManifestFile(filepath.Join(snap.Path, model.SnapshotManifestPath))
	if err != nil {
		return frozenInputs{}, err
	}
	if ferrs := doc.Validate(); len(ferrs) > 0 {
		return frozenInputs{}, fieldErrs(ferrs)
	}

	frozen := frozenInputs{Manifest: doc.Manifest, Snapshot: snap}
	if doc.Manifest.EnvFile != "" {
		f, err := os.Open(filepath.Join(snap.Path, model.SnapshotEnvFilePath))
		if err != nil {
			return frozenInputs{}, fmt.Errorf("open snapshotted env file: %w", err)
		}
		defer f.Close()
		entries, err := envfile.Parse(f, envfile.Limits{})
		if err != nil {
			return frozenInputs{}, fmt.Errorf("env file %s: %w", doc.Manifest.EnvFile, err)
		}
		frozen.Env = entries
	}
	return frozen, nil
}

func loadManifestFile(path string) (*schema.Document, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", domain.ErrNotFound, path)
		}
		return nil, err
	}
	defer f.Close()
	doc, err := schema.Load(f, schema.Limits{})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return doc, nil
}

// UpRequest reconciles the project to a fresh candidate compiled from
// the current workspace inputs.
type UpRequest struct {
	Dir   string
	Force bool // replace containers even when already in sync (rebuild)
}

type UpResult struct {
	Record    registry.Record
	Candidate domain.Digest
	State     runtime.State
}

func (a *App) Up(ctx context.Context, req UpRequest) (UpResult, error) {
	op := "up"
	if req.Force {
		op = "rebuild"
	}
	root, rec, err := a.resolveProject(ctx, req.Dir)
	if err != nil {
		return UpResult{}, &domain.OpError{Op: op, Err: err}
	}
	if err := a.deps.Docker.Ping(ctx); err != nil {
		return UpResult{}, &domain.OpError{Op: op, Project: rec.ID, Err: err}
	}
	cand, err := a.prepareCandidate(ctx, root, rec)
	if err != nil {
		return UpResult{}, &domain.OpError{Op: op, Project: rec.ID, Err: err}
	}
	state, err := a.runtime.Up(ctx, cand, runtime.UpOptions{
		Force:        req.Force,
		Progress:     a.deps.Progress,
		LifecycleOut: a.deps.LifecycleOut,
	})
	if err != nil {
		return UpResult{}, &domain.OpError{Op: op, Project: rec.ID, Err: err}
	}
	// The durable candidate exists and its containers run; only now move
	// the approved-candidate pointer.
	updated, err := a.deps.Registry.Update(ctx, rec.ID, rec.Revision, func(r *registry.Record) error {
		r.Approved = &cand.Record.Digest
		return nil
	})
	if err != nil {
		return UpResult{}, &domain.OpError{Op: op, Project: rec.ID, Err: err}
	}
	state.Approved = cand.Record.Digest
	for i := range state.Containers {
		state.Containers[i].InSync = state.Containers[i].Candidate == cand.Record.Digest
	}
	return UpResult{Record: updated, Candidate: cand.Record.Digest, State: state}, nil
}

// DownRequest tears down the project's containers and network. Volumes
// survive unless RemoveVolumes is set.
type DownRequest struct {
	Dir           string
	RemoveVolumes bool
}

type DownResult struct {
	Record registry.Record
}

func (a *App) Down(ctx context.Context, req DownRequest) (DownResult, error) {
	_, rec, err := a.resolveProject(ctx, req.Dir)
	if err != nil {
		return DownResult{}, &domain.OpError{Op: "down", Err: err}
	}
	if err := a.deps.Docker.Ping(ctx); err != nil {
		return DownResult{}, &domain.OpError{Op: "down", Project: rec.ID, Err: err}
	}
	if err := a.runtime.Down(ctx, rec, runtime.DownOptions{RemoveVolumes: req.RemoveVolumes}); err != nil {
		return DownResult{}, &domain.OpError{Op: "down", Project: rec.ID, Err: err}
	}
	return DownResult{Record: rec}, nil
}

type StatusRequest struct {
	Dir string
}

type StatusResult struct {
	Record registry.Record
	State  runtime.State
}

func (a *App) Status(ctx context.Context, req StatusRequest) (StatusResult, error) {
	_, rec, err := a.resolveProject(ctx, req.Dir)
	if err != nil {
		return StatusResult{}, &domain.OpError{Op: "status", Err: err}
	}
	if err := a.deps.Docker.Ping(ctx); err != nil {
		return StatusResult{}, &domain.OpError{Op: "status", Project: rec.ID, Err: err}
	}
	state, err := a.runtime.Status(ctx, rec)
	if err != nil {
		return StatusResult{}, &domain.OpError{Op: "status", Project: rec.ID, Err: err}
	}
	return StatusResult{Record: rec, State: state}, nil
}

// resolveProject is the shared discover-then-resolve step.
func (a *App) resolveProject(ctx context.Context, dir string) (paths.Root, registry.Record, error) {
	root, err := paths.Discover(dir)
	if err != nil {
		return paths.Root{}, registry.Record{}, err
	}
	rec, err := a.deps.Registry.Resolve(ctx, root)
	if err != nil {
		return paths.Root{}, registry.Record{}, err
	}
	return root, rec, nil
}

// fieldErrs folds diagnostics into one error that keeps every line and
// still classifies as ErrInvalid.
func fieldErrs(errs []domain.FieldError) error {
	var buf bytes.Buffer
	for i, e := range errs {
		if i > 0 {
			buf.WriteString("\n")
		}
		buf.WriteString(e.Error())
	}
	return fmt.Errorf("%w: %s", domain.ErrInvalid, buf.String())
}

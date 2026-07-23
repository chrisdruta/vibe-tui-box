package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chrisdruta/vibe-tui-box/internal/dockerapi"
	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/model"
	"github.com/chrisdruta/vibe-tui-box/internal/paths"
	"github.com/chrisdruta/vibe-tui-box/internal/registry"
	"github.com/chrisdruta/vibe-tui-box/internal/runtime"
	"github.com/chrisdruta/vibe-tui-box/internal/store"
)

// prepareCandidate runs the shared candidate pipeline: freeze inputs,
// resolve every image reference to a digest, compile and validate the
// canonical plan, then publish the immutable candidate with its record.
// No stage after the snapshot rereads workspace paths.
func (a *App) prepareCandidate(ctx context.Context, root paths.Root, rec registry.Record) (runtime.Candidate, error) {
	frozen, err := a.freezeInputs(ctx, root, rec)
	if err != nil {
		return runtime.Candidate{}, err
	}
	artifact, releaseArtifact, err := a.loadArtifact(ctx, rec)
	if err != nil {
		return runtime.Candidate{}, err
	}
	defer releaseArtifact()

	refs := []string{frozen.Manifest.Image.Base}
	for _, name := range frozen.Manifest.ServiceNames() {
		refs = append(refs, frozen.Manifest.Services[name].Image)
	}
	digests := make(map[string]domain.Digest, len(refs))
	for _, ref := range refs {
		resolved, err := a.deps.Docker.ResolveImage(ctx, dockerapi.ImageRef(ref))
		if err != nil {
			return runtime.Candidate{}, fmt.Errorf("resolve image %s: %w", ref, err)
		}
		digests[ref] = resolved.Digest
	}
	if frozen.Manifest.Image.Extension {
		built, err := a.buildExtension(ctx, rec, frozen, digests)
		if err != nil {
			return runtime.Candidate{}, err
		}
		digests[model.ExtensionImageRef(rec.ID)] = built.Digest
	}

	brokerStore, err := a.brokerStore(rec.ID)
	if err != nil {
		return runtime.Candidate{}, err
	}
	plan, ferrs := model.Compile(model.CompileInput{
		Project:          rec,
		Artifact:         artifact,
		Manifest:         frozen.Manifest,
		Snapshot:         frozen.Snapshot,
		ImageDigests:     digests,
		BrokerResultsDir: brokerStore.ResultsDir(),
	})
	if len(ferrs) > 0 {
		return runtime.Candidate{}, fieldErrs(ferrs)
	}
	planBytes, err := model.CanonicalJSON(plan)
	if err != nil {
		return runtime.Candidate{}, err
	}

	staging, err := a.deps.Store.NewStaging("candidate")
	if err != nil {
		return runtime.Candidate{}, err
	}
	defer os.RemoveAll(staging)
	if err := os.WriteFile(filepath.Join(staging, runtime.PlanFileName), planBytes, 0o600); err != nil {
		return runtime.Candidate{}, fmt.Errorf("stage candidate: %w", err)
	}
	digest, _, err := store.DigestTree(staging)
	if err != nil {
		return runtime.Candidate{}, err
	}
	if _, err := a.deps.Store.Publish(ctx, store.CandidateObject, staging, digest); err != nil {
		return runtime.Candidate{}, err
	}

	candRecord := store.CandidateRecord{
		Digest:    digest,
		ProjectID: rec.ID,
		Kind:      store.RuntimeCandidate,
		Snapshot:  frozen.Snapshot.Digest,
		Plan:      domain.SHA256(planBytes),
		Images:    resolvedImages(plan),
		CreatedAt: a.deps.Clock.Now().UTC(),
	}
	if err := a.deps.Store.WriteCandidateRecord(candRecord); err != nil {
		return runtime.Candidate{}, err
	}
	return runtime.Candidate{Record: candRecord, Plan: plan}, nil
}

// loadArtifact leases the project's pinned artifact for the duration of
// an operation; a project without a pin gets the zero artifact.
func (a *App) loadArtifact(ctx context.Context, rec registry.Record) (store.Artifact, func(), error) {
	noop := func() {}
	if rec.Artifact.IsZero() {
		return store.Artifact{}, noop, nil
	}
	arec, err := a.deps.Store.ReadArtifactRecord(rec.Artifact)
	if err != nil {
		return store.Artifact{}, noop, err
	}
	lease, err := a.deps.Store.Open(ctx, store.ArtifactObject, rec.Artifact)
	if err != nil {
		return store.Artifact{}, noop, err
	}
	return store.Artifact{Record: arec, Path: lease.Object.Path}, func() { lease.Close() }, nil
}

func resolvedImages(plan model.Plan) []store.ResolvedImage {
	out := make([]store.ResolvedImage, 0, len(plan.Images))
	for _, img := range plan.Images {
		out = append(out, store.ResolvedImage{Ref: img.Ref, Digest: img.Digest})
	}
	return out
}

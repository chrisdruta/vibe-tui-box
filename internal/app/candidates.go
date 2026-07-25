package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chrisdruta/vibe-tui-box/internal/builder"
	"github.com/chrisdruta/vibe-tui-box/internal/dockerapi"
	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/model"
	"github.com/chrisdruta/vibe-tui-box/internal/paths"
	"github.com/chrisdruta/vibe-tui-box/internal/registry"
	"github.com/chrisdruta/vibe-tui-box/internal/runtime"
	"github.com/chrisdruta/vibe-tui-box/internal/schema"
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
	if hasTools(frozen.Manifest) {
		built, err := a.buildTools(ctx, rec, frozen, digests)
		if err != nil {
			return runtime.Candidate{}, err
		}
		digests[model.ToolsImageRef(rec.ID)] = built.Digest
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
	// Unchanged inputs recompile to the identical plan, so the digest —
	// and the published candidate — already exist. Adopt the first
	// candidate's timestamp so the rewrite is byte-identical instead of
	// diverging by clock alone (records are immutable: a divergent
	// rewrite is a conflict). Every other field is derived from the
	// plan the digest covers, so a real content disagreement still
	// reaches WriteCandidateRecord and still conflicts.
	if existing, err := a.deps.Store.ReadCandidateRecord(digest); err == nil {
		candRecord.CreatedAt = existing.CreatedAt
	}
	if err := a.deps.Store.WriteCandidateRecord(candRecord); err != nil {
		return runtime.Candidate{}, err
	}
	return runtime.Candidate{Record: candRecord, Plan: plan}, nil
}

// hasTools reports whether the manifest selects any engine-installed
// agents or toolchains.
func hasTools(m schema.Manifest) bool {
	return len(m.Image.Agents) > 0 || len(m.Image.Toolchains) > 0
}

// buildTools builds the engine-generated install image. The Dockerfile
// is engine-authored from the manifest's closed agent/toolchain enums —
// unlike extension builds it is not project input, so no per-digest
// operator approval applies. The build context holds nothing but the
// generated Dockerfile.
func (a *App) buildTools(ctx context.Context, rec registry.Record, frozen frozenInputs, digests map[string]domain.Digest) (dockerapi.BuiltImage, error) {
	// A non-empty AgentRefresh (minted by every rebuild and by `vibe up
	// --refresh-agents`, persisted on the record) stamps the unversioned
	// agent layers with the token so they re-pull and then stay
	// warm-cached until the next refresh. Manifest-pinned agents live in
	// plain layers the token never reaches.
	refresh := rec.AgentRefresh != ""
	dockerfile := builder.GenerateInstall(frozen.Manifest.Image.Agents, frozen.Manifest.Image.Toolchains, refresh)
	if err := builder.ValidateDockerfile(dockerfile); err != nil {
		return dockerapi.BuiltImage{}, fmt.Errorf("generated install dockerfile: %w", err)
	}
	contextDir, err := a.deps.Store.NewStaging("tools-context")
	if err != nil {
		return dockerapi.BuiltImage{}, err
	}
	defer os.RemoveAll(contextDir)
	if err := os.WriteFile(filepath.Join(contextDir, "Dockerfile"), dockerfile, 0o600); err != nil {
		return dockerapi.BuiltImage{}, fmt.Errorf("stage tools context: %w", err)
	}

	base := frozen.Manifest.Image.Base
	return a.builder.Build(ctx, builder.Candidate{
		Digest:       domain.SHA256(dockerfile),
		ContextDir:   contextDir,
		Dockerfile:   "Dockerfile",
		BaseImage:    dockerapi.ResolvedImage{Ref: dockerapi.ImageRef(base), Digest: digests[base]},
		Tag:          model.ToolsImageRef(rec.ID),
		RefreshToken: rec.AgentRefresh,
	}, a.deps.Progress)
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

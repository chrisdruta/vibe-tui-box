package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/paths"
	"github.com/chrisdruta/vibe-tui-box/internal/registry"
	"github.com/chrisdruta/vibe-tui-box/internal/store"
)

// ProvisionRequest installs the running binary and its embedded payload
// as an immutable artifact. When Dir is inside a registered project,
// the project is pinned to the new artifact.
type ProvisionRequest struct {
	Dir string
}

type ProvisionResult struct {
	Artifact store.ArtifactRecord
	Pinned   *registry.Record
}

func (a *App) Provision(ctx context.Context, req ProvisionRequest) (ProvisionResult, error) {
	if a.deps.Payload == nil {
		return ProvisionResult{}, &domain.OpError{Op: "provision",
			Err: fmt.Errorf("%w: this build embeds no payload", domain.ErrUnavailable)}
	}
	platform, err := domain.CurrentPlatform()
	if err != nil {
		return ProvisionResult{}, &domain.OpError{Op: "provision", Err: err}
	}

	staging, err := a.deps.Store.NewStaging("artifact")
	if err != nil {
		return ProvisionResult{}, &domain.OpError{Op: "provision", Err: err}
	}
	defer os.RemoveAll(staging)

	binaryDigest, err := copyBinary(a.deps.Executable, filepath.Join(staging, store.ArtifactBinaryRelPath))
	if err != nil {
		return ProvisionResult{}, &domain.OpError{Op: "provision", Err: err}
	}
	payloadDigest, err := a.deps.Payload.Extract(ctx, filepath.Join(staging, store.ArtifactPayloadRelPath))
	if err != nil {
		return ProvisionResult{}, &domain.OpError{Op: "provision", Err: err}
	}

	digest, _, err := store.DigestTree(staging)
	if err != nil {
		return ProvisionResult{}, &domain.OpError{Op: "provision", Err: err}
	}
	if _, err := a.deps.Store.Publish(ctx, store.ArtifactObject, staging, digest); err != nil {
		return ProvisionResult{}, &domain.OpError{Op: "provision", Err: err}
	}
	record := store.ArtifactRecord{
		Digest:        digest,
		Version:       a.deps.Version.Release,
		Platform:      platform,
		BinaryDigest:  binaryDigest,
		PayloadDigest: payloadDigest,
		Release: domain.ReleaseProvenance{
			Source:  "self-provision",
			Version: a.deps.Version.Release,
		},
		InstalledAt: a.deps.Clock.Now().UTC(),
	}
	if err := a.deps.Store.WriteArtifactRecord(record); err != nil {
		return ProvisionResult{}, &domain.OpError{Op: "provision", Err: err}
	}

	result := ProvisionResult{Artifact: record}
	pinned, err := a.pinProject(ctx, req.Dir, record)
	if err != nil {
		return ProvisionResult{}, &domain.OpError{Op: "provision", Err: err}
	}
	result.Pinned = pinned
	return result, nil
}

// pinProject points the project containing dir (when registered) at the
// artifact. An unregistered or absent project is not an error.
func (a *App) pinProject(ctx context.Context, dir string, artifact store.ArtifactRecord) (*registry.Record, error) {
	root, err := paths.Discover(dir)
	if err != nil {
		return nil, nil // no project here
	}
	rec, err := a.deps.Registry.Resolve(ctx, root)
	if err != nil {
		return nil, nil // project not registered
	}
	updated, err := a.deps.Registry.Update(ctx, rec.ID, rec.Revision, func(r *registry.Record) error {
		r.Artifact = artifact.Digest
		r.ReleaseVersion = artifact.Version
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

// UpdateRequest acquires a release artifact from the canonical source
// and installs it: store object, project pin, and host binary handoff.
type UpdateRequest struct {
	Dir     string
	Version string
}

type UpdateResult struct {
	Artifact   store.ArtifactRecord
	Pinned     *registry.Record
	BinaryPath string
}

func (a *App) Update(ctx context.Context, req UpdateRequest) (UpdateResult, error) {
	if a.deps.Release == nil {
		return UpdateResult{}, &domain.OpError{Op: "update",
			Err: fmt.Errorf("%w: no release source configured", domain.ErrUnavailable)}
	}
	if req.Version == "" {
		return UpdateResult{}, &domain.OpError{Op: "update",
			Err: fmt.Errorf("%w: a release version is required (e.g. --version v2.0.0)", domain.ErrInvalid)}
	}
	artifact, err := a.deps.Release.Acquire(ctx, req.Version)
	if err != nil {
		return UpdateResult{}, &domain.OpError{Op: "update", Err: err}
	}
	pinned, err := a.pinProject(ctx, req.Dir, artifact.Record)
	if err != nil {
		return UpdateResult{}, &domain.OpError{Op: "update", Err: err}
	}
	binPath, err := a.installBinary(artifact)
	if err != nil {
		return UpdateResult{}, &domain.OpError{Op: "update", Err: err}
	}
	return UpdateResult{Artifact: artifact.Record, Pinned: pinned, BinaryPath: binPath}, nil
}

// installBinary copies the artifact's engine binary into the host bin
// directory and atomically repoints the `vibe` symlink — the shim
// handoff: the next invocation runs the new release.
func (a *App) installBinary(artifact store.Artifact) (string, error) {
	target := filepath.Join(a.deps.Layout.Bin, "vibe-"+artifact.Record.Digest.Hex()[:12])
	if _, err := copyBinary(artifact.BinaryPath(), target); err != nil {
		return "", err
	}
	link := filepath.Join(a.deps.Layout.Bin, "vibe")
	tmp := link + ".tmp"
	os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return "", fmt.Errorf("install binary: %w", err)
	}
	if err := os.Rename(tmp, link); err != nil {
		return "", fmt.Errorf("install binary: %w", err)
	}
	return link, nil
}

// copyBinary copies an executable file, returning its content digest.
func copyBinary(src, dst string) (domain.Digest, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return domain.Digest{}, fmt.Errorf("read binary: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return domain.Digest{}, err
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		if os.IsExist(err) {
			// Same digest-addressed target: verify instead of clobbering.
			existing, readErr := os.ReadFile(dst)
			if readErr == nil && domain.SHA256(existing) == domain.SHA256(data) {
				return domain.SHA256(data), nil
			}
			return domain.Digest{}, fmt.Errorf("%w: %s exists with different content", domain.ErrConflict, dst)
		}
		return domain.Digest{}, fmt.Errorf("write binary: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return domain.Digest{}, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return domain.Digest{}, err
	}
	if err := f.Close(); err != nil {
		return domain.Digest{}, err
	}
	return domain.SHA256(data), nil
}

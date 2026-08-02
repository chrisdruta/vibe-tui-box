package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/lock"
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
	fail := opFail[ProvisionResult]("provision", "")
	if a.deps.Payload == nil {
		return fail(fmt.Errorf("%w: this build embeds no payload", domain.ErrUnavailable))
	}
	platform, err := domain.CurrentPlatform()
	if err != nil {
		return fail(err)
	}

	// Shared store-global hold from the artifact publish through the
	// project pin, shielding the span from an exclusive GC.
	sharedStore, err := a.deps.Locks.AcquireShared(ctx, lock.StoreGlobal())
	if err != nil {
		return fail(err)
	}
	defer sharedStore.Release()

	staging, err := a.deps.Store.NewStaging("artifact")
	if err != nil {
		return fail(err)
	}
	defer os.RemoveAll(staging)

	binaryDigest, err := copyBinary(a.deps.Executable, filepath.Join(staging, store.ArtifactBinaryRelPath))
	if err != nil {
		return fail(err)
	}
	payloadDigest, err := a.deps.Payload.Extract(ctx, filepath.Join(staging, store.ArtifactPayloadRelPath))
	if err != nil {
		return fail(err)
	}

	digest, _, err := store.DigestTree(staging)
	if err != nil {
		return fail(err)
	}
	if _, err := a.deps.Store.Publish(ctx, store.ArtifactObject, staging, digest); err != nil {
		return fail(err)
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
		return fail(err)
	}

	result := ProvisionResult{Artifact: record}
	pinned, err := a.pinProject(ctx, req.Dir, record)
	if err != nil {
		return fail(err)
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
	fail := opFail[UpdateResult]("update", "")
	if a.deps.Release == nil {
		return fail(fmt.Errorf("%w: no release source configured", domain.ErrUnavailable))
	}
	if req.Version == "" {
		return fail(fmt.Errorf("%w: a release version is required (e.g. --version v2.0.0)", domain.ErrInvalid))
	}
	// Same span discipline as Provision: the acquired artifact is
	// unrooted until the pin (or the newest-release rule) covers it.
	sharedStore, err := a.deps.Locks.AcquireShared(ctx, lock.StoreGlobal())
	if err != nil {
		return fail(err)
	}
	defer sharedStore.Release()
	artifact, err := a.deps.Release.Acquire(ctx, req.Version)
	if err != nil {
		return fail(err)
	}
	pinned, err := a.pinProject(ctx, req.Dir, artifact.Record)
	if err != nil {
		return fail(err)
	}
	binPath, err := a.installBinary(artifact)
	if err != nil {
		return fail(err)
	}
	// Unpinned (no registered project here) leaves the live server
	// alone: there is no record to materialize a conf from, and the
	// next `vibe tui` join stamps as it always has.
	if pinned != nil {
		a.restampTui(ctx, *pinned, binPath)
	}
	a.bumpTuiSerial(ctx)
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

// copyBinary streams an executable file to dst, returning its content
// digest; the binary never buffers in memory whole.
func copyBinary(src, dst string) (domain.Digest, error) {
	in, err := os.Open(src)
	if err != nil {
		return domain.Digest{}, fmt.Errorf("read binary: %w", err)
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return domain.Digest{}, err
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		if os.IsExist(err) {
			// Same digest-addressed target: verify instead of clobbering.
			srcDigest, err := domain.SHA256Reader(in)
			if err != nil {
				return domain.Digest{}, fmt.Errorf("read binary: %w", err)
			}
			if existing, err := os.Open(dst); err == nil {
				dstDigest, err := domain.SHA256Reader(existing)
				existing.Close()
				if err == nil && dstDigest == srcDigest {
					return srcDigest, nil
				}
			}
			return domain.Digest{}, fmt.Errorf("%w: %s exists with different content", domain.ErrConflict, dst)
		}
		return domain.Digest{}, fmt.Errorf("write binary: %w", err)
	}
	digest, err := domain.SHA256Reader(io.TeeReader(in, f))
	if err != nil {
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
	return digest, nil
}

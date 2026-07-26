package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chrisdruta/vibe-tui-box/internal/dev"
	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/paths"
	"github.com/chrisdruta/vibe-tui-box/internal/registry"
	"github.com/chrisdruta/vibe-tui-box/internal/store"
	"github.com/chrisdruta/vibe-tui-box/internal/terminal"
)

// EngineModule identifies the engine repository for dev-mode source
// verification.
const EngineModule = "github.com/chrisdruta/vibe-tui-box"

// DevOnRequest builds the engine from Source (default: the project
// itself) and switches the project to the resulting dev artifact.
type DevOnRequest struct {
	Dir    string
	Source string
}

type DevOnResult struct {
	Record     dev.Record
	Project    registry.Record
	Artifact   store.ArtifactRecord
	BinaryPath string
}

func (a *App) DevOn(ctx context.Context, req DevOnRequest) (DevOnResult, error) {
	fail := opFail[DevOnResult]("dev on", "")
	_, rec, err := a.resolveProject(ctx, req.Dir)
	if err != nil {
		return fail(err)
	}
	fail = opFail[DevOnResult]("dev on", rec.ID)
	sourceDir := req.Source
	if sourceDir == "" {
		sourceDir = req.Dir
	}
	sourceRoot, err := paths.Discover(sourceDir)
	if err != nil {
		return fail(err)
	}
	if err := dev.VerifyEngineRepo(sourceRoot.Path, EngineModule); err != nil {
		return fail(err)
	}
	// Entering dev mode is the source-trust ceremony: the build output
	// becomes the host `vibe` binary, so flipping a release-mode project
	// to dev builds takes a distinct confirmation. A sync of a project
	// already in dev mode repeats that decision and stays quiet.
	if rec.Mode != registry.ModeDev {
		if a.deps.Prompt == nil {
			return fail(fmt.Errorf("%w: entering dev mode needs interactive confirmation", domain.ErrConflict))
		}
		ok, err := a.deps.Prompt.Confirm(ctx, terminal.Confirmation{
			Title:    "Enter dev mode? Engine binaries built from this source will run on the host.",
			Chrome:   []string{"source: " + sourceRoot.Path},
			Question: "trust this source?",
		})
		if err != nil {
			return fail(err)
		}
		if !ok {
			return fail(fmt.Errorf("%w: not confirmed", domain.ErrCanceled))
		}
	}
	if err := a.deps.Docker.Ping(ctx); err != nil {
		return fail(err)
	}

	devRecord, artifact, err := a.dev.Build(ctx, rec.ID, sourceRoot, a.deps.Progress)
	if err != nil {
		return fail(err)
	}
	updated, err := a.deps.Registry.Update(ctx, rec.ID, rec.Revision, func(r *registry.Record) error {
		r.Mode = registry.ModeDev
		r.Artifact = artifact.Record.Digest
		r.ReleaseVersion = artifact.Record.Version
		return nil
	})
	if err != nil {
		return fail(err)
	}
	if err := a.writeDevRecord(devRecord); err != nil {
		return fail(err)
	}
	// The same shim handoff `vibe update` performs: repoint the host
	// `vibe` symlink at the fresh dev binary so the next invocation runs
	// it — no manual copy after every sync. The binary is host-global by
	// nature (artifact pins stay per-project); `dev off` hands back to
	// the newest release.
	binPath, err := a.installBinary(artifact)
	if err != nil {
		return fail(err)
	}
	a.bumpTuiSerial(ctx)
	return DevOnResult{Record: devRecord, Project: updated, Artifact: artifact.Record, BinaryPath: binPath}, nil
}

// DevOffRequest reverts the project to release mode and the newest
// release artifact.
type DevOffRequest struct {
	Dir string
}

type DevOffResult struct {
	Project    registry.Record
	BinaryPath string // "" when no release artifact existed to hand back to
}

func (a *App) DevOff(ctx context.Context, req DevOffRequest) (DevOffResult, error) {
	fail := opFail[DevOffResult]("dev off", "")
	_, rec, err := a.resolveProject(ctx, req.Dir)
	if err != nil {
		return fail(err)
	}
	fail = opFail[DevOffResult]("dev off", rec.ID)
	if rec.Mode != registry.ModeDev {
		return fail(fmt.Errorf("%w: project is not in dev mode", domain.ErrInvalid))
	}
	var release *store.ArtifactRecord
	if artifacts, err := a.deps.Store.ListArtifactRecords(); err == nil {
		for i := range artifacts {
			if artifacts[i].Release.Source != "dev-build" {
				release = &artifacts[i]
				break
			}
		}
	}

	// Hand the `vibe` symlink back to the release the project reverts to,
	// undoing dev on's handoff — and do it BEFORE the registry moves. The
	// binary is the durable thing the reverted record points at, so it
	// must be in place first: the package's pointer-moves-last discipline
	// (compare Up, where the approved candidate moves only after its
	// containers run). A failed handoff must therefore leave the project
	// in dev mode, not flip the record while the shim still runs the dev
	// binary. When no release artifact exists, that leg is best-effort and
	// legitimately leaves the current dev binary in place.
	result := DevOffResult{}
	if release != nil {
		lease, err := a.deps.Store.Open(ctx, store.ArtifactObject, release.Digest)
		if err != nil {
			return fail(err)
		}
		binPath, err := a.installBinary(store.Artifact{Record: *release, Path: lease.Object.Path})
		lease.Close()
		if err != nil {
			return fail(err)
		}
		result.BinaryPath = binPath
	}

	updated, err := a.deps.Registry.Update(ctx, rec.ID, rec.Revision, func(r *registry.Record) error {
		r.Mode = registry.ModeRelease
		if release != nil {
			r.Artifact = release.Digest
			r.ReleaseVersion = release.Version
		} else {
			r.Artifact = domain.Digest{}
			r.ReleaseVersion = ""
		}
		return nil
	})
	if err != nil {
		return fail(err)
	}
	result.Project = updated
	// The dev record is now stale. Removal is best-effort: a leftover
	// record is inert once the project is in release mode (nothing reads
	// it) and store GC reclaims it, so a failure here is safe to ignore.
	_ = os.Remove(a.devRecordPath(rec.ID))
	a.bumpTuiSerial(ctx)
	return result, nil
}

// DevStatusRequest reports the project's dev state.
type DevStatusRequest struct {
	Dir string
}

type DevStatusResult struct {
	Project registry.Record
	Record  *dev.Record
}

func (a *App) DevStatus(ctx context.Context, req DevStatusRequest) (DevStatusResult, error) {
	fail := opFail[DevStatusResult]("dev status", "")
	_, rec, err := a.resolveProject(ctx, req.Dir)
	if err != nil {
		return fail(err)
	}
	result := DevStatusResult{Project: rec}
	if data, err := os.ReadFile(a.devRecordPath(rec.ID)); err == nil {
		var devRec dev.Record
		if json.Unmarshal(data, &devRec) == nil {
			result.Record = &devRec
		}
	}
	return result, nil
}

func (a *App) devRecordPath(id domain.ProjectID) string {
	return filepath.Join(a.deps.Layout.State, "dev", string(id)+".json")
}

func (a *App) writeDevRecord(rec dev.Record) error {
	path := a.devRecordPath(rec.ProjectID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return store.WriteFileAtomic(path, append(data, '\n'), 0o600)
}

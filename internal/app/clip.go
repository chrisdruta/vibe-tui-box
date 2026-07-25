package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/runner"
)

// ClipRequest saves the host clipboard image for the project's agent.
// DestDir, when set, is a workspace-relative directory and the write
// goes through the bind mount (no daemon needed); empty streams into
// the running dev container's /tmp. Env is the curated host environment
// the CLI assembled — clipboard tooling and WSL interop live there, and
// the app never reads ambient environment itself.
type ClipRequest struct {
	Dir      string
	DestDir  string
	PathOnly bool
	Env      []string
	Stdout   io.Writer
	Stderr   io.Writer
}

type ClipResult struct {
	ExitCode int
}

// Clip runs the pinned artifact's clip-image.sh — the same hardened
// script behind the tui's prefix+v binding — as a first-class verb.
// The engine contributes project resolution and the dev-container
// name; every clipboard and path-containment decision stays in the
// script, which owns that logic for both entry points.
func (a *App) Clip(ctx context.Context, req ClipRequest) (ClipResult, error) {
	fail := opFail[ClipResult]("clip", "")
	root, rec, err := a.resolveProject(ctx, req.Dir)
	if err != nil {
		return fail(err)
	}
	fail = opFail[ClipResult]("clip", rec.ID)
	artifact, release, err := a.loadArtifact(ctx, rec)
	if err != nil {
		return fail(err)
	}
	defer release()
	if artifact.IsZero() {
		return fail(fmt.Errorf("%w: no pinned artifact (run `vibe provision`)", domain.ErrUnavailable))
	}
	script := filepath.Join(artifact.PayloadPath(), "host", "scripts", "clip-image.sh")
	if _, err := os.Stat(script); err != nil {
		return fail(fmt.Errorf("%w: artifact carries no clip script (run `vibe provision`)", domain.ErrUnavailable))
	}
	// DIR mode deliberately makes no Docker call: the bind mount is the
	// transport, so it works with the daemon down.
	container := ""
	if req.DestDir == "" {
		name, err := a.requireDevContainer(ctx, rec)
		if err != nil {
			return fail(err)
		}
		container = string(name)
	}
	args := []string{root.Path, req.DestDir, container}
	if req.PathOnly {
		args = append(args, "--path-only")
	}
	out, err := a.deps.Runner.Run(ctx, runner.Invocation{
		Path:   script,
		Args:   args,
		Env:    req.Env,
		Stdout: req.Stdout,
		Stderr: req.Stderr,
	})
	if err != nil {
		return fail(err)
	}
	return ClipResult{ExitCode: out.ExitCode}, nil
}

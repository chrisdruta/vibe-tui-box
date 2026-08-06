package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"

	"vibe/internal/dockerapi"
	"vibe/internal/domain"
	"vibe/internal/model"
	"vibe/internal/runner"
)

// ClipRequest saves the host clipboard image for the project's agent.
// DestDir, when set, is a workspace-relative directory and the write
// goes through the bind mount (no daemon needed); empty streams into
// the running dev container's /tmp. PathOnly marks a machine consumer
// (the tui's prefix+v flow) that wants no clipboard copy-back. Env is
// the curated host environment the CLI assembled — clipboard tooling
// and WSL interop live there, and the app never reads ambient
// environment itself.
type ClipRequest struct {
	Dir      string
	DestDir  string
	PathOnly bool
	Env      []string
}

// ClipResult reports where the image landed. SavedPath is the
// workspace-relative host copy ("" in /tmp mode); PathCopied reports
// the QoL clipboard copy-back.
type ClipResult struct {
	ContainerPath string
	SavedPath     string
	PathCopied    bool
}

// Clip captures the host clipboard through the pinned artifact's
// clip-image.sh — which is pure platform-clipboard glue (save/copy) —
// and places the image itself: workspace writes go through an os.Root
// on the registered project root (symlink-escape-proof, O_EXCL — the
// engine's containment discipline, not the script's), /tmp mode
// streams through the typed Docker exec. The script never touches the
// workspace and never runs docker.
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
	destDir := ""
	if req.DestDir != "" {
		destDir = path.Clean(filepath.ToSlash(req.DestDir))
		if destDir == "." || !filepath.IsLocal(destDir) {
			return fail(fmt.Errorf("%w: destination %q must be a relative directory inside the workspace", domain.ErrInvalid, req.DestDir))
		}
	}
	// /tmp mode needs the running container; fail before bothering the
	// clipboard. DIR mode deliberately makes no Docker call: the bind
	// mount is the transport, so it works with the daemon down.
	var container dockerapi.ContainerName
	if destDir == "" {
		container, err = a.requireDevContainer(ctx, rec)
		if err != nil {
			return fail(err)
		}
	}

	name := "clip-" + a.deps.Clock.Now().UTC().Format("20060102-150405") + ".png"
	tmp, err := os.MkdirTemp("", "vibe-clip-")
	if err != nil {
		return fail(err)
	}
	defer os.RemoveAll(tmp)
	hostPNG := filepath.Join(tmp, name)
	out, err := a.deps.Runner.Run(ctx, runner.Invocation{
		Path: script,
		Args: []string{"save", hostPNG},
		Env:  req.Env,
	})
	if err != nil {
		return fail(err)
	}
	if out.ExitCode != 0 {
		return fail(fmt.Errorf("clipboard capture: %s", lastLine(out.Stderr, "clip-image.sh failed")))
	}

	var res ClipResult
	if destDir != "" {
		rel := path.Join(destDir, name)
		if err := writeIntoWorkspace(root.Path, destDir, rel, hostPNG); err != nil {
			return fail(err)
		}
		res.SavedPath = rel
		res.ContainerPath = path.Join(model.WorkspaceTarget, rel)
	} else {
		res.ContainerPath = "/tmp/" + name
		png, err := os.Open(hostPNG)
		if err != nil {
			return fail(err)
		}
		defer png.Close()
		// tee is argv-only transport: bytes ride exec stdin, the target
		// path never crosses the boundary inside a shell string.
		execRes, err := a.deps.Docker.Exec(ctx, dockerapi.ExecRequest{
			Container: container,
			User:      devContainerUser,
			Argv:      []string{"tee", res.ContainerPath},
			Stdin:     true,
			Streams:   dockerapi.Streams{In: png, Out: io.Discard, Err: io.Discard},
		})
		if err != nil {
			return fail(err)
		}
		if execRes.ExitCode != 0 {
			return fail(fmt.Errorf("stream into %s failed (exit %d)", container, execRes.ExitCode))
		}
	}

	// QoL for the interactive flow only: the path replaces the image on
	// the host clipboard so the next paste is the path itself.
	// Best-effort — no clipboard tool is not an error.
	if !req.PathOnly {
		if out, err := a.deps.Runner.Run(ctx, runner.Invocation{
			Path: script,
			Args: []string{"copy", res.ContainerPath},
			Env:  req.Env,
		}); err == nil && out.ExitCode == 0 {
			res.PathCopied = true
		}
	}
	return res, nil
}

// writeIntoWorkspace copies src to rel beneath the registered root
// through an os.Root: path components cannot escape the workspace even
// through pre-planted symlinks, and the file itself is created O_EXCL.
func writeIntoWorkspace(rootPath, destDir, rel, src string) error {
	ws, err := os.OpenRoot(rootPath)
	if err != nil {
		return err
	}
	defer ws.Close()
	if err := ws.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("%w: destination %q is not usable inside the workspace: %v", domain.ErrInvalid, destDir, err)
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := ws.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("%w: cannot create %q in the workspace: %v", domain.ErrInvalid, rel, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// lastLine extracts the final non-empty line of captured output for a
// one-line diagnostic, falling back when there is none.
func lastLine(out []byte, fallback string) string {
	lines := bytes.Split(bytes.TrimRight(out, "\n"), []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		if len(lines[i]) > 0 {
			return string(lines[i])
		}
	}
	return fallback
}

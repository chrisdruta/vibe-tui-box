package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"vibe/internal/domain"
	"vibe/internal/paths"
	"vibe/internal/tmuxui"
)

// ChooserRequest drives the hidden `vibe _chooser` agents-chooser
// renderer (docs/tui-layout.md "Launch surfaces"). Input carries the
// choosing session's window porcelain (the engine never talks to the
// UI server's tmux itself); CacheDir is the engine-truth cache beside
// the tmux socket, "" when the cache layer is unavailable — entries
// then degrade to launch verdicts, which -A semantics keep safe.
type ChooserRequest struct {
	Input    io.Reader
	CacheDir string
	Project  domain.ProjectID
}

// maxChooserInput bounds the porcelain read; one session's window
// listing is bytes, so anything beyond this is a runaway feeder.
const maxChooserInput = 1 << 20

// RenderChooser renders the agents-chooser menu items: one entry per
// installed CLI (image.agents, manifest default first), each entry's
// reach-vs-launch verdict joined from the `vibe ps` fetch cache — the
// same truth the tray's ghost cells read, so the chooser and the tray
// cannot disagree. Cache-only and manifest-only by contract: no
// Docker, a menu opens instantly.
func (a *App) RenderChooser(ctx context.Context, req ChooserRequest) (RenderResult, error) {
	fail := opFail[RenderResult]("_chooser", req.Project)
	if req.Input == nil {
		return fail(fmt.Errorf("%w: chooser input is required", domain.ErrInvalid))
	}
	if req.Project == "" {
		return fail(fmt.Errorf("%w: --project is required", domain.ErrInvalid))
	}
	records, err := a.deps.Registry.List(ctx)
	if err != nil {
		return fail(err)
	}
	idx := -1
	for i, rec := range records {
		if rec.ID == req.Project {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fail(fmt.Errorf("%w: project %q is not registered", domain.ErrNotFound, req.Project))
	}
	doc, err := loadManifestFile(filepath.Join(records[idx].Root, paths.ManifestRelPath))
	if err != nil {
		return fail(err)
	}
	if ferrs := doc.Validate(); len(ferrs) > 0 {
		return fail(fieldErrs(ferrs))
	}

	in := tmuxui.ChooserInput{Default: string(doc.Manifest.Agent.Cmd)}
	for _, s := range doc.Manifest.Image.Agents {
		in.Kinds = append(in.Kinds, string(s.Kind))
	}
	data, err := io.ReadAll(io.LimitReader(req.Input, maxChooserInput))
	if err != nil {
		return fail(err)
	}
	in.Windows = tmuxui.ParseChooserWindows(string(data))
	if req.CacheDir != "" {
		if agents, err := os.ReadFile(filepath.Join(req.CacheDir, "agents")); err == nil {
			for _, ag := range tmuxui.ParseAgents(strings.Split(string(agents), "\n")) {
				// Workspace services and engine sidecars never launch
				// and never reattach — the chooser is an agent surface
				// only, so only kindless (agent) rows pass.
				if ag.Project == string(req.Project) && ag.Kind == "" {
					in.Agents = append(in.Agents, ag)
				}
			}
		}
	}
	return RenderResult{Lines: tmuxui.Chooser(in)}, nil
}

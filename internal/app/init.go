package app

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/chrisdruta/vibe-tui-box/internal/dockerapi"
	"github.com/chrisdruta/vibe-tui-box/internal/doctor"
	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/initproject"
	"github.com/chrisdruta/vibe-tui-box/internal/paths"
	"github.com/chrisdruta/vibe-tui-box/internal/payload"
	"github.com/chrisdruta/vibe-tui-box/internal/registry"
	"github.com/chrisdruta/vibe-tui-box/internal/schema"
)

// InitRequest seeds a new project: render the preset into .vibe/,
// register the project, and pin the newest installed artifact.
type InitRequest struct {
	Dir    string
	Preset string
}

type InitResult struct {
	Record  registry.Record
	Created []string
	Preset  string
}

func (a *App) Init(ctx context.Context, req InitRequest) (InitResult, error) {
	if a.deps.Payload == nil {
		return InitResult{}, &domain.OpError{Op: "init",
			Err: fmt.Errorf("%w: this build embeds no presets", domain.ErrUnavailable)}
	}
	// Refuse to nest inside an existing project.
	if existing, err := paths.Discover(req.Dir); err == nil {
		return InitResult{}, &domain.OpError{Op: "init",
			Err: fmt.Errorf("%w: already inside project %s", domain.ErrConflict, existing.Path)}
	}

	presetName := req.Preset
	if presetName == "" {
		presetName = "minimal"
	}
	if presetName == payload.CommonPreset {
		return InitResult{}, &domain.OpError{Op: "init",
			Err: fmt.Errorf("%w: %q is the shared overlay, not a preset", domain.ErrInvalid, presetName)}
	}
	preset, err := a.deps.Payload.Preset(presetName)
	if err != nil {
		return InitResult{}, &domain.OpError{Op: "init", Err: err}
	}
	// Every preset renders on top of the shared overlay (AGENTS.md, hook
	// samples); the preset's own files win on collision.
	if common, err := a.deps.Payload.Preset(payload.CommonPreset); err == nil {
		for name, content := range common.Files {
			if _, ok := preset.Files[name]; !ok {
				preset.Files[name] = content
			}
		}
	}

	abs, err := filepath.Abs(req.Dir)
	if err != nil {
		return InitResult{}, &domain.OpError{Op: "init", Err: err}
	}
	harness := a.deps.Version.Release
	files, err := initproject.Render(preset, initproject.TemplateData{
		ProjectName:    filepath.Base(abs),
		HarnessVersion: normalizeHarnessVersion(harness),
	})
	if err != nil {
		return InitResult{}, &domain.OpError{Op: "init", Err: err}
	}
	created, err := initproject.Materialize(abs, files)
	if err != nil {
		return InitResult{}, &domain.OpError{Op: "init", Err: err}
	}

	// From here the .vibe tree exists; failures leave it for inspection
	// and the error names the recovery command.
	root, err := paths.Discover(abs)
	if err != nil {
		return InitResult{}, initRecoveryErr(err)
	}
	newRec := registry.NewRecord{
		Root:        root,
		DisplayName: filepath.Base(root.Path),
		Mode:        registry.ModeRelease,
	}
	if artifacts, err := a.deps.Store.ListArtifactRecords(); err == nil && len(artifacts) > 0 {
		newRec.Artifact = artifacts[0].Digest
		newRec.ReleaseVersion = artifacts[0].Version
	}
	rec, err := a.deps.Registry.Create(ctx, newRec)
	if err != nil {
		return InitResult{}, initRecoveryErr(err)
	}
	return InitResult{Record: rec, Created: created, Preset: presetName}, nil
}

func initRecoveryErr(err error) error {
	return &domain.OpError{Op: "init",
		Err: fmt.Errorf("%w (the rendered .vibe/ was kept; fix the cause and run `vibe register`)", err)}
}

// normalizeHarnessVersion maps development builds onto a valid manifest
// version string; anything the schema would reject becomes v0.0.0.
func normalizeHarnessVersion(v string) string {
	if schema.ValidHarnessVersion(v) {
		return v
	}
	return "v0.0.0"
}

// DoctorRequest runs the health suite for the current directory.
type DoctorRequest struct {
	Dir string
}

type DoctorResult struct {
	Results []doctor.Result
	Healthy bool
}

func (a *App) Doctor(ctx context.Context, req DoctorRequest) (DoctorResult, error) {
	input := &doctor.Input{
		Layout:   a.deps.Layout,
		Store:    a.deps.Store,
		Docker:   a.deps.Docker,
		TmuxPath: a.deps.Executables.Tmux,
	}
	if root, err := paths.Discover(req.Dir); err == nil {
		input.Root = &root
		if rec, err := a.deps.Registry.Resolve(ctx, root); err == nil {
			input.Record = &rec
		}
	}
	results := doctor.Run(ctx, input, doctor.DefaultChecks())
	return DoctorResult{Results: results, Healthy: doctor.Healthy(results)}, nil
}

// BootstrapRequest verifies the manifest's required tools exist inside
// the running dev container.
type BootstrapRequest struct {
	Dir string
}

type ToolStatus struct {
	Tool    string `json:"tool"`
	Present bool   `json:"present"`
}

type BootstrapResult struct {
	Tools   []ToolStatus
	Missing int
}

func (a *App) Bootstrap(ctx context.Context, req BootstrapRequest) (BootstrapResult, error) {
	root, rec, err := a.resolveProject(ctx, req.Dir)
	if err != nil {
		return BootstrapResult{}, &domain.OpError{Op: "bootstrap", Err: err}
	}
	doc, err := loadManifestFile(filepath.Join(root.Path, paths.ManifestRelPath))
	if err != nil {
		return BootstrapResult{}, &domain.OpError{Op: "bootstrap", Project: rec.ID, Err: err}
	}
	if ferrs := doc.Validate(); len(ferrs) > 0 {
		return BootstrapResult{}, &domain.OpError{Op: "bootstrap", Project: rec.ID, Err: fieldErrs(ferrs)}
	}
	_, name, err := a.devContainer(ctx, req.Dir)
	if err != nil {
		return BootstrapResult{}, &domain.OpError{Op: "bootstrap", Err: err}
	}

	var result BootstrapResult
	for _, tool := range doc.Manifest.Bootstrap.Required {
		probe, err := a.deps.Docker.Exec(ctx, dockerapi.ExecRequest{
			Container: name,
			User:      devContainerUser,
			Argv:      []string{"which", tool},
		})
		if err != nil {
			return BootstrapResult{}, &domain.OpError{Op: "bootstrap", Project: rec.ID, Err: err}
		}
		present := probe.ExitCode == 0
		if !present {
			result.Missing++
		}
		result.Tools = append(result.Tools, ToolStatus{Tool: tool, Present: present})
	}
	return result, nil
}

// Presets lists the embedded preset names.
func (a *App) Presets() []string {
	if a.deps.Payload == nil {
		return nil
	}
	return a.deps.Payload.Names()
}

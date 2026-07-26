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
	"github.com/chrisdruta/vibe-tui-box/internal/terminal"
)

// InitRequest seeds a new project: render the preset into .vibe/,
// register the project, and pin the newest installed artifact.
// AutoMemory is the explicit --auto-memory choice (nil = not given);
// Interactive marks a TTY session where the missing choice may be
// asked instead of defaulted.
type InitRequest struct {
	Dir         string
	Preset      string
	AutoMemory  *bool
	Interactive bool
}

type InitResult struct {
	Record  registry.Record
	Created []string
	Preset  string
	Memory  schema.MemoryMode
}

func (a *App) Init(ctx context.Context, req InitRequest) (InitResult, error) {
	fail := opFail[InitResult]("init", "")
	if a.deps.Payload == nil {
		return fail(fmt.Errorf("%w: this build embeds no presets", domain.ErrUnavailable))
	}
	// Refuse to nest inside an existing project.
	if existing, err := paths.Discover(req.Dir); err == nil {
		return fail(fmt.Errorf("%w: already inside project %s", domain.ErrConflict, existing.Path))
	}

	presetName := req.Preset
	if presetName == "" {
		presetName = "minimal"
	}
	if presetName == payload.CommonPreset {
		return fail(fmt.Errorf("%w: %q is the shared overlay, not a preset", domain.ErrInvalid, presetName))
	}
	preset, err := a.deps.Payload.Preset(presetName)
	if err != nil {
		return fail(err)
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
		return fail(err)
	}

	// Resolution order: flag > question > off. The question runs only on
	// an interactive session with a prompt wired — scripted and --json
	// runs get the hardened default without blocking on stdin.
	memory := schema.MemoryOff
	switch {
	case req.AutoMemory != nil:
		if *req.AutoMemory {
			memory = schema.MemoryAuto
		}
	case req.Interactive && a.deps.Prompt != nil:
		ok, err := a.deps.Prompt.Confirm(ctx, terminal.Confirmation{
			Title:    "auto memory: Claude keeps cross-session project memory on the agent-state",
			Chrome:   []string{"volume, surviving rebuilds (agent.memory in .vibe/vibe.yaml flips it later)"},
			Question: "enable auto memory?",
		})
		if err != nil {
			return fail(err)
		}
		if ok {
			memory = schema.MemoryAuto
		}
	}

	harness := a.deps.Version.Release
	files, err := initproject.Render(preset, initproject.TemplateData{
		ProjectName:    filepath.Base(abs),
		HarnessVersion: normalizeHarnessVersion(harness),
		AutoMemory:     string(memory),
	})
	if err != nil {
		return fail(err)
	}
	created, err := initproject.Materialize(abs, files)
	if err != nil {
		return fail(err)
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
	// Release-mode projects pin release artifacts only — on a dogfood
	// host the newest artifact is often a dev build, which only `vibe
	// dev on` may pin (same filter as DevOff's revert). No release
	// artifact means no pin: `vibe up` then names provision/update as
	// the fix instead of silently running unreleased engine payload.
	if artifacts, err := a.deps.Store.ListArtifactRecords(); err == nil {
		for i := range artifacts {
			if artifacts[i].Release.Source == "dev-build" {
				continue
			}
			newRec.Artifact = artifacts[i].Digest
			newRec.ReleaseVersion = artifacts[i].Version
			break
		}
	}
	rec, err := a.deps.Registry.Create(ctx, newRec)
	if err != nil {
		return InitResult{}, initRecoveryErr(err)
	}
	return InitResult{Record: rec, Created: created, Preset: presetName, Memory: memory}, nil
}

func initRecoveryErr(err error) error {
	return opError("init", "", fmt.Errorf("%w (the rendered .vibe/ was kept; fix the cause and run `vibe register`)", err))
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
	fail := opFail[BootstrapResult]("bootstrap", "")
	root, rec, err := a.resolveProject(ctx, req.Dir)
	if err != nil {
		return fail(err)
	}
	fail = opFail[BootstrapResult]("bootstrap", rec.ID)
	doc, err := loadManifestFile(filepath.Join(root.Path, paths.ManifestRelPath))
	if err != nil {
		return fail(err)
	}
	if ferrs := doc.Validate(); len(ferrs) > 0 {
		return fail(fieldErrs(ferrs))
	}
	name, err := a.requireDevContainer(ctx, rec)
	if err != nil {
		return fail(err)
	}

	var result BootstrapResult
	for _, tool := range doc.Manifest.Bootstrap.Required {
		probe, err := a.deps.Docker.Exec(ctx, dockerapi.ExecRequest{
			Container: name,
			User:      devContainerUser,
			Argv:      []string{"which", tool},
		})
		if err != nil {
			return fail(err)
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

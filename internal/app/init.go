package app

import (
	"context"
	"fmt"
	"path/filepath"

	"vibe/internal/dockerapi"
	"vibe/internal/doctor"
	"vibe/internal/domain"
	"vibe/internal/initproject"
	"vibe/internal/lock"
	"vibe/internal/paths"
	"vibe/internal/payload"
	"vibe/internal/registry"
	"vibe/internal/schema"
	"vibe/internal/terminal"
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
	// RootSkipped lists root briefing files (AGENTS.md, CLAUDE.md) that
	// already existed and were left untouched — the operator wires the
	// .vibe/AGENTS.md pointer into those themselves.
	RootSkipped []string
	Preset      string
	Memory      schema.MemoryMode
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

	files, err := initproject.Render(preset, initproject.TemplateData{
		ProjectName: filepath.Base(abs),
		AutoMemory:  string(memory),
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
	rootCreated, rootSkipped, err := initproject.SeedRootBriefing(abs)
	if err != nil {
		return InitResult{}, initRecoveryErr(err)
	}
	created = append(created, rootCreated...)
	root, err := paths.Discover(abs)
	if err != nil {
		return InitResult{}, initRecoveryErr(err)
	}
	newRec := registry.NewRecord{
		Root:        root,
		DisplayName: filepath.Base(root.Path),
		Mode:        registry.ModeRelease,
	}
	// Shared store-global hold from artifact selection through the
	// registry write: the selected artifact is unrooted until the
	// record pinning it exists, and an exclusive GC in that window
	// could otherwise collect it.
	sharedStore, err := a.deps.Locks.AcquireShared(ctx, lock.StoreGlobal())
	if err != nil {
		return InitResult{}, initRecoveryErr(err)
	}
	defer sharedStore.Release()
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
	return InitResult{Record: rec, Created: created, RootSkipped: rootSkipped, Preset: presetName, Memory: memory}, nil
}

func initRecoveryErr(err error) error {
	return opError("init", "", fmt.Errorf("%w (the rendered .vibe/ was kept; fix the cause and run `vibe register`)", err))
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
	if input.TmuxPath != "" && a.deps.Tmux != nil {
		// Best-effort: an unknown version just mutes the sixel grading.
		if v, err := a.deps.Tmux.Version(ctx); err == nil {
			input.TmuxVersion = v
		}
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

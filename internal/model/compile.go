package model

import (
	"fmt"
	"path"
	"sort"
	"strconv"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/registry"
	"github.com/chrisdruta/vibe-tui-box/internal/schema"
	"github.com/chrisdruta/vibe-tui-box/internal/snapshot"
	"github.com/chrisdruta/vibe-tui-box/internal/store"
)

// Fixed locations inside a candidate input snapshot.
const (
	// SnapshotImportDir holds copied imports: imports/<index>/... in
	// manifest order.
	SnapshotImportDir = "imports"
	// SnapshotManifestPath is the frozen project manifest.
	SnapshotManifestPath = "vibe.yaml"
	// SnapshotEnvFilePath is the frozen env file when the manifest
	// declares one.
	SnapshotEnvFilePath = "env-file"
	// SnapshotDockerfilePath and SnapshotBuildDir hold the frozen
	// extension build inputs when image.extension is enabled.
	SnapshotDockerfilePath = "Dockerfile"
	SnapshotBuildDir       = "build"
)

// CompileInput carries only resolved inputs; compilation never reads
// the workspace or the network.
type CompileInput struct {
	Project      registry.Record
	Artifact     store.Artifact
	Manifest     schema.Manifest
	Snapshot     snapshot.Result
	ImageDigests map[string]domain.Digest
	// BrokerResultsDir, when set, is mounted read-only at ResultsTarget
	// so the container can read request results.
	BrokerResultsDir string
}

// Compile turns a validated manifest plus frozen inputs into the
// canonical plan. It generates names, labels, required mounts, volumes,
// and defaults, then computes the canonical hash. Field errors mean the
// manifest asked for something the runtime model refuses.
func Compile(in CompileInput) (Plan, []domain.FieldError) {
	var errs []domain.FieldError
	m := &in.Manifest
	id := in.Project.ID

	plan := Plan{
		Format: PlanFormat,
		Project: Project{
			ID:          id,
			Root:        in.Project.Root,
			DisplayName: in.Project.DisplayName,
		},
		Artifact: Artifact{
			Digest:  in.Artifact.Record.Digest,
			Version: in.Artifact.Record.Version,
		},
		Networks: []Network{{Name: NetworkName(id)}},
		Volumes:  []Volume{{Name: AgentStateVolumeName(id)}},
		Inputs: Inputs{
			Snapshot: in.Snapshot.Digest,
			EnvFile:  m.EnvFile,
		},
	}

	resolve := func(ref string) ImageID {
		return ImageID{Ref: ref, Digest: in.ImageDigests[ref]}
	}

	// Dev container image: extension over tools over base — each build
	// stage, when enabled, becomes the next stage's FROM.
	devImage := resolve(m.Image.Base)
	hasTools := len(m.Image.Agents) > 0 || len(m.Image.Toolchains) > 0
	if hasTools {
		agents := make([]string, 0, len(m.Image.Agents))
		for _, a := range m.Image.Agents {
			// The spec's string form ("claude", "claude@2.1.220") is the
			// canonical-plan form: a pin change is a plan change, an
			// unversioned entry stays byte-identical to the pre-version
			// output.
			agents = append(agents, a.String())
		}
		sort.Strings(agents)
		devImage = resolve(ToolsImageRef(id))
		plan.Tools = &Tools{
			Agents:     agents,
			Toolchains: sortedStrings(m.Image.Toolchains),
		}
	}
	if m.Image.Extension {
		devImage = resolve(ExtensionImageRef(id))
		plan.Extension = &Extension{Dockerfile: "Dockerfile"}
	}
	dev := Container{
		Name:  DevContainerName(id),
		Image: devImage,
		User:  "vscode",
		// Without an artifact there is no payload entrypoint; keep the
		// container alive on the image's own sleep.
		Command: []string{"sleep", "infinity"},
		Policy:  defaultPolicy(),
		Mounts: []Mount{
			{Kind: BindMount, Source: in.Project.Root, Target: WorkspaceTarget},
			{Kind: VolumeMount, Source: AgentStateVolumeName(id), Target: AgentStateTarget},
			{Kind: TmpfsMount, Target: RuntimeDirTarget},
		},
	}
	if !in.Artifact.IsZero() {
		dev.Command = []string{PayloadEntrypoint}
		dev.Mounts = append(dev.Mounts, Mount{
			Kind: BindMount, Source: in.Artifact.PayloadPath(), Target: PayloadTarget, ReadOnly: true,
		})
	}
	if in.BrokerResultsDir != "" {
		dev.Mounts = append(dev.Mounts, Mount{
			Kind: BindMount, Source: in.BrokerResultsDir, Target: ResultsTarget, ReadOnly: true,
		})
	}
	for i, imp := range m.Runtime.Imports {
		// Engine-owned targets stay engine-owned even when the mount is
		// absent from this compile (no artifact installed yet, no broker
		// results); rejecting here keeps the error at the manifest field
		// instead of a later mount-overlap surprise on `up`.
		if r := reservedTargetFor(imp.Target); r != "" {
			errs = append(errs, domain.FieldError{
				Path:    fmt.Sprintf("runtime.imports[%d].target", i),
				Message: fmt.Sprintf("target %q overlaps the engine-owned target %q", imp.Target, r),
			})
			continue
		}
		dev.Mounts = append(dev.Mounts, Mount{
			Kind:     BindMount,
			Source:   path.Join(in.Snapshot.Path, SnapshotImportDir, strconv.Itoa(i)),
			Target:   imp.Target,
			ReadOnly: imp.Readonly,
		})
	}
	dev.Ports, errs = compilePorts("runtime.ports", m.Runtime.Ports, errs)
	dev.Environment = compileEnv(m.Runtime.Env)
	// Engine-provided: points spec-following tools (state-dir.sh first
	// among them) at the runtime tmpfs mounted above. Comes after user
	// env, cannot be shadowed.
	dev.Environment = append(dev.Environment,
		Env{Key: "XDG_RUNTIME_DIR", Value: RuntimeDirTarget})
	// gh logins relocate onto the agent-state volume like the agent
	// CLIs' state: a per-project fine-grained token pasted into
	// `gh auth login` survives rebuilds and stays compartmentalized per
	// project (docs/configuration.md "GitHub access").
	dev.Environment = append(dev.Environment,
		Env{Key: "GH_CONFIG_DIR", Value: path.Join(AgentStateTarget, "gh")})
	for _, agent := range m.Image.Agents {
		// Claude relocates all its state (including .claude.json) under
		// CLAUDE_CONFIG_DIR, so logins land in the agent-state volume and
		// survive rebuilds. Engine-provided: comes last, cannot be shadowed.
		if agent.Kind == schema.AgentClaude {
			// DISABLE_AUTOUPDATER pins the running claude to the image's
			// frozen install: self-updates would land in the container's
			// writable layer and silently revert on the next replace, so the
			// version moves only when a rebuild re-pulls the layer (or the
			// manifest pin changes).
			dev.Environment = append(dev.Environment,
				Env{Key: "CLAUDE_CONFIG_DIR", Value: path.Join(AgentStateTarget, "claude")},
				Env{Key: "DISABLE_AUTOUPDATER", Value: "1"})
		}
		// Codex keeps auth.json and config under CODEX_HOME; the same
		// relocation keeps `codex login` alive across rebuilds (the
		// agent-state docs promise it for every agent, not just Claude).
		if agent.Kind == schema.AgentCodex {
			dev.Environment = append(dev.Environment,
				Env{Key: "CODEX_HOME", Value: path.Join(AgentStateTarget, "codex")})
		}
	}
	if !in.Artifact.IsZero() {
		// Engine-provided entries come last so they cannot be shadowed.
		dev.Environment = append(dev.Environment,
			Env{Key: "VIBE_PAYLOAD_DIGEST", Value: in.Artifact.Record.PayloadDigest.String()})
	}
	dev.Labels = commonLabels(id, "dev")
	plan.Dev = dev

	// Sidecars in deterministic name order.
	for _, name := range m.ServiceNames() {
		svc := m.Services[name]
		c := Container{
			Name:   SidecarContainerName(id, name),
			Image:  resolve(svc.Image),
			Policy: defaultPolicy(),
			Labels: commonLabels(id, "sidecar:"+name),
		}
		c.Ports, errs = compilePorts("services."+name+".ports", svc.Ports, errs)
		c.Environment = compileEnv(svc.Env)
		for _, vol := range svc.Volumes {
			volName := ServiceVolumeName(id, vol.Name)
			c.Mounts = append(c.Mounts, Mount{Kind: VolumeMount, Source: volName, Target: vol.Target})
			plan.Volumes = append(plan.Volumes, Volume{Name: volName})
		}
		plan.Services = append(plan.Services, c)
	}

	// Image inventory: unique references sorted for determinism.
	refs := map[string]bool{devImage.Ref: true}
	for _, c := range plan.Services {
		refs[c.Image.Ref] = true
	}
	if hasTools || m.Image.Extension {
		refs[m.Image.Base] = true // derived builds still pull the base
	}
	if hasTools && m.Image.Extension {
		refs[ToolsImageRef(id)] = true // extension builds FROM the tools image
	}
	for ref := range refs {
		plan.Images = append(plan.Images, ImageID{Ref: ref, Digest: in.ImageDigests[ref]})
	}
	sort.Slice(plan.Images, func(i, j int) bool { return plan.Images[i].Ref < plan.Images[j].Ref })
	sort.Slice(plan.Volumes, func(i, j int) bool { return plan.Volumes[i].Name < plan.Volumes[j].Name })

	if verrs := Validate(plan); len(verrs) > 0 {
		errs = append(errs, verrs...)
	}
	if len(errs) > 0 {
		return Plan{}, errs
	}

	hash, err := Hash(plan)
	if err != nil {
		return Plan{}, []domain.FieldError{{Message: fmt.Sprintf("canonicalize plan: %v", err)}}
	}
	plan.CanonicalHash = hash
	return plan, nil
}

// sortedStrings canonicalizes a manifest-ordered enum list for the
// deterministic plan.
func sortedStrings[T ~string](in []T) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		out = append(out, string(v))
	}
	sort.Strings(out)
	return out
}

func defaultPolicy() ContainerPolicy {
	return ContainerPolicy{
		DropAllCapabilities: true,
		NoNewPrivileges:     true,
		Network:             NetworkProject,
	}
}

func compilePorts(fieldBase string, ports []schema.Port, errs []domain.FieldError) ([]PortBinding, []domain.FieldError) {
	var out []PortBinding
	for i, p := range ports {
		parsed, err := schema.ParsePort(string(p))
		if err != nil {
			errs = append(errs, domain.FieldError{
				Path:    fmt.Sprintf("%s[%d]", fieldBase, i),
				Message: err.Error(),
			})
			continue
		}
		out = append(out, PortBinding(parsed))
	}
	return out, errs
}

func compileEnv(env schema.OrderedEnv) []Env {
	out := make([]Env, 0, len(env))
	for _, e := range env {
		out = append(out, Env{Key: e.Key, Value: e.Value})
	}
	return out
}

func commonLabels(id domain.ProjectID, role string) []Label {
	return []Label{
		{Key: "dev.vibe.managed", Value: "true"},
		{Key: "dev.vibe.project", Value: string(id)},
		{Key: "dev.vibe.role", Value: role},
	}
}

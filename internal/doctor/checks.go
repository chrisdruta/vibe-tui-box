package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chrisdruta/vibe-tui-box/internal/dockerapi"
	"github.com/chrisdruta/vibe-tui-box/internal/model"
	"github.com/chrisdruta/vibe-tui-box/internal/payload"
	"github.com/chrisdruta/vibe-tui-box/internal/store"
)

// DefaultChecks is the standard suite in report order.
func DefaultChecks() []Check {
	return []Check{
		checkFunc{"layout", checkLayout},
		checkFunc{"project", checkProject},
		checkFunc{"artifact", checkArtifact},
		checkFunc{"docker", checkDocker},
		checkFunc{"containers", checkContainers},
		checkFunc{"lifecycle", checkLifecycle},
		checkFunc{"tmux", checkTmux},
	}
}

func checkLayout(ctx context.Context, in *Input) Result {
	if err := in.Layout.Validate(); err != nil {
		return Result{Status: Fail, Summary: err.Error()}
	}
	for _, dir := range []string{in.Layout.State, in.Layout.Staging, in.Layout.Projects} {
		probe, err := os.CreateTemp(dir, ".doctor-*")
		if err != nil {
			return Result{Status: Fail, Summary: fmt.Sprintf("%s is not writable", dir), Details: []string{err.Error()}}
		}
		probe.Close()
		os.Remove(probe.Name())
	}
	return Result{Status: OK, Summary: in.Layout.Root + " is writable"}
}

func checkProject(ctx context.Context, in *Input) Result {
	if in.Root == nil {
		return Result{Status: Skip, Summary: "not inside a project (no .vibe/vibe.yaml)"}
	}
	if in.Record == nil {
		return Result{Status: Fail, Summary: fmt.Sprintf("%s is not registered (run `vibe register`)", in.Root.Path)}
	}
	if in.Record.RootIdentity != in.Root.Identity {
		return Result{Status: Fail, Summary: "project root identity does not match its registration"}
	}
	return Result{Status: OK, Summary: fmt.Sprintf("%s registered as %q", in.Root.Path, in.Record.DisplayName)}
}

func checkArtifact(ctx context.Context, in *Input) Result {
	if in.Record == nil {
		return Result{Status: Skip, Summary: "no registered project"}
	}
	if in.Record.Artifact.IsZero() {
		return Result{Status: Warn, Summary: "no artifact pinned (run `vibe provision` or `vibe update`)"}
	}
	rec, err := in.Store.ReadArtifactRecord(in.Record.Artifact)
	if err != nil {
		return Result{Status: Fail, Summary: "pinned artifact record unreadable", Details: []string{err.Error()}}
	}
	lease, err := in.Store.Open(ctx, store.ArtifactObject, in.Record.Artifact)
	if err != nil {
		return Result{Status: Fail, Summary: "pinned artifact object missing", Details: []string{err.Error()}}
	}
	defer lease.Close()
	manifestData, err := os.ReadFile(filepath.Join(lease.Object.Path, store.ArtifactPayloadRelPath, payload.ManifestPath))
	if err != nil {
		return Result{Status: Fail, Summary: "artifact payload manifest missing", Details: []string{err.Error()}}
	}
	manifest, err := payload.ParseManifest(manifestData)
	if err != nil {
		return Result{Status: Fail, Summary: "artifact payload manifest invalid", Details: []string{err.Error()}}
	}
	if manifest.Digest != rec.PayloadDigest {
		return Result{Status: Fail, Summary: "artifact payload digest does not match its record"}
	}
	return Result{Status: OK, Summary: fmt.Sprintf("artifact %s (version %s)", shortDigest(rec.Digest.Hex()), rec.Version)}
}

func checkDocker(ctx context.Context, in *Input) Result {
	if err := in.Docker.Ping(ctx); err != nil {
		return Result{Status: Fail, Summary: "docker daemon unreachable", Details: []string{err.Error()}}
	}
	return Result{Status: OK, Summary: "docker daemon reachable"}
}

func checkContainers(ctx context.Context, in *Input) Result {
	if in.Record == nil {
		return Result{Status: Skip, Summary: "no registered project"}
	}
	if in.Docker.Ping(ctx) != nil {
		return Result{Status: Skip, Summary: "docker unreachable"}
	}
	containers, err := in.Docker.ListProjectContainers(ctx, in.Record.ID)
	if err != nil {
		return Result{Status: Fail, Summary: "cannot list project containers", Details: []string{err.Error()}}
	}
	if len(containers) == 0 {
		return Result{Status: Warn, Summary: "no containers (run `vibe up`)"}
	}
	var details []string
	status := OK
	for _, c := range containers {
		state := "running"
		if !c.Running {
			state = "stopped"
			status = Warn
		}
		if in.Record.Approved != nil && c.Labels["dev.vibe.candidate"] != in.Record.Approved.String() {
			state += ", stale candidate"
			status = Warn
		}
		details = append(details, fmt.Sprintf("%s: %s", c.Name, state))
	}
	return Result{Status: status, Summary: fmt.Sprintf("%d containers", len(containers)), Details: details}
}

func checkLifecycle(ctx context.Context, in *Input) Result {
	if in.Record == nil {
		return Result{Status: Skip, Summary: "no registered project"}
	}
	if in.Record.Artifact.IsZero() {
		return Result{Status: Skip, Summary: "no artifact pinned; lifecycle entrypoint not in use"}
	}
	if in.Docker.Ping(ctx) != nil {
		return Result{Status: Skip, Summary: "docker unreachable"}
	}
	name := dockerapi.ContainerName(model.DevContainerName(in.Record.ID))
	state, err := in.Docker.InspectContainer(ctx, name)
	if err != nil || !state.Running {
		return Result{Status: Skip, Summary: "dev container not running"}
	}
	res, err := in.Docker.Exec(ctx, dockerapi.ExecRequest{
		Container: name,
		Argv:      []string{"cat", "/tmp/vibe-ready"},
	})
	if err != nil {
		return Result{Status: Fail, Summary: "cannot probe lifecycle marker", Details: []string{err.Error()}}
	}
	if res.ExitCode != 0 {
		return Result{Status: Fail, Summary: "lifecycle marker /tmp/vibe-ready missing"}
	}
	return Result{Status: OK, Summary: "lifecycle marker present"}
}

func checkTmux(ctx context.Context, in *Input) Result {
	if in.TmuxPath == "" {
		return Result{Status: Warn, Summary: "tmux not found on PATH; `vibe tui` will be unavailable"}
	}
	return Result{Status: OK, Summary: in.TmuxPath}
}

func shortDigest(hex string) string {
	if len(hex) > 12 {
		return hex[:12]
	}
	return hex
}

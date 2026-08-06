package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"vibe/internal/dockerapi"
	"vibe/internal/model"
	"vibe/internal/payload"
	"vibe/internal/runtime"
	"vibe/internal/store"
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
		if in.Record.Approved != nil && c.Labels[runtime.CandidateLabel] != in.Record.Approved.String() {
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

// checkTmux also grades the preview-window sixel floor: images render
// full-fidelity only on tmux >= 3.7 (older servers drop the raster on
// adjacent-pane redraws — upstream, fixed by 3.7), so a lower version
// warns that previews degrade to low-fi. Degradation is never a Fail:
// open-path.sh gates the format at click time and show-image.sh says
// so on screen; this is the same message, one screen earlier.
func checkTmux(ctx context.Context, in *Input) Result {
	if in.TmuxPath == "" {
		return Result{Status: Warn, Summary: "tmux not found on PATH; `vibe tui` will be unavailable"}
	}
	maj, min, ok := parseTmuxVersion(in.TmuxVersion)
	switch {
	case !ok:
		return Result{Status: OK, Summary: in.TmuxPath}
	case maj > 3 || (maj == 3 && min >= 7):
		return Result{Status: OK, Summary: fmt.Sprintf("%s (%s — preview images can render sixel; the terminal has final say at attach)", in.TmuxPath, in.TmuxVersion)}
	default:
		return Result{Status: Warn, Summary: fmt.Sprintf("%s (%s — image previews degrade to low-fi; sixel retention needs tmux >= 3.7)", in.TmuxPath, in.TmuxVersion)}
	}
}

// parseTmuxVersion reads "3.7b"-shaped strings; letters after the
// minor number are release suffixes, not semver.
func parseTmuxVersion(v string) (maj, min int, ok bool) {
	if n, _ := fmt.Sscanf(v, "%d.%d", &maj, &min); n == 2 {
		return maj, min, true
	}
	return 0, 0, false
}

func shortDigest(hex string) string {
	if len(hex) > 12 {
		return hex[:12]
	}
	return hex
}

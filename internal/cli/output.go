package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/chrisdruta/vibe-tui-box/internal/app"
	"github.com/chrisdruta/vibe-tui-box/internal/registry"
	"github.com/chrisdruta/vibe-tui-box/internal/runtime"
	"github.com/chrisdruta/vibe-tui-box/internal/version"
)

// jsonObject wraps command output in a versioned envelope.
func writeJSON(w io.Writer, kind string, payload any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(struct {
		Format int    `json:"format"`
		Kind   string `json:"kind"`
		Data   any    `json:"data"`
	}{Format: 1, Kind: kind, Data: payload})
}

type versionResult struct {
	Info version.Info
}

func (r *versionResult) RenderHuman(w io.Writer) error {
	_, err := fmt.Fprintln(w, r.Info)
	return err
}

func (r *versionResult) RenderJSON(w io.Writer) error {
	return writeJSON(w, "version", r.Info)
}

type registerResult struct {
	Result app.RegisterResult
}

func (r *registerResult) RenderHuman(w io.Writer) error {
	rec := r.Result.Record
	_, err := fmt.Fprintf(w, "registered %s (%s) at %s\n", rec.DisplayName, rec.ID, rec.Root)
	return err
}

func (r *registerResult) RenderJSON(w io.Writer) error {
	return writeJSON(w, "register", r.Result.Record)
}

type configResult struct {
	Result app.ConfigResult
}

// The canonical plan is JSON by construction; both modes emit it, the
// JSON mode wrapped in the versioned envelope.
func (r *configResult) RenderHuman(w io.Writer) error {
	_, err := w.Write(r.Result.Canonical)
	return err
}

func (r *configResult) RenderJSON(w io.Writer) error {
	return writeJSON(w, "config", r.Result.Plan)
}

type psResult struct {
	Result app.PSResult
}

func (r *psResult) RenderHuman(w io.Writer) error {
	if len(r.Result.Projects) == 0 {
		_, err := fmt.Fprintln(w, "no registered projects")
		return err
	}
	for _, rec := range r.Result.Projects {
		if _, err := fmt.Fprintf(w, "%-24s %-8s %s\n", rec.DisplayName, rec.Mode, rec.Root); err != nil {
			return err
		}
	}
	if r.Result.AgentProject != "" {
		if _, err := fmt.Fprintf(w, "\nagents (%s)\n", r.Result.AgentProject); err != nil {
			return err
		}
		for _, row := range r.Result.Agents {
			if _, err := fmt.Fprintf(w, "  %-18s %-12s %-5s %s\n", row.Name, row.State, row.Age, row.Detail); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *psResult) RenderJSON(w io.Writer) error {
	projects := r.Result.Projects
	if projects == nil {
		projects = []registry.Record{}
	}
	agents := r.Result.Agents
	if agents == nil {
		agents = []app.AgentPSRow{}
	}
	return writeJSON(w, "ps", struct {
		Projects     []registry.Record `json:"projects"`
		AgentProject string            `json:"agentProject,omitempty"`
		Agents       []app.AgentPSRow  `json:"agents"`
	}{projects, r.Result.AgentProject, agents})
}

type upResult struct {
	Result app.UpResult
	Op     string
}

func (r *upResult) RenderHuman(w io.Writer) error {
	rec := r.Result.Record
	if _, err := fmt.Fprintf(w, "%s: %s (%s)\ncandidate %s\n", r.Op, rec.DisplayName, rec.ID, r.Result.Candidate); err != nil {
		return err
	}
	return renderState(w, r.Result.State)
}

func (r *upResult) RenderJSON(w io.Writer) error {
	return writeJSON(w, r.Op, struct {
		Record    registry.Record `json:"record"`
		Candidate string          `json:"candidate"`
		State     runtime.State   `json:"state"`
	}{r.Result.Record, r.Result.Candidate.String(), r.Result.State})
}

type downResult struct {
	Result app.DownResult
}

func (r *downResult) RenderHuman(w io.Writer) error {
	rec := r.Result.Record
	_, err := fmt.Fprintf(w, "down: %s (%s)\n", rec.DisplayName, rec.ID)
	return err
}

func (r *downResult) RenderJSON(w io.Writer) error {
	return writeJSON(w, "down", r.Result.Record)
}

type statusResult struct {
	Result app.StatusResult
}

func (r *statusResult) RenderHuman(w io.Writer) error {
	rec := r.Result.Record
	if _, err := fmt.Fprintf(w, "%s (%s) — %s\n", rec.DisplayName, rec.ID, rec.Root); err != nil {
		return err
	}
	return renderState(w, r.Result.State)
}

func (r *statusResult) RenderJSON(w io.Writer) error {
	return writeJSON(w, "status", struct {
		Record registry.Record `json:"record"`
		State  runtime.State   `json:"state"`
	}{r.Result.Record, r.Result.State})
}

func renderState(w io.Writer, state runtime.State) error {
	if len(state.Containers) == 0 {
		_, err := fmt.Fprintln(w, "no containers (run `vibe up`)")
		return err
	}
	for _, c := range state.Containers {
		status := "stopped"
		if c.Running {
			status = "running"
		}
		sync := ""
		if !c.InSync {
			sync = "  (stale candidate)"
		}
		if _, err := fmt.Fprintf(w, "  %-32s %-12s %s%s\n", c.Name, c.Role, status, sync); err != nil {
			return err
		}
	}
	return nil
}

// clipResult reports where the clipboard image landed. PathOnly mode
// prints exactly one machine-readable line — the container path — for
// the tui's prefix+v consumer; human mode narrates.
type clipResult struct {
	Result   app.ClipResult
	PathOnly bool
}

func (r *clipResult) RenderHuman(w io.Writer) error {
	if r.PathOnly {
		_, err := fmt.Fprintln(w, r.Result.ContainerPath)
		return err
	}
	if r.Result.SavedPath != "" {
		if _, err := fmt.Fprintf(w, "Saved: %s\n", r.Result.SavedPath); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "In the container: %s\n", r.Result.ContainerPath); err != nil {
		return err
	}
	if r.Result.PathCopied {
		_, err := fmt.Fprintln(w, "(path copied to clipboard)")
		return err
	}
	return nil
}

func (r *clipResult) RenderJSON(w io.Writer) error {
	return writeJSON(w, "clip", struct {
		ContainerPath string `json:"container_path"`
		SavedPath     string `json:"saved_path,omitempty"`
		PathCopied    bool   `json:"path_copied"`
	}{r.Result.ContainerPath, r.Result.SavedPath, r.Result.PathCopied})
}

// execResult carries only the container process's exit code, which
// becomes the vibe exit code.
type execResult struct {
	Code int
}

func (r *execResult) RenderHuman(io.Writer) error { return nil }

func (r *execResult) RenderJSON(w io.Writer) error {
	return writeJSON(w, "exec", struct {
		ExitCode int `json:"exit_code"`
	}{r.Code})
}

func (r *execResult) ExitCode() int { return r.Code }

type provisionResult struct {
	Result app.ProvisionResult
}

func (r *provisionResult) RenderHuman(w io.Writer) error {
	rec := r.Result.Artifact
	if _, err := fmt.Fprintf(w, "provisioned artifact %s (version %s, payload %s)\n",
		rec.Digest, rec.Version, rec.PayloadDigest); err != nil {
		return err
	}
	if r.Result.Pinned != nil {
		_, err := fmt.Fprintf(w, "pinned project %s (%s)\n", r.Result.Pinned.DisplayName, r.Result.Pinned.ID)
		return err
	}
	_, err := fmt.Fprintln(w, "no registered project here; run inside a project to pin it")
	return err
}

func (r *provisionResult) RenderJSON(w io.Writer) error {
	return writeJSON(w, "provision", struct {
		Artifact any `json:"artifact"`
		Pinned   any `json:"pinned,omitempty"`
	}{r.Result.Artifact, r.Result.Pinned})
}

type updateResult struct {
	Result app.UpdateResult
}

func (r *updateResult) RenderHuman(w io.Writer) error {
	rec := r.Result.Artifact
	if _, err := fmt.Fprintf(w, "installed release %s (artifact %s)\nbinary: %s\n",
		rec.Version, rec.Digest, r.Result.BinaryPath); err != nil {
		return err
	}
	if r.Result.Pinned != nil {
		if _, err := fmt.Fprintf(w, "pinned project %s (%s); run `vibe rebuild` to apply\n",
			r.Result.Pinned.DisplayName, r.Result.Pinned.ID); err != nil {
			return err
		}
	}
	return nil
}

func (r *updateResult) RenderJSON(w io.Writer) error {
	return writeJSON(w, "update", struct {
		Artifact   any    `json:"artifact"`
		Pinned     any    `json:"pinned,omitempty"`
		BinaryPath string `json:"binary_path"`
	}{r.Result.Artifact, r.Result.Pinned, r.Result.BinaryPath})
}

type initResult struct {
	Result app.InitResult
}

func (r *initResult) RenderHuman(w io.Writer) error {
	rec := r.Result.Record
	if _, err := fmt.Fprintf(w, "initialized %s (%s) from preset %q\n", rec.DisplayName, rec.ID, r.Result.Preset); err != nil {
		return err
	}
	for _, f := range r.Result.Created {
		if _, err := fmt.Fprintf(w, "  created %s\n", f); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "auto memory: %s (agent.memory in .vibe/vibe.yaml)\n", r.Result.Memory); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w, "next: `vibe up`")
	return err
}

func (r *initResult) RenderJSON(w io.Writer) error {
	return writeJSON(w, "init", struct {
		Record  registry.Record `json:"record"`
		Created []string        `json:"created"`
		Preset  string          `json:"preset"`
		Memory  string          `json:"memory"`
	}{r.Result.Record, r.Result.Created, r.Result.Preset, string(r.Result.Memory)})
}

type doctorResult struct {
	Result app.DoctorResult
}

func (r *doctorResult) RenderHuman(w io.Writer) error {
	for _, res := range r.Result.Results {
		if _, err := fmt.Fprintf(w, "%-5s %-11s %s\n", res.Status, res.Name, res.Summary); err != nil {
			return err
		}
		for _, d := range res.Details {
			if _, err := fmt.Fprintf(w, "      %s\n", d); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *doctorResult) RenderJSON(w io.Writer) error {
	return writeJSON(w, "doctor", struct {
		Healthy bool `json:"healthy"`
		Results any  `json:"results"`
	}{r.Result.Healthy, r.Result.Results})
}

func (r *doctorResult) ExitCode() int {
	if r.Result.Healthy {
		return 0
	}
	return 1
}

type bootstrapResult struct {
	Result app.BootstrapResult
}

func (r *bootstrapResult) RenderHuman(w io.Writer) error {
	if len(r.Result.Tools) == 0 {
		_, err := fmt.Fprintln(w, "no required tools declared in bootstrap.required")
		return err
	}
	for _, tool := range r.Result.Tools {
		mark := "ok"
		if !tool.Present {
			mark = "MISSING"
		}
		if _, err := fmt.Fprintf(w, "%-8s %s\n", mark, tool.Tool); err != nil {
			return err
		}
	}
	return nil
}

func (r *bootstrapResult) RenderJSON(w io.Writer) error {
	return writeJSON(w, "bootstrap", struct {
		Missing int `json:"missing"`
		Tools   any `json:"tools"`
	}{r.Result.Missing, r.Result.Tools})
}

func (r *bootstrapResult) ExitCode() int {
	if r.Result.Missing > 0 {
		return 1
	}
	return 0
}

type forgetResult struct {
	Result app.ForgetResult
}

func (r *forgetResult) RenderHuman(w io.Writer) error {
	rec := r.Result.Record
	_, err := fmt.Fprintf(w, "forgot %s (%s); workspace and .vibe/ untouched\n", rec.DisplayName, rec.ID)
	return err
}

func (r *forgetResult) RenderJSON(w io.Writer) error {
	return writeJSON(w, "forget", r.Result.Record)
}

type requestListResult struct {
	Result app.RequestListResult
}

func (r *requestListResult) RenderHuman(w io.Writer) error {
	for _, p := range r.Result.Problems {
		if _, err := fmt.Fprintf(w, "warning: %s\n", p); err != nil {
			return err
		}
	}
	if len(r.Result.Pending) == 0 {
		_, err := fmt.Fprintln(w, "no pending requests")
		return err
	}
	for _, p := range r.Result.Pending {
		if _, err := fmt.Fprintf(w, "%-20s candidate %s\n", p.RequestID, p.Candidate); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w, "inspect with `vibe request show ID`; decide with approve/reject DIGEST")
	return err
}

func (r *requestListResult) RenderJSON(w io.Writer) error {
	return writeJSON(w, "request-list", struct {
		Pending  any `json:"pending"`
		Problems any `json:"problems,omitempty"`
	}{r.Result.Pending, r.Result.Problems})
}

type requestShowResult struct {
	Result app.RequestShowResult
}

func (r *requestShowResult) RenderHuman(w io.Writer) error {
	p := r.Result.Pending
	if _, err := fmt.Fprintf(w, "request %s\ncandidate %s\ncreated %s\nsummary:\n", p.RequestID, p.Candidate, p.CreatedAt.Format("2006-01-02 15:04:05Z07:00")); err != nil {
		return err
	}
	for _, line := range r.Result.Summary.Lines {
		if _, err := fmt.Fprintf(w, "  │ %s\n", line); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w, "reason:"); err != nil {
		return err
	}
	for _, line := range r.Result.Reason.Lines {
		if _, err := fmt.Fprintf(w, "  │ %s\n", line); err != nil {
			return err
		}
	}
	if r.Result.Reason.Truncated || r.Result.Summary.Truncated {
		if _, err := fmt.Fprintln(w, "  │ … (truncated)"); err != nil {
			return err
		}
	}
	if r.Result.DiffLabel != "" {
		if _, err := fmt.Fprintln(w, r.Result.DiffLabel); err != nil {
			return err
		}
		for _, line := range r.Result.Diff.Lines {
			if _, err := fmt.Fprintf(w, "  │ %s\n", line); err != nil {
				return err
			}
		}
		if r.Result.Diff.Truncated {
			if _, err := fmt.Fprintln(w, "  │ … (truncated)"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *requestShowResult) RenderJSON(w io.Writer) error {
	return writeJSON(w, "request-show", struct {
		Pending  any      `json:"pending"`
		PlanDiff []string `json:"plan_diff,omitempty"`
	}{Pending: r.Result.Pending, PlanDiff: r.Result.Diff.Lines})
}

type requestDecideResult struct {
	Result app.RequestDecideResult
}

func (r *requestDecideResult) RenderHuman(w io.Writer) error {
	res := r.Result.Result
	if _, err := fmt.Fprintf(w, "request %s: %s\n", res.RequestID, res.Status); err != nil {
		return err
	}
	if r.Result.State != nil {
		return renderState(w, *r.Result.State)
	}
	return nil
}

func (r *requestDecideResult) RenderJSON(w io.Writer) error {
	return writeJSON(w, "request-decide", r.Result.Result)
}

type devOnResult struct {
	Result app.DevOnResult
}

func (r *devOnResult) RenderHuman(w io.Writer) error {
	_, err := fmt.Fprintf(w, "dev build installed: artifact %s (binary %s)\n%s -> this build; project %s is in dev mode; run `vibe rebuild` to apply\n",
		r.Result.Artifact.Digest, r.Result.Record.Output, r.Result.BinaryPath, r.Result.Project.DisplayName)
	return err
}

func (r *devOnResult) RenderJSON(w io.Writer) error {
	return writeJSON(w, "dev-on", struct {
		Record   any    `json:"record"`
		Project  any    `json:"project"`
		Artifact any    `json:"artifact"`
		Binary   string `json:"binary"`
	}{r.Result.Record, r.Result.Project, r.Result.Artifact, r.Result.BinaryPath})
}

type devOffResult struct {
	Result app.DevOffResult
}

func (r *devOffResult) RenderHuman(w io.Writer) error {
	handoff := "no release artifact installed — `vibe` still runs the last dev build"
	if r.Result.BinaryPath != "" {
		handoff = r.Result.BinaryPath + " -> release binary"
	}
	_, err := fmt.Fprintf(w, "project %s is back in release mode (artifact %s)\n%s\n",
		r.Result.Project.DisplayName, r.Result.Project.ReleaseVersion, handoff)
	return err
}

func (r *devOffResult) RenderJSON(w io.Writer) error {
	return writeJSON(w, "dev-off", struct {
		Project any    `json:"project"`
		Binary  string `json:"binary,omitempty"`
	}{r.Result.Project, r.Result.BinaryPath})
}

type devStatusResult struct {
	Result app.DevStatusResult
}

func (r *devStatusResult) RenderHuman(w io.Writer) error {
	rec := r.Result.Project
	if _, err := fmt.Fprintf(w, "%s mode: %s (artifact %s)\n", rec.DisplayName, rec.Mode, rec.ReleaseVersion); err != nil {
		return err
	}
	if r.Result.Record != nil {
		_, err := fmt.Fprintf(w, "dev build: source %s → binary %s (built %s)\n",
			r.Result.Record.Source, r.Result.Record.Output, r.Result.Record.BuiltAt.Format("2006-01-02 15:04"))
		return err
	}
	return nil
}

func (r *devStatusResult) RenderJSON(w io.Writer) error {
	return writeJSON(w, "dev-status", struct {
		Project any `json:"project"`
		Record  any `json:"record,omitempty"`
	}{r.Result.Project, r.Result.Record})
}

type gcResult struct {
	Result app.GCResult
}

func (r *gcResult) RenderHuman(w io.Writer) error {
	verb := "removed"
	if r.Result.DryRun {
		verb = "would remove"
	}
	counts := map[string]int{}
	for _, e := range r.Result.Removed {
		counts[e.Kind]++
	}
	if len(r.Result.Removed) == 0 && r.Result.StagingEntries == 0 {
		_, err := fmt.Fprintln(w, "nothing to collect")
		return err
	}
	if _, err := fmt.Fprintf(w, "%s %d item(s), %s\n", verb, len(r.Result.Removed), humanBytes(r.Result.TotalBytes)); err != nil {
		return err
	}
	for _, kind := range []string{"artifact", "candidate", "snapshot", "record", "binary", "broker", "state"} {
		if counts[kind] > 0 {
			if _, err := fmt.Fprintf(w, "  %-9s %d\n", kind, counts[kind]); err != nil {
				return err
			}
		}
	}
	if r.Result.StagingEntries > 0 {
		if _, err := fmt.Fprintf(w, "  staging   %d entr%s (%s)\n", r.Result.StagingEntries,
			pluralY(r.Result.StagingEntries), humanBytes(r.Result.StagingBytes)); err != nil {
			return err
		}
	}
	for _, s := range r.Result.Skipped {
		if _, err := fmt.Fprintf(w, "  skipped %s %s: %s\n", s.Kind, s.ID, s.Reason); err != nil {
			return err
		}
	}
	return nil
}

func (r *gcResult) RenderJSON(w io.Writer) error {
	return writeJSON(w, "gc", r.Result)
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func pluralY(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

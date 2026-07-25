package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chrisdruta/vibe-tui-box/internal/dockerapi"
	dockerfake "github.com/chrisdruta/vibe-tui-box/internal/dockerapi/fake"
	"github.com/chrisdruta/vibe-tui-box/internal/doctor"
	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/paths"
	"github.com/chrisdruta/vibe-tui-box/internal/payload"
	"github.com/chrisdruta/vibe-tui-box/internal/schema"
	"github.com/chrisdruta/vibe-tui-box/internal/terminal"
)

// withPresetPayload adds a bundle whose minimal preset renders a valid
// manifest.
func withPresetPayload(t *testing.T, a *App) {
	t.Helper()
	tmpl := `schema: 1
harness: {{.HarnessVersion}}
image:
  base: "mcr.microsoft.com/devcontainers/base:debian"
  agents: [claude]
agent:
  cmd: claude
  tmux: true
  memory: {{.AutoMemory}}
`
	script := "#!/bin/sh\nexec sleep infinity\n"
	files := map[string][]byte{
		"container/entrypoint.sh":        []byte(script),
		"presets/minimal/vibe.yaml.tmpl": []byte(tmpl),
	}
	var entries []payload.File
	for path, content := range files {
		mode := "0644"
		if filepath.Ext(path) == ".sh" {
			mode = "0755"
		}
		entries = append(entries, payload.File{
			Path: path, Mode: mode, Size: int64(len(content)), Digest: domain.SHA256(content),
		})
	}
	manifest, err := payload.EncodeManifest(entries)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for path, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, payload.ManifestPath), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	bundle, err := payload.New(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "vibe")
	if err := os.WriteFile(exe, []byte("FAKE-ENGINE"), 0o755); err != nil {
		t.Fatal(err)
	}
	a.deps.Payload = bundle
	a.deps.Executable = exe
}

func TestInitRendersRegistersAndPins(t *testing.T) {
	a, _ := newTestApp(t)
	withPresetPayload(t, a)
	ctx := context.Background()

	// Provision first so init pins the artifact.
	if _, err := a.Provision(ctx, ProvisionRequest{Dir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	res, err := a.Init(ctx, InitRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if res.Preset != "minimal" || len(res.Created) != 1 {
		t.Fatalf("init result: %+v", res)
	}
	if res.Record.Artifact.IsZero() {
		t.Fatal("init did not pin the installed artifact")
	}

	// The rendered manifest is valid and templated.
	data, err := os.ReadFile(filepath.Join(dir, paths.ManifestRelPath))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" || !containsStr(string(data), "harness: v") {
		t.Fatalf("rendered manifest wrong:\n%s", data)
	}

	// Config compiles the new project.
	if _, err := a.Config(ctx, ConfigRequest{Dir: dir}); err != nil {
		t.Fatalf("config after init: %v", err)
	}

	// Re-init refuses to overwrite.
	if _, err := a.Init(ctx, InitRequest{Dir: dir}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("re-init should conflict, got %v", err)
	}
	// Unknown preset.
	if _, err := a.Init(ctx, InitRequest{Dir: t.TempDir(), Preset: "nope"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown preset should be not-found, got %v", err)
	}
}

// fatalPrompt fails the test if the prompt is consulted at all.
type fatalPrompt struct{ t *testing.T }

func (p fatalPrompt) Confirm(context.Context, terminal.Confirmation) (bool, error) {
	p.t.Fatal("prompt must not be consulted")
	return false, nil
}

func TestInitAutoMemoryResolution(t *testing.T) {
	a, _ := newTestApp(t)
	withPresetPayload(t, a)
	ctx := context.Background()

	readMemory := func(t *testing.T, dir string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, paths.ManifestRelPath))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "memory:"); ok {
				return strings.TrimSpace(rest)
			}
		}
		t.Fatalf("no memory line in rendered manifest:\n%s", data)
		return ""
	}

	// An explicit flag decides without consulting the prompt, in both
	// directions.
	a.deps.Prompt = fatalPrompt{t}
	for _, tc := range []struct {
		flag bool
		want string
	}{{true, "auto"}, {false, "off"}} {
		dir := t.TempDir()
		flag := tc.flag
		res, err := a.Init(ctx, InitRequest{Dir: dir, AutoMemory: &flag, Interactive: true})
		if err != nil {
			t.Fatal(err)
		}
		if got := readMemory(t, dir); got != tc.want || string(res.Memory) != tc.want {
			t.Fatalf("flag %v rendered %q (result %q), want %q", tc.flag, got, res.Memory, tc.want)
		}
	}

	// No flag on an interactive session: the question decides.
	a.deps.Prompt = terminal.AutoApprove{Approve: true}
	dir := t.TempDir()
	res, err := a.Init(ctx, InitRequest{Dir: dir, Interactive: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := readMemory(t, dir); got != "auto" || res.Memory != schema.MemoryAuto {
		t.Fatalf("prompted init rendered %q (result %q), want auto", got, res.Memory)
	}

	// No flag, not interactive: hardened default, prompt untouched.
	a.deps.Prompt = fatalPrompt{t}
	dir = t.TempDir()
	res, err = a.Init(ctx, InitRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if got := readMemory(t, dir); got != "off" || res.Memory != schema.MemoryOff {
		t.Fatalf("non-interactive init rendered %q (result %q), want off", got, res.Memory)
	}
}

func TestDoctorFlow(t *testing.T) {
	a, docker := newTestApp(t)
	withPresetPayload(t, a)
	ctx := context.Background()

	// Outside a project: project-scoped checks skip, docker fails
	// against an unreachable daemon fake? The fake pings fine, so the
	// suite is healthy.
	res, err := a.Doctor(ctx, DoctorRequest{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Healthy {
		t.Fatalf("empty-host doctor should be healthy: %+v", res.Results)
	}
	byName := map[string]doctor.Result{}
	for _, r := range res.Results {
		byName[r.Name] = r
	}
	if byName["project"].Status != doctor.Skip {
		t.Fatalf("project check outside a project: %+v", byName["project"])
	}

	// Inside a provisioned, running project everything reports ok.
	dir := t.TempDir()
	if _, err := a.Init(ctx, InitRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Provision(ctx, ProvisionRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Up(ctx, UpRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	res, err = a.Doctor(ctx, DoctorRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	byName = map[string]doctor.Result{}
	for _, r := range res.Results {
		byName[r.Name] = r
	}
	for _, name := range []string{"layout", "project", "artifact", "docker", "containers", "lifecycle"} {
		if byName[name].Status != doctor.OK {
			t.Fatalf("check %s: %+v", name, byName[name])
		}
	}

	// A dead daemon turns docker checks into failures.
	docker.PingErr = errors.New("daemon gone")
	res, err = a.Doctor(ctx, DoctorRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if res.Healthy {
		t.Fatal("doctor should be unhealthy with a dead daemon")
	}
}

func TestBootstrapReportsTools(t *testing.T) {
	a, docker := newTestApp(t)
	withPresetPayload(t, a)
	ctx := context.Background()
	dir := newProject(t)
	writeManifest(t, dir, `schema: 1
harness: v2.0.0
image:
  base: "mcr.microsoft.com/devcontainers/base:debian"
  agents: [claude]
agent:
  cmd: claude
  tmux: true
bootstrap:
  required: [git, missing-tool]
`)
	reg, err := a.Register(ctx, RegisterRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Up(ctx, UpRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	docker.ExecResults[dockerfake.ExecKey([]string{"which", "git"})] = execOK()
	docker.ExecResults[dockerfake.ExecKey([]string{"which", "missing-tool"})] = execFail()

	res, err := a.Bootstrap(ctx, BootstrapRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 2 || res.Missing != 1 {
		t.Fatalf("bootstrap result: %+v", res)
	}
	if !res.Tools[0].Present || res.Tools[1].Present {
		t.Fatalf("tool statuses wrong: %+v", res.Tools)
	}

	// A failure after resolution (dev container not running) carries the
	// resolved project's ID on the OpError.
	if _, err := a.Down(ctx, DownRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	_, err = a.Bootstrap(ctx, BootstrapRequest{Dir: dir})
	if err == nil {
		t.Fatal("bootstrap should fail with the dev container down")
	}
	var opErr *domain.OpError
	if !errors.As(err, &opErr) {
		t.Fatalf("want *domain.OpError, got %T: %v", err, err)
	}
	if opErr.Project != reg.Record.ID {
		t.Fatalf("OpError project = %q, want %q", opErr.Project, reg.Record.ID)
	}
}

func containsStr(s, sub string) bool { return strings.Contains(s, sub) }

func execOK() dockerapi.ExecResult   { return dockerapi.ExecResult{ExitCode: 0} }
func execFail() dockerapi.ExecResult { return dockerapi.ExecResult{ExitCode: 1} }

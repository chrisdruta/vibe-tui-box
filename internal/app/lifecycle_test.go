package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chrisdruta/vibe-tui-box/internal/builder"
	"github.com/chrisdruta/vibe-tui-box/internal/dockerapi"
	dockerfake "github.com/chrisdruta/vibe-tui-box/internal/dockerapi/fake"
	"github.com/chrisdruta/vibe-tui-box/internal/model"
	"github.com/chrisdruta/vibe-tui-box/internal/paths"
	"github.com/chrisdruta/vibe-tui-box/internal/schema"
)

func writeManifest(t *testing.T, dir, manifest string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, paths.ManifestRelPath), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestUpStatusRunDown(t *testing.T) {
	a, docker := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)

	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}

	up, err := a.Up(ctx, UpRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if up.Candidate.IsZero() || !up.State.Running() {
		t.Fatalf("up result wrong: %+v", up)
	}
	if up.Record.Approved == nil || *up.Record.Approved != up.Candidate {
		t.Fatal("approved candidate not recorded")
	}

	// The base image was resolved to a digest during preparation.
	if len(docker.CallsTo("ResolveImage")) == 0 {
		t.Fatal("images not resolved")
	}

	// Container env carries manifest env and the engine-provided claude
	// state relocation — never the frozen env file, which stays
	// exec-scoped (`vibe run`, the agent CLI).
	creates := docker.CallsTo("CreateContainer")
	if len(creates) != 1 {
		t.Fatalf("want 1 container, got %d", len(creates))
	}
	created := creates[0].Request.(dockerapi.CreateRequest)
	wantEnv := []string{"FLAG=1", "CLAUDE_CONFIG_DIR=/vibe/agent-state/claude", "DISABLE_AUTOUPDATER=1"}
	if fmt.Sprint(created.Env) != fmt.Sprint(wantEnv) {
		t.Fatalf("container env wrong: %v", created.Env)
	}

	status, err := a.Status(ctx, StatusRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !status.State.Running() {
		t.Fatalf("status not running: %+v", status.State)
	}

	// Second up is a no-op for containers and bumps the approved pointer
	// to the identical candidate.
	up2, err := a.Up(ctx, UpRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if up2.Candidate != up.Candidate {
		t.Fatal("identical inputs must produce the identical candidate")
	}
	if got := len(docker.CallsTo("CreateContainer")); got != 1 {
		t.Fatalf("idempotent up created containers: %d", got)
	}

	// Run: exec in the dev container with the frozen env file.
	res, err := a.Run(ctx, ContainerCommand{Dir: dir, Argv: []string{"env"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("run exit %d", res.ExitCode)
	}
	execs := docker.CallsTo("Exec")
	lastExec := execs[len(execs)-1].Request.(dockerapi.ExecRequest)
	if len(lastExec.Env) != 1 || lastExec.Env[0] != "SECRET=s3cret" {
		t.Fatalf("run env wrong: %v", lastExec.Env)
	}
	if lastExec.User != "vscode" {
		t.Fatalf("run user %q", lastExec.User)
	}

	// Exec: explicit env only.
	if _, err := a.Exec(ctx, ContainerCommand{Dir: dir, Argv: []string{"true"}}); err != nil {
		t.Fatal(err)
	}
	execs = docker.CallsTo("Exec")
	lastExec = execs[len(execs)-1].Request.(dockerapi.ExecRequest)
	if len(lastExec.Env) != 0 {
		t.Fatalf("exec must not inherit env: %v", lastExec.Env)
	}

	if _, err := a.Down(ctx, DownRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if len(docker.Containers) != 0 {
		t.Fatal("containers left after down")
	}

	// Agent commands against a stopped project fail clearly.
	if _, err := a.Exec(ctx, ContainerCommand{Dir: dir, Argv: []string{"true"}}); err == nil {
		t.Fatal("exec without a running container should fail")
	}
}

// advancingClock hands out a distinct timestamp per call, like the
// production SystemClock. The suite-wide fixedClock cannot see records
// that diverge by clock alone.
type advancingClock struct{ n int }

func (c *advancingClock) Now() time.Time {
	c.n++
	return time.Date(2026, 7, 23, 12, 0, c.n, 0, time.UTC)
}

// A rebuild on unchanged inputs recompiles the identical plan, so the
// candidate record already exists. Under a moving clock that rewrite
// must not be mistaken for a divergent one.
func TestRebuildUnchangedInputsUnderMovingClock(t *testing.T) {
	a, docker := newTestApp(t)
	a.deps.Clock = &advancingClock{}
	ctx := context.Background()
	dir := newProject(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	up, err := a.Up(ctx, UpRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	rebuilt, err := a.Up(ctx, UpRequest{Dir: dir, Force: true})
	if err != nil {
		t.Fatalf("rebuild on unchanged inputs: %v", err)
	}
	if rebuilt.Candidate != up.Candidate {
		t.Fatal("identical inputs must produce the identical candidate")
	}
	// The rebuild reached the containers rather than aborting in
	// candidate preparation.
	if got := len(docker.CallsTo("CreateContainer")); got != 2 {
		t.Fatalf("want 2 container creates (up + forced rebuild), got %d", got)
	}
	// The surviving record keeps the first candidate's creation time.
	recCand, err := a.deps.Store.ReadCandidateRecord(up.Candidate)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 7, 23, 12, 0, 1, 0, time.UTC); !recCand.CreatedAt.Equal(want) {
		t.Fatalf("created_at %s, want the original %s", recCand.CreatedAt, want)
	}
}

// lastToolsRefreshArg returns the per-agent claude refresh arg carried
// by the most recent tools-image build, and whether it was present at
// all. The test manifest lists only claude, so its arg is the whole
// refresh surface; every Build call is a tools build.
func lastToolsRefreshArg(t *testing.T, docker *dockerfake.Client) (string, bool) {
	t.Helper()
	builds := docker.CallsTo("Build")
	if len(builds) == 0 {
		t.Fatal("no tools image was built")
	}
	req := builds[len(builds)-1].Request.(dockerapi.BuildRequest)
	v, ok := req.BuildArgs[builder.AgentRefreshArgFor(schema.AgentClaude)]
	return v, ok
}

// Every rebuild refreshes the unversioned agents: the token is minted
// without any flag, threaded into the tools build, and persisted; a
// later plain up keeps it (warm cache), never reverting the agents.
// With no version prober wired (this app), the pair values fall back
// to the mint timestamp — the pre-probe behavior.
func TestRebuildRefreshesAgents(t *testing.T) {
	a, docker := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}

	// Plain up: no refresh token minted or passed to the build —
	// idempotent ups stay off the network.
	up, err := a.Up(ctx, UpRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if up.Record.AgentRefresh != "" {
		t.Fatalf("plain up set a refresh token: %q", up.Record.AgentRefresh)
	}
	if v, ok := lastToolsRefreshArg(t, docker); ok {
		t.Fatalf("plain up passed a refresh build arg: %q", v)
	}

	// Plain rebuild — no flag: token minted as the per-agent
	// fingerprint, its claude value passed to the build, persisted.
	// "No version given" means "latest per rebuild".
	rb, err := a.Up(ctx, UpRequest{Dir: dir, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	token := rb.Record.AgentRefresh
	value := builder.ParseAgentRefresh(token)[schema.AgentClaude]
	if token == "" || value == "" {
		t.Fatalf("rebuild did not mint a per-agent token: %q", token)
	}
	if v, ok := lastToolsRefreshArg(t, docker); !ok || v != value {
		t.Fatalf("rebuild build arg = (%q, %v), want the claude pair %q", v, ok, value)
	}

	// A later plain up keeps the token and still passes it — the
	// refreshed agents stay warm-cached, never reverting.
	up2, err := a.Up(ctx, UpRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if up2.Record.AgentRefresh != token {
		t.Fatalf("plain up changed the token: %q -> %q", token, up2.Record.AgentRefresh)
	}
	if v, ok := lastToolsRefreshArg(t, docker); !ok || v != value {
		t.Fatalf("plain up dropped the token: got (%q, %v), want %q", v, ok, value)
	}
}

// fakeAgentVersions scripts the rebuild-time channel probe.
type fakeAgentVersions struct {
	versions map[schema.AgentKind]string
	err      error
	calls    int
}

func (f *fakeAgentVersions) Resolve(_ context.Context, kind schema.AgentKind, _ string) (string, error) {
	f.calls++
	if f.err != nil {
		return "", f.err
	}
	return f.versions[kind], nil
}

// The probe makes the refresh token MEAN the resolved upstream version:
// two rebuilds against an unmoved channel mint the identical token (the
// warm-cache case that used to re-download identical builds), a bump
// changes it, and a probe failure degrades to the timestamp fallback so
// staleness never hides behind a dead endpoint.
func TestRebuildProbesAgentVersions(t *testing.T) {
	a, docker := newTestApp(t)
	probe := &fakeAgentVersions{versions: map[schema.AgentKind]string{schema.AgentClaude: "2.1.220"}}
	a.deps.AgentVersions = probe
	ctx := context.Background()
	dir := newProject(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}

	rb1, err := a.Up(ctx, UpRequest{Dir: dir, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if rb1.Record.AgentRefresh != "claude=2.1.220" {
		t.Fatalf("token = %q, want the resolved fingerprint", rb1.Record.AgentRefresh)
	}
	if v, ok := lastToolsRefreshArg(t, docker); !ok || v != "2.1.220" {
		t.Fatalf("build arg = (%q, %v), want the resolved version", v, ok)
	}
	if probe.calls != 1 {
		t.Fatalf("want one probe (one channel-tracking agent), got %d", probe.calls)
	}

	// Upstream unmoved: the second rebuild mints the IDENTICAL token —
	// same build arg, warm Docker layer cache, no re-download.
	rb2, err := a.Up(ctx, UpRequest{Dir: dir, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if rb2.Record.AgentRefresh != rb1.Record.AgentRefresh {
		t.Fatalf("unmoved upstream changed the token: %q -> %q", rb1.Record.AgentRefresh, rb2.Record.AgentRefresh)
	}

	// A real bump changes the pair and re-pulls.
	probe.versions[schema.AgentClaude] = "2.1.221"
	rb3, err := a.Up(ctx, UpRequest{Dir: dir, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if rb3.Record.AgentRefresh != "claude=2.1.221" {
		t.Fatalf("bumped upstream token = %q", rb3.Record.AgentRefresh)
	}

	// Probe failure: the pair value degrades to the mint timestamp —
	// today's bust-every-rebuild, never a stale skip.
	probe.err = errors.New("endpoint down")
	rb4, err := a.Up(ctx, UpRequest{Dir: dir, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	fallback := builder.ParseAgentRefresh(rb4.Record.AgentRefresh)[schema.AgentClaude]
	if fallback == "" || fallback == "2.1.221" {
		t.Fatalf("failed probe must fall back to a timestamp value: %q", rb4.Record.AgentRefresh)
	}

	// A junk value that would corrupt the pair grammar is a bust, not a
	// token.
	probe.err = nil
	probe.versions[schema.AgentClaude] = "2.1.222 --evil"
	rb5, err := a.Up(ctx, UpRequest{Dir: dir, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if v := builder.ParseAgentRefresh(rb5.Record.AgentRefresh)[schema.AgentClaude]; strings.Contains(v, "evil") || v == "" {
		t.Fatalf("junk probe value leaked into the token: %q", rb5.Record.AgentRefresh)
	}
}

func TestShellProbesCandidates(t *testing.T) {
	a, docker := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Up(ctx, UpRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}

	// zsh missing, bash present.
	docker.ExecResults[dockerfake.ExecKey([]string{"test", "-x", "/bin/zsh"})] = dockerapi.ExecResult{ExitCode: 1}
	docker.ExecResults[dockerfake.ExecKey([]string{"test", "-x", "/bin/bash"})] = dockerapi.ExecResult{ExitCode: 0}

	if _, err := a.Shell(ctx, ContainerCommand{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	execs := docker.CallsTo("Exec")
	last := execs[len(execs)-1].Request.(dockerapi.ExecRequest)
	if len(last.Argv) != 2 || last.Argv[0] != "/bin/bash" || last.Argv[1] != "-l" {
		t.Fatalf("shell argv wrong: %v", last.Argv)
	}
}

func lastExecArgv(t *testing.T, docker *dockerfake.Client) dockerapi.ExecRequest {
	t.Helper()
	execs := docker.CallsTo("Exec")
	if len(execs) == 0 {
		t.Fatal("no exec calls recorded")
	}
	return execs[len(execs)-1].Request.(dockerapi.ExecRequest)
}

var agentProbeKey = dockerfake.ExecKey([]string{"test", "-r", model.PayloadAgentSession, "-a",
	"(", "-x", "/usr/local/bin/tmux", "-o", "-x", "/usr/bin/tmux", ")"})

func TestAgentSessionWrapping(t *testing.T) {
	a, docker := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	reg, err := a.Register(ctx, RegisterRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Up(ctx, UpRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}

	// tmux and the payload probe fine (fake execs default to exit 0):
	// the agent CLI is wrapped in the session carrier, with the frozen
	// env file and the engine identity.
	if _, err := a.Agent(ctx, AgentRequest{ContainerCommand: ContainerCommand{Dir: dir}}); err != nil {
		t.Fatal(err)
	}
	last := lastExecArgv(t, docker)
	wantArgv := []string{"bash", model.PayloadAgentSession, "agent", "--", "claude"}
	if fmt.Sprint(last.Argv) != fmt.Sprint(wantArgv) {
		t.Fatalf("agent argv wrong: %v", last.Argv)
	}
	wantEnv := []string{
		"SECRET=s3cret",
		"VIBE_PROJECT=" + string(reg.Record.ID),
		"VIBE_PROJECT_NAME=" + reg.Record.DisplayName,
		"VIBE_AGENT_MEMORY=off",
	}
	if fmt.Sprint(last.Env) != fmt.Sprint(wantEnv) {
		t.Fatalf("agent env wrong: %v", last.Env)
	}
	// Workloads start in the workspace, never the image WORKDIR.
	if last.WorkDir != model.WorkspaceTarget {
		t.Fatalf("agent workdir %q, want %q", last.WorkDir, model.WorkspaceTarget)
	}

	// Flag pass-throughs keep script order: --cold, -a marker, -s NAME.
	if _, err := a.Agent(ctx, AgentRequest{
		ContainerCommand: ContainerCommand{Dir: dir},
		Cold:             true,
		Agent:            "claude",
		Session:          "review",
	}); err != nil {
		t.Fatal(err)
	}
	last = lastExecArgv(t, docker)
	wantArgv = []string{"bash", model.PayloadAgentSession, "agent", "--cold", "-a", "-s", "review", "--", "claude"}
	if fmt.Sprint(last.Argv) != fmt.Sprint(wantArgv) {
		t.Fatalf("agent flag argv wrong: %v", last.Argv)
	}

	// -a must name an installed agent.
	if _, err := a.Agent(ctx, AgentRequest{ContainerCommand: ContainerCommand{Dir: dir}, Agent: "codex"}); err == nil {
		t.Fatal("-a outside image.agents should fail")
	}

	// A container without tmux or the payload falls back to direct exec —
	// identity still rides along, the cold/session variants do not exist.
	docker.ExecResults[agentProbeKey] = dockerapi.ExecResult{ExitCode: 1}
	if _, err := a.Agent(ctx, AgentRequest{ContainerCommand: ContainerCommand{Dir: dir}}); err != nil {
		t.Fatal(err)
	}
	last = lastExecArgv(t, docker)
	if fmt.Sprint(last.Argv) != fmt.Sprint([]string{"claude"}) {
		t.Fatalf("fallback argv wrong: %v", last.Argv)
	}
	if fmt.Sprint(last.Env) != fmt.Sprint(wantEnv) {
		t.Fatalf("fallback env wrong: %v", last.Env)
	}
	if _, err := a.Agent(ctx, AgentRequest{ContainerCommand: ContainerCommand{Dir: dir}, Cold: true}); err == nil {
		t.Fatal("--cold without the carrier should fail")
	}

	// agent.memory: auto rides the identity env (read live from the
	// manifest, like agent.tmux — never through the candidate).
	docker.ExecResults[agentProbeKey] = dockerapi.ExecResult{ExitCode: 0}
	writeManifest(t, dir, strings.Replace(testManifest, "  tmux: true", "  tmux: true\n  memory: auto", 1))
	if _, err := a.Agent(ctx, AgentRequest{ContainerCommand: ContainerCommand{Dir: dir}}); err != nil {
		t.Fatal(err)
	}
	last = lastExecArgv(t, docker)
	if !slices.Contains(last.Env, "VIBE_AGENT_MEMORY=auto") {
		t.Fatalf("memory env missing: %v", last.Env)
	}
}

func TestAgentTmuxOptOut(t *testing.T) {
	a, docker := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	writeManifest(t, dir, strings.Replace(testManifest, "tmux: true", "tmux: false", 1))
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Up(ctx, UpRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Agent(ctx, AgentRequest{ContainerCommand: ContainerCommand{Dir: dir}}); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(lastExecArgv(t, docker).Argv) != fmt.Sprint([]string{"claude"}) {
		t.Fatalf("opt-out argv wrong: %v", lastExecArgv(t, docker).Argv)
	}
	// The opt-out never probes.
	for _, call := range docker.CallsTo("Exec") {
		if dockerfake.ExecKey(call.Request.(dockerapi.ExecRequest).Argv) == agentProbeKey {
			t.Fatal("agent.tmux: false must not probe for the carrier")
		}
	}
}

func TestAgentStopRestart(t *testing.T) {
	a, docker := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	reg, err := a.Register(ctx, RegisterRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Up(ctx, UpRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}

	// --stop swaps the script mode and keeps the session-naming flags;
	// the frozen env file (secrets) never rides a kill — identity only.
	if _, err := a.Agent(ctx, AgentRequest{
		ContainerCommand: ContainerCommand{Dir: dir},
		Mode:             AgentStop,
		Session:          "review",
	}); err != nil {
		t.Fatal(err)
	}
	last := lastExecArgv(t, docker)
	wantArgv := []string{"bash", model.PayloadAgentSession, "stop", "-s", "review", "--", "claude"}
	if fmt.Sprint(last.Argv) != fmt.Sprint(wantArgv) {
		t.Fatalf("stop argv wrong: %v", last.Argv)
	}
	wantEnv := []string{
		"VIBE_PROJECT=" + string(reg.Record.ID),
		"VIBE_PROJECT_NAME=" + reg.Record.DisplayName,
		"VIBE_AGENT_MEMORY=off",
	}
	if fmt.Sprint(last.Env) != fmt.Sprint(wantEnv) {
		t.Fatalf("stop env wrong: %v", last.Env)
	}

	// --restart rides agent mode as a flag and keeps the full launch env.
	if _, err := a.Agent(ctx, AgentRequest{
		ContainerCommand: ContainerCommand{Dir: dir},
		Mode:             AgentRestart,
	}); err != nil {
		t.Fatal(err)
	}
	last = lastExecArgv(t, docker)
	wantArgv = []string{"bash", model.PayloadAgentSession, "agent", "--restart", "--", "claude"}
	if fmt.Sprint(last.Argv) != fmt.Sprint(wantArgv) {
		t.Fatalf("restart argv wrong: %v", last.Argv)
	}
	if !slices.Contains(last.Env, "SECRET=s3cret") {
		t.Fatalf("restart env lost the frozen env file: %v", last.Env)
	}

	// Without the carrier there is no session to address.
	docker.ExecResults[agentProbeKey] = dockerapi.ExecResult{ExitCode: 1}
	if _, err := a.Agent(ctx, AgentRequest{ContainerCommand: ContainerCommand{Dir: dir}, Mode: AgentStop}); err == nil {
		t.Fatal("--stop without the carrier should fail")
	}
	if _, err := a.Agent(ctx, AgentRequest{ContainerCommand: ContainerCommand{Dir: dir}, Mode: AgentRestart}); err == nil {
		t.Fatal("--restart without the carrier should fail")
	}
}

func TestPSAgentRows(t *testing.T) {
	a, docker := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}

	// No running dev container: the fleet listing stands alone.
	res, err := a.PS(ctx, PSRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if res.AgentProject != "" || len(res.Agents) != 0 {
		t.Fatalf("agent rows without a container: %+v", res)
	}

	if _, err := a.Up(ctx, UpRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	// Container-controlled bytes: control characters are stripped, rows
	// are decoded, ages rendered against the engine clock (fixed at
	// 2026-07-23T12:00Z).
	psKey := dockerfake.ExecKey([]string{"bash", model.PayloadAgentPS})
	base := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC).Unix()
	docker.ExecOutputs[psKey] = fmt.Sprintf(
		"agent|working|%d|claude - opus|claude|opus\nagent-codex|exited(3)|%d|detached\nbad line\nevil|id\x1ble|%d|\nweb|running|%d||svc|\nblender|exited(137)|||svc|\n",
		base-90, base-7200, base-5, base-10800)
	res, err = a.PS(ctx, PSRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if res.AgentProject == "" || len(res.Agents) != 3 {
		t.Fatalf("agent rows wrong: %+v", res)
	}
	// The feeder's `svc` kind marker routes workspace-service rows to
	// their own section — Name and State are the whole truth, and the
	// marker never leaks into a CLI column.
	if len(res.Services) != 2 ||
		res.Services[0].Name != "web" || res.Services[0].State != "running" ||
		res.Services[1].Name != "blender" || res.Services[1].State != "exited(137)" ||
		res.Services[0].CLI != "" {
		t.Fatalf("service rows wrong: %+v", res.Services)
	}
	// Services ship an epoch since the signal-density pass; one written
	// before it (empty field) just has no age.
	if res.Services[0].Age != "3h" || res.Services[0].Since != base-10800 || res.Services[1].Age != "" {
		t.Fatalf("service ages wrong: %+v", res.Services)
	}
	if res.Agents[0].Name != "agent" || res.Agents[0].State != "working" || res.Agents[0].Age != "1m" {
		t.Fatalf("row 0 wrong: %+v", res.Agents[0])
	}
	// The feeder's trailing cli/model columns land as fields; the human
	// detail column keeps the same joined form it always had.
	if res.Agents[0].CLI != "claude" || res.Agents[0].Model != "opus" || res.Agents[0].Detail != "claude - opus" {
		t.Fatalf("row 0 truth columns: %+v", res.Agents[0])
	}
	// A four-field row (an image built before the split) still decodes.
	if res.Agents[1].State != "exited(3)" || res.Agents[1].Age != "2h" || res.Agents[1].Detail != "detached" ||
		res.Agents[1].CLI != "" {
		t.Fatalf("row 1 wrong: %+v", res.Agents[1])
	}
	if res.Agents[2].State != "idle" {
		t.Fatalf("escape byte not stripped: %q", res.Agents[2].State)
	}

	// A feeder failure (no payload, older image) drops the section.
	docker.ExecResults[psKey] = dockerapi.ExecResult{ExitCode: 127}
	res, err = a.PS(ctx, PSRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if res.AgentProject != "" || len(res.Agents) != 0 {
		t.Fatalf("agent rows despite feeder failure: %+v", res)
	}
}

func TestReapAgentClients(t *testing.T) {
	a, docker := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	reg, err := a.Register(ctx, RegisterRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Up(ctx, UpRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	a.reapAgentClients(ctx, reg.Record)
	last := lastExecArgv(t, docker)
	want := []string{"bash", model.PayloadAgentSession, "reap"}
	if fmt.Sprint(last.Argv) != fmt.Sprint(want) {
		t.Fatalf("reap argv wrong: %v", last.Argv)
	}
}

func TestUpDoesNotApproveOnFailure(t *testing.T) {
	a, docker := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	docker.StartErr = errors.New("boom")
	if _, err := a.Up(ctx, UpRequest{Dir: dir}); err == nil {
		t.Fatal("up should fail when start fails")
	}
	status, err := a.Status(ctx, StatusRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if status.Record.Approved != nil {
		t.Fatal("failed up must not move the approved candidate")
	}
}

func TestViewerWindowSelfStamp(t *testing.T) {
	a, _ := newTestApp(t)
	rt := &recordingTmux{}
	a.deps.Tmux = rt
	ctx := context.Background()
	dir := newProject(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Up(ctx, UpRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}

	// Outside the vibe-engine server the edge resolves ViewerPane to
	// empty (tmux.SelfPane) and no stamp lands: pane ids are per-server
	// counters, and a personal tmux's %3 could collide with a real
	// window on ours.
	if _, err := a.Agent(ctx, AgentRequest{ContainerCommand: ContainerCommand{Dir: dir}}); err != nil {
		t.Fatal(err)
	}
	if len(rt.winOptions) != 0 {
		t.Fatalf("without a viewer pane nothing must be stamped: %v", rt.winOptions)
	}

	// Inside the engine server, every launch door self-stamps its own
	// window with the composed session address — the grammar mirror of
	// agent-session.sh's agent(-cmd)(-name)(-cold).
	a.deps.ViewerPane = "%3"
	if _, err := a.Agent(ctx, AgentRequest{ContainerCommand: ContainerCommand{Dir: dir}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Agent(ctx, AgentRequest{
		ContainerCommand: ContainerCommand{Dir: dir},
		Cold:             true,
		Agent:            "claude",
		Session:          "review",
	}); err != nil {
		t.Fatal(err)
	}
	want := []recordedWinOption{
		{Pane: "%3", Option: "@vibe_session", Value: "agent"},
		{Pane: "%3", Option: "@vibe_session", Value: "agent-claude-review-cold"},
	}
	if fmt.Sprint(rt.winOptions) != fmt.Sprint(want) {
		t.Fatalf("agent stamps wrong: %v", rt.winOptions)
	}

	// A stop runs in a popup pane, never a viewer: no stamp.
	if _, err := a.Agent(ctx, AgentRequest{ContainerCommand: ContainerCommand{Dir: dir}, Mode: AgentStop}); err != nil {
		t.Fatal(err)
	}
	if len(rt.winOptions) != len(want) {
		t.Fatalf("stop must not stamp: %v", rt.winOptions)
	}

	// The attach-only viewer spawn stamps the full address it was given.
	if _, err := a.Attach(ctx, AttachRequest{ContainerCommand: ContainerCommand{Dir: dir}, Session: "agent-codex"}); err != nil {
		t.Fatal(err)
	}
	if got := rt.winOptions[len(rt.winOptions)-1].Value; got != "agent-codex" {
		t.Fatalf("attach stamp wrong: %q", got)
	}
}

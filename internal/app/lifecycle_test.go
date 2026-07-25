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

	// Container env carries the frozen env file, manifest env, then the
	// engine-provided claude state relocation.
	creates := docker.CallsTo("CreateContainer")
	if len(creates) != 1 {
		t.Fatalf("want 1 container, got %d", len(creates))
	}
	created := creates[0].Request.(dockerapi.CreateRequest)
	wantEnv := []string{"SECRET=s3cret", "FLAG=1", "CLAUDE_CONFIG_DIR=/vibe/agent-state/claude", "DISABLE_AUTOUPDATER=1"}
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

// lastToolsRefreshArg returns the AgentRefreshArg carried by the most
// recent tools-image build, and whether it was present at all. The test
// manifest builds only the tools image, so every Build call is one.
func lastToolsRefreshArg(t *testing.T, docker *dockerfake.Client) (string, bool) {
	t.Helper()
	builds := docker.CallsTo("Build")
	if len(builds) == 0 {
		t.Fatal("no tools image was built")
	}
	req := builds[len(builds)-1].Request.(dockerapi.BuildRequest)
	v, ok := req.BuildArgs[builder.AgentRefreshArg]
	return v, ok
}

// --refresh-agents mints a token, threads it into the tools build, and
// persists it so later plain rebuilds keep the refreshed agents (warm)
// instead of reverting to the cache-frozen build.
func TestRebuildRefreshAgents(t *testing.T) {
	a, docker := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}

	// Plain up: no refresh token minted or passed to the build.
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

	// Rebuild --refresh-agents: token minted, passed to the build, persisted.
	rb, err := a.Up(ctx, UpRequest{Dir: dir, Force: true, RefreshAgents: true})
	if err != nil {
		t.Fatal(err)
	}
	token := rb.Record.AgentRefresh
	if token == "" {
		t.Fatal("refresh did not persist a token")
	}
	if v, ok := lastToolsRefreshArg(t, docker); !ok || v != token {
		t.Fatalf("refresh build arg = (%q, %v), want persisted token %q", v, ok, token)
	}

	// A later plain rebuild keeps the same token and still passes it —
	// the refreshed agents do not revert.
	rb2, err := a.Up(ctx, UpRequest{Dir: dir, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if rb2.Record.AgentRefresh != token {
		t.Fatalf("plain rebuild changed the token: %q -> %q", token, rb2.Record.AgentRefresh)
	}
	if v, ok := lastToolsRefreshArg(t, docker); !ok || v != token {
		t.Fatalf("plain rebuild dropped the token: got (%q, %v), want %q", v, ok, token)
	}
}

// The refresh path is not gated on Force: `vibe up --refresh-agents`
// mints, threads, and persists the token exactly like rebuild does.
func TestUpRefreshAgents(t *testing.T) {
	a, docker := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	up, err := a.Up(ctx, UpRequest{Dir: dir, RefreshAgents: true})
	if err != nil {
		t.Fatal(err)
	}
	token := up.Record.AgentRefresh
	if token == "" {
		t.Fatal("up --refresh-agents did not persist a token")
	}
	if v, ok := lastToolsRefreshArg(t, docker); !ok || v != token {
		t.Fatalf("up refresh build arg = (%q, %v), want %q", v, ok, token)
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
		Stop:             true,
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
		Restart:          true,
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
	if _, err := a.Agent(ctx, AgentRequest{ContainerCommand: ContainerCommand{Dir: dir}, Stop: true}); err == nil {
		t.Fatal("--stop without the carrier should fail")
	}
	if _, err := a.Agent(ctx, AgentRequest{ContainerCommand: ContainerCommand{Dir: dir}, Restart: true}); err == nil {
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
		"agent|working|%d|\nagent-codex|exited(3)|%d|detached\nbad line\nevil|id\x1ble|%d|\n",
		base-90, base-7200, base-5)
	res, err = a.PS(ctx, PSRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if res.AgentProject == "" || len(res.Agents) != 3 {
		t.Fatalf("agent rows wrong: %+v", res)
	}
	if res.Agents[0].Name != "agent" || res.Agents[0].State != "working" || res.Agents[0].Age != "1m" {
		t.Fatalf("row 0 wrong: %+v", res.Agents[0])
	}
	if res.Agents[1].State != "exited(3)" || res.Agents[1].Age != "2h" || res.Agents[1].Detail != "detached" {
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

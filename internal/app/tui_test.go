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
	"testing/fstest"

	"vibe/internal/dockerapi"
	"vibe/internal/domain"
	"vibe/internal/model"
	"vibe/internal/payload"
	"vibe/internal/registry"
	"vibe/internal/tmux"
)

func TestStartApproved(t *testing.T) {
	a, docker := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	reg, err := a.Register(ctx, RegisterRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}

	// Nothing approved yet: refuse with the `vibe up` hint instead of
	// letting a tui session race a container that cannot exist.
	err = a.startApproved(ctx, reg.Record)
	if err == nil || !errors.Is(err, domain.ErrUnavailable) || !strings.Contains(err.Error(), "vibe up") {
		t.Fatalf("no-approved error wrong: %v", err)
	}

	up, err := a.Up(ctx, UpRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}

	// Running and in sync: a status listing, no reconcile.
	created := len(docker.CallsTo("CreateContainer"))
	if err := a.startApproved(ctx, up.Record); err != nil {
		t.Fatal(err)
	}
	if got := len(docker.CallsTo("CreateContainer")); got != created {
		t.Fatalf("in-sync start reconciled: %d -> %d creates", created, got)
	}

	// After a down, the approved candidate is brought back exactly —
	// containers recreated, no input freeze (no new snapshot).
	if _, err := a.Down(ctx, DownRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	snaps := len(docker.CallsTo("ResolveImage"))
	if err := a.startApproved(ctx, up.Record); err != nil {
		t.Fatal(err)
	}
	if got := len(docker.CallsTo("CreateContainer")); got <= created {
		t.Fatalf("post-down start did not recreate containers: %d creates", got)
	}
	if got := len(docker.CallsTo("ResolveImage")); got != snaps {
		t.Fatal("startApproved must not re-resolve inputs — it runs the approved candidate")
	}
}

func TestStartApprovedGuards(t *testing.T) {
	a, docker := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)

	// A service makes the plan multi-container, so completeness is
	// judged against the PLAN — a removed sidecar must not pass.
	manifest := testManifest + `services:
  cache:
    image: "redis:7"
`
	writeManifest(t, dir, manifest)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	up, err := a.Up(ctx, UpRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}

	// Remove the sidecar outright: the state listing alone can't see it,
	// the plan can. startApproved must reconcile it back.
	containers, err := docker.ListProjectContainers(ctx, up.Record.ID)
	if err != nil {
		t.Fatal(err)
	}
	removed := false
	for _, c := range containers {
		if strings.Contains(string(c.Name), "-svc-") {
			if err := docker.RemoveContainer(ctx, c.ID, dockerapi.RemoveOptions{Force: true}); err != nil {
				t.Fatal(err)
			}
			removed = true
		}
	}
	if !removed {
		t.Fatal("no sidecar container to remove")
	}
	created := len(docker.CallsTo("CreateContainer"))
	if err := a.startApproved(ctx, up.Record); err != nil {
		t.Fatal(err)
	}
	if got := len(docker.CallsTo("CreateContainer")); got <= created {
		t.Fatal("missing planned sidecar did not trigger a reconcile")
	}

	// Running containers on a DIFFERENT candidate are live work: point
	// the approved pointer at the OLD candidate (the crashed-up shape)
	// and startApproved must refuse rather than replace them.
	writeManifest(t, dir, manifest+"bootstrap:\n  required: [git]\n")
	up2, err := a.Up(ctx, UpRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if up2.Candidate == up.Candidate {
		t.Fatal("manifest change should compile a new candidate")
	}
	reverted, err := a.deps.Registry.Update(ctx, up2.Record.ID, up2.Record.Revision, func(r *registry.Record) error {
		r.Approved = &up.Candidate
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = a.startApproved(ctx, reverted)
	if err == nil || !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale running containers should refuse with conflict, got %v", err)
	}
	if !strings.Contains(err.Error(), "vibe up") {
		t.Fatalf("refusal must name the deliberate command: %v", err)
	}
}

func TestDownKillsUISession(t *testing.T) {
	a, _ := newTestApp(t)
	rt := &recordingTmux{}
	a.deps.Tmux = rt
	ctx := context.Background()
	dir := newProject(t)
	reg, err := a.Register(ctx, RegisterRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Up(ctx, UpRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Down(ctx, DownRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	want := tmux.SessionFor(reg.Record.ID)
	if len(rt.killed) != 1 || rt.killed[0] != want {
		t.Fatalf("down killed %v, want exactly [%s]", rt.killed, want)
	}
}

// The CLI defers the kill so its output flushes before the session —
// possibly the one the command runs inside — goes down: no inline
// kill, and the returned KillUI does exactly the inline one's job.
func TestDownDeferredUIKill(t *testing.T) {
	a, _ := newTestApp(t)
	rt := &recordingTmux{}
	a.deps.Tmux = rt
	ctx := context.Background()
	dir := newProject(t)
	reg, err := a.Register(ctx, RegisterRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Up(ctx, UpRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	res, err := a.Down(ctx, DownRequest{Dir: dir, DeferUIKill: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rt.killed) != 0 {
		t.Fatalf("deferred down killed inline: %v", rt.killed)
	}
	if res.KillUI == nil {
		t.Fatal("deferred down returned no KillUI")
	}
	res.KillUI(ctx)
	want := tmux.SessionFor(reg.Record.ID)
	if len(rt.killed) != 1 || rt.killed[0] != want {
		t.Fatalf("KillUI killed %v, want exactly [%s]", rt.killed, want)
	}
}

func TestMaterializeTuiConfAppendsUserConf(t *testing.T) {
	a, _ := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}

	entry := "#!/bin/sh\nexec sleep infinity\n"
	conf := "# payload conf body\nset -g status on\n"
	manifest, err := payload.EncodeManifest([]payload.File{
		{Path: "container/entrypoint.sh", Mode: "0755", Size: int64(len(entry)), Digest: domain.SHA256([]byte(entry))},
		{Path: "host/tmux-tui.conf", Mode: "0644", Size: int64(len(conf)), Digest: domain.SHA256([]byte(conf))},
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := payload.New(fstest.MapFS{
		"container/entrypoint.sh": &fstest.MapFile{Data: []byte(entry)},
		"host/tmux-tui.conf":      &fstest.MapFile{Data: []byte(conf)},
		payload.ManifestPath:      &fstest.MapFile{Data: manifest},
	})
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "vibe")
	if err := os.WriteFile(exe, []byte("FAKE-ENGINE"), 0o755); err != nil {
		t.Fatal(err)
	}
	a.deps.Payload = bundle
	a.deps.Executable = exe
	if _, err := a.Provision(ctx, ProvisionRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}

	_, rec, err := a.resolveProject(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	path, hostDir, err := a.materializeTuiConf(ctx, rec, a.deps.Executable)
	if err != nil {
		t.Fatal(err)
	}
	if path == "" || hostDir == "" {
		t.Fatalf("conf not materialized: %q %q", path, hostDir)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	// Prologue stamps, the payload body, then the user conf hook LAST so
	// user overrides win.
	if !strings.Contains(got, "@vibe_exe") || !strings.Contains(got, "# payload conf body") {
		t.Fatalf("materialized conf incomplete:\n%s", got)
	}
	userConf := filepath.Join(filepath.Dir(a.deps.Layout.Root), ".config", "vibe", "tui.conf")
	wantLine := "source-file -q " + `"` + userConf + `"`
	idx := strings.Index(got, wantLine)
	if idx == -1 {
		t.Fatalf("user conf hook missing (want %q):\n%s", wantLine, got)
	}
	if idx < strings.Index(got, "# payload conf body") {
		t.Fatalf("user conf must load after the payload body:\n%s", got)
	}
}

// withTuiConfPayload equips the app with a payload bundle whose
// host/tmux-tui.conf drives the rich (conf != "") Tui path, plus a fake
// executable so provisioning pins a real artifact.
func withTuiConfPayload(t *testing.T, a *App) {
	t.Helper()
	entry := "#!/bin/sh\nexec sleep infinity\n"
	conf := "# payload conf body\nset -g status on\n"
	manifest, err := payload.EncodeManifest([]payload.File{
		{Path: "container/entrypoint.sh", Mode: "0755", Size: int64(len(entry)), Digest: domain.SHA256([]byte(entry))},
		{Path: "host/tmux-tui.conf", Mode: "0644", Size: int64(len(conf)), Digest: domain.SHA256([]byte(conf))},
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := payload.New(fstest.MapFS{
		"container/entrypoint.sh": &fstest.MapFile{Data: []byte(entry)},
		"host/tmux-tui.conf":      &fstest.MapFile{Data: []byte(conf)},
		payload.ManifestPath:      &fstest.MapFile{Data: manifest},
	})
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

// reapExecs counts the container-side reap execs the tui issued.
func reapExecs(t *testing.T, calls []dockerapi.ExecRequest) int {
	t.Helper()
	want := []string{"bash", model.PayloadAgentSession, "reap"}
	n := 0
	for _, req := range calls {
		if slices.Equal(req.Argv, want) {
			n++
		}
	}
	return n
}

// TestTuiHappyPathWithConf drives the full rich path: an already-running
// project pinned to a conf-carrying artifact. startApproved is a no-op
// (everything is up), the conf is materialized and applied, identity
// globals/options are stamped, and Attach fires against the ID-derived
// session.
func TestTuiHappyPathWithConf(t *testing.T) {
	a, docker := newTestApp(t)
	withTuiConfPayload(t, a)
	ctx := context.Background()
	dir := newProject(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Provision(ctx, ProvisionRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	up, err := a.Up(ctx, UpRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	// Attach the recorder only now so the setup's serial bumps don't land
	// in the recorded globals — the Tui path's own writes are the subject.
	rt := &recordingTmux{hasSession: true}
	a.deps.Tmux = rt

	created := len(docker.CallsTo("CreateContainer"))
	if err := a.Tui(ctx, TuiRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	// startApproved found everything already running: no reconcile.
	if got := len(docker.CallsTo("CreateContainer")); got != created {
		t.Fatalf("in-sync tui reconciled: %d -> %d creates", created, got)
	}

	session := tmux.SessionFor(up.Record.ID)

	// The conf is materialized and applied to the server exactly once.
	if len(rt.configured) != 1 || rt.configured[0] == "" {
		t.Fatalf("ConfigureServer = %v, want one non-empty path", rt.configured)
	}
	// EnsureSession: ID-derived session, agent window, [exe, "agent"].
	if len(rt.ensured) != 1 {
		t.Fatalf("EnsureSession calls = %d, want 1", len(rt.ensured))
	}
	spec := rt.ensured[0]
	if spec.ID != session || spec.Window != "agent" || spec.Workdir == "" {
		t.Fatalf("session spec = %+v", spec)
	}
	if !slices.Equal(spec.Command, []string{a.deps.Executable, "agent"}) {
		t.Fatalf("session command = %v, want [%s agent]", spec.Command, a.deps.Executable)
	}
	// VIBE_TUI_CONF stamped to the materialized conf path.
	if len(rt.envs) != 1 || rt.envs[0] != (recordedEnv{Name: "VIBE_TUI_CONF", Value: rt.configured[0]}) {
		t.Fatalf("SetEnvironment = %+v, want VIBE_TUI_CONF=%s", rt.envs, rt.configured[0])
	}
	// Server globals: exe and the store-owned payload host dir. No engine
	// serial — startApproved never bumped (nothing needed starting).
	if v, ok := rt.globalValue("@vibe_exe"); !ok || v != a.deps.Executable {
		t.Fatalf("@vibe_exe = %q (%v), want %q", v, ok, a.deps.Executable)
	}
	if v, ok := rt.globalValue("@vibe_payload_dir"); !ok || !strings.HasSuffix(v, "/payload/host") {
		t.Fatalf("@vibe_payload_dir = %q (%v), want a /payload/host suffix", v, ok)
	}
	if _, ok := rt.globalValue("@vibe_engine_serial"); ok {
		t.Fatal("tui bumped the engine serial on an already-running project")
	}
	// Session options carry identity for the host scripts.
	if v, ok := rt.optionValue(session, "@vibe_name"); !ok || v != up.Record.DisplayName {
		t.Fatalf("@vibe_name = %q (%v), want %q", v, ok, up.Record.DisplayName)
	}
	if v, ok := rt.optionValue(session, "@vibe_project"); !ok || v != string(up.Record.ID) {
		t.Fatalf("@vibe_project = %q (%v), want %q", v, ok, up.Record.ID)
	}
	// status-right stamped with the clickable engine-state cell; identity
	// stays out of the bar on the rich path.
	status, ok := rt.optionValue(session, "status-right")
	if !ok {
		t.Fatal("status-right not stamped")
	}
	if !strings.Contains(status, "_state --project "+string(up.Record.ID)) {
		t.Fatalf("status-right missing engine-state cell: %q", status)
	}
	// Attach targeted the session.
	if !slices.Equal(rt.attached, []tmux.SessionID{session}) {
		t.Fatalf("Attach = %v, want [%s]", rt.attached, session)
	}
	// No server ran before this Tui (ListSessions answered none), so
	// the fresh conf applies at server start — no re-source needed.
	if len(rt.sourced) != 0 {
		t.Fatalf("fresh server must not re-source: %v", rt.sourced)
	}
}

// TestOpenProjectMintsWithoutAttach pins the sidebar's make-it-live
// click (`vibe _open`): by registry ID with no cwd, the approved path
// starts what was approved and mints the session — stamps and all —
// WITHOUT attaching (the click handler switches the client itself),
// and the never-approved path runs the full first Up and reports
// Approved=false so the handler skips the auto-switch.
func TestOpenProjectMintsWithoutAttach(t *testing.T) {
	a, _ := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	reg, err := a.Register(ctx, RegisterRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	up, err := a.Up(ctx, UpRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	rt := &recordingTmux{}
	a.deps.Tmux = rt

	res, err := a.OpenProject(ctx, OpenProjectRequest{Project: reg.Record.ID})
	if err != nil {
		t.Fatal(err)
	}
	session := tmux.SessionFor(reg.Record.ID)
	if res.Session != session || !res.Approved {
		t.Fatalf("result = %+v, want approved session %s", res, session)
	}
	if len(rt.ensured) != 1 || rt.ensured[0].ID != session || rt.ensured[0].Workdir != up.Record.Root {
		t.Fatalf("EnsureSession = %+v", rt.ensured)
	}
	if len(rt.attached) != 0 {
		t.Fatalf("open must never attach: %v", rt.attached)
	}
	if v, ok := rt.optionValue(session, "@vibe_project"); !ok || v != string(reg.Record.ID) {
		t.Fatalf("@vibe_project = %q (%v)", v, ok)
	}
	// The chrome status-right resolves the engine THROUGH @vibe_exe so
	// the cell follows shim handoffs; both minting paths must stamp the
	// identical string.
	status, ok := rt.optionValue(session, "status-right")
	if !ok || !strings.Contains(status, "#('#{@vibe_exe}' _state --project "+string(reg.Record.ID)+")") {
		t.Fatalf("status-right = %q (%v)", status, ok)
	}
	if _, ok := rt.globalValue("@vibe_engine_serial"); !ok {
		t.Fatal("open must bump the engine serial (fleet facts changed)")
	}

	// Never-approved: the full first Up runs, and the caller is told
	// not to auto-switch.
	dir2 := newProject(t)
	reg2, err := a.Register(ctx, RegisterRequest{Dir: dir2})
	if err != nil {
		t.Fatal(err)
	}
	res2, err := a.OpenProject(ctx, OpenProjectRequest{Project: reg2.Record.ID})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Approved {
		t.Fatal("first-approval open must report Approved=false")
	}
	status2, err := a.Status(ctx, StatusRequest{Dir: dir2})
	if err != nil || !status2.State.Running() {
		t.Fatalf("open must have brought the project up: %+v, %v", status2, err)
	}

	// Unknown project: not found, nothing minted.
	if _, err := a.OpenProject(ctx, OpenProjectRequest{Project: domain.ProjectID("aaaaaaaaaaaaaaaaaaaaaaaaaa")}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown project must be not found, got %v", err)
	}
}

// TestReapSessionResolvesByName pins the session-closed hook's door
// (`vibe _reap`): only the truncated ID-derived session name survives a
// kill, so the engine resolves it through SessionFor and reaps that
// project's container-side ghost viewers; an unmapped name is a quiet
// no-op.
func TestReapSessionResolvesByName(t *testing.T) {
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
	before := len(docker.CallsTo("Exec"))
	if err := a.ReapSession(ctx, string(tmux.SessionFor(reg.Record.ID))); err != nil {
		t.Fatal(err)
	}
	execs := docker.CallsTo("Exec")
	if len(execs) != before+1 {
		t.Fatalf("want one reap exec, got %d new", len(execs)-before)
	}
	argv := execs[len(execs)-1].Request.(dockerapi.ExecRequest).Argv
	if len(argv) != 3 || argv[2] != "reap" {
		t.Fatalf("reap argv = %v", argv)
	}
	if err := a.ReapSession(ctx, "vibe-zzzzzzzzzzzz"); err != nil {
		t.Fatalf("unmapped name must no-op, got %v", err)
	}
	if got := len(docker.CallsTo("Exec")); got != before+1 {
		t.Fatalf("unmapped name must not exec, got %d new", got-before)
	}
}

// TestTuiAttachHealsExistingServer covers the attach-time heal: a
// server that predates this engine keeps its birth conf (-f applies at
// server start only), so joining it must re-source the freshly
// materialized conf or every new binding waits for a manual prefix+R —
// the 2026-07-28 stale-bindings dogfood, twice in one evening.
func TestTuiAttachHealsExistingServer(t *testing.T) {
	a, _ := newTestApp(t)
	withTuiConfPayload(t, a)
	ctx := context.Background()
	dir := newProject(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Provision(ctx, ProvisionRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Up(ctx, UpRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	rt := &recordingTmux{
		hasSession: true,
		sessions:   []tmux.Session{{ID: "vibe-other", Attached: true}},
	}
	a.deps.Tmux = rt
	if err := a.Tui(ctx, TuiRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if len(rt.configured) != 1 {
		t.Fatalf("ConfigureServer = %v", rt.configured)
	}
	if !slices.Equal(rt.sourced, []string{rt.configured[0]}) {
		t.Fatalf("joining a live server must re-source the fresh conf: %v", rt.sourced)
	}
}

// TestTuiBarePathEscapesDisplayName covers the conf == "" path (no
// artifact pinned): none of the rich-path wiring runs, and the bare
// status-right carries the operator's display name — escaped, so a
// #()-bearing name cannot inject a tmux command.
func TestTuiBarePathEscapesDisplayName(t *testing.T) {
	a, _ := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	const evil = "evil #(rm -rf ~) name"
	reg, err := a.Register(ctx, RegisterRequest{Dir: dir, DisplayName: evil})
	if err != nil {
		t.Fatal(err)
	}
	// Up with no provision: no artifact pins, so materializeTuiConf yields
	// "" and the tui runs bare.
	if _, err := a.Up(ctx, UpRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	rt := &recordingTmux{hasSession: true}
	a.deps.Tmux = rt
	if err := a.Tui(ctx, TuiRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	session := tmux.SessionFor(reg.Record.ID)

	// The rich-path wiring is skipped wholesale.
	if len(rt.configured) != 0 {
		t.Fatalf("ConfigureServer called on the bare path: %v", rt.configured)
	}
	if len(rt.envs) != 0 {
		t.Fatalf("SetEnvironment called on the bare path: %v", rt.envs)
	}
	if len(rt.globals) != 0 {
		t.Fatalf("global options set on the bare path: %v", rt.globals)
	}

	status, ok := rt.optionValue(session, "status-right")
	if !ok {
		t.Fatal("status-right not stamped")
	}
	// The display name is escaped: every '#' doubled to "##".
	if !strings.Contains(status, "##(") {
		t.Fatalf("display name not escaped (no ##(): %q", status)
	}
	// The engine's own #(<exe> _state ...) prefix is the only legitimate
	// directive. Strip it and confirm the name portion is exactly the
	// escaped form — and that collapsing the escaped ## pairs leaves no
	// lone #( that tmux would execute.
	prefix := fmt.Sprintf("#('%s' _state --project %s)", a.deps.Executable, reg.Record.ID)
	name := strings.TrimPrefix(status, prefix)
	if name != tmux.EscapeFormat(evil) {
		t.Fatalf("display-name portion = %q, want %q", name, tmux.EscapeFormat(evil))
	}
	if collapsed := strings.ReplaceAll(name, "##", ""); strings.Contains(collapsed, "#(") {
		t.Fatalf("unescaped #( directive survived: %q", status)
	}
}

// TestTuiStartFailsSessionExists: startApproved fails (nothing approved)
// but a live session exists → attach anyway, no error.
func TestTuiStartFailsSessionExists(t *testing.T) {
	a, _ := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	reg, err := a.Register(ctx, RegisterRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	// No Up → nothing approved → startApproved returns an error; the live
	// session (HasSession true) means we attach anyway.
	rt := &recordingTmux{hasSession: true}
	a.deps.Tmux = rt
	if err := a.Tui(ctx, TuiRequest{Dir: dir}); err != nil {
		t.Fatalf("attach-anyway must not error: %v", err)
	}
	session := tmux.SessionFor(reg.Record.ID)
	if !slices.Equal(rt.attached, []tmux.SessionID{session}) {
		t.Fatalf("Attach = %v, want [%s]", rt.attached, session)
	}
}

// TestTuiStartFailsNoSession: startApproved fails and no session exists →
// the engine error surfaces as a tui OpError carrying the project ID.
func TestTuiStartFailsNoSession(t *testing.T) {
	a, _ := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	reg, err := a.Register(ctx, RegisterRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	rt := &recordingTmux{hasSession: false}
	a.deps.Tmux = rt
	err = a.Tui(ctx, TuiRequest{Dir: dir})
	var opErr *domain.OpError
	if !errors.As(err, &opErr) {
		t.Fatalf("want *domain.OpError, got %T: %v", err, err)
	}
	if opErr.Op != "tui" || opErr.Project != reg.Record.ID {
		t.Fatalf("op error = {Op:%q Project:%q}, want {tui %s}", opErr.Op, opErr.Project, reg.Record.ID)
	}
	if !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("want ErrUnavailable (no approved candidate), got %v", err)
	}
	if len(rt.attached) != 0 {
		t.Fatalf("no session: must not attach, got %v", rt.attached)
	}
}

// TestTuiReapOnQuit: after Attach returns, an absent session (a quit)
// reaps the ghost agent clients; a live session (a plain detach) leaves
// the panes running.
func TestTuiReapOnQuit(t *testing.T) {
	cases := []struct {
		name       string
		hasSession bool
		wantReap   bool
	}{
		{"quit reaps ghost clients", false, true},
		{"detach leaves panes running", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, docker := newTestApp(t)
			withTuiConfPayload(t, a)
			ctx := context.Background()
			dir := newProject(t)
			if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
				t.Fatal(err)
			}
			if _, err := a.Provision(ctx, ProvisionRequest{Dir: dir}); err != nil {
				t.Fatal(err)
			}
			if _, err := a.Up(ctx, UpRequest{Dir: dir}); err != nil {
				t.Fatal(err)
			}
			rt := &recordingTmux{hasSession: tc.hasSession}
			a.deps.Tmux = rt
			if err := a.Tui(ctx, TuiRequest{Dir: dir}); err != nil {
				t.Fatal(err)
			}
			var execs []dockerapi.ExecRequest
			for _, call := range docker.CallsTo("Exec") {
				if req, ok := call.Request.(dockerapi.ExecRequest); ok {
					execs = append(execs, req)
				}
			}
			if got := reapExecs(t, execs); (got > 0) != tc.wantReap {
				t.Fatalf("reap execs = %d, want reap=%v", got, tc.wantReap)
			}
		})
	}
}

// TestTuiUnavailableWithoutTmux: a nil Tmux dependency is unavailable,
// surfaced as a tui OpError before any project work.
func TestTuiUnavailableWithoutTmux(t *testing.T) {
	a, _ := newTestApp(t) // newTestApp injects no Tmux dependency
	ctx := context.Background()
	dir := newProject(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	err := a.Tui(ctx, TuiRequest{Dir: dir})
	if !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("nil tmux should be unavailable, got %v", err)
	}
	var opErr *domain.OpError
	if !errors.As(err, &opErr) || opErr.Op != "tui" {
		t.Fatalf("want a tui OpError, got %T: %v", err, err)
	}
}

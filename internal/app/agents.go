package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"time"

	"github.com/chrisdruta/vibe-tui-box/internal/dockerapi"
	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/envfile"
	"github.com/chrisdruta/vibe-tui-box/internal/model"
	"github.com/chrisdruta/vibe-tui-box/internal/registry"
	"github.com/chrisdruta/vibe-tui-box/internal/schema"
	"github.com/chrisdruta/vibe-tui-box/internal/store"
	"github.com/chrisdruta/vibe-tui-box/internal/tmuxui"
)

// ContainerCommand is the shared shape for commands that run inside the
// dev container. Argv is preserved exactly; there is no shell-string
// form anywhere in the engine.
type ContainerCommand struct {
	Dir     string
	User    string
	Workdir string
	Env     []envfile.Entry
	Argv    []string
	TTY     bool
	Stdin   bool
	Streams dockerapi.Streams
}

type ExecResult struct {
	ExitCode int
}

// devContainerUser is the fixed in-container account.
const devContainerUser = "vscode"

// Exec runs argv in the dev container with only the explicitly given
// environment.
func (a *App) Exec(ctx context.Context, cmd ContainerCommand) (ExecResult, error) {
	fail := opFail[ExecResult]("exec", "")
	rec, name, err := a.devContainer(ctx, cmd.Dir)
	if err != nil {
		return fail(err)
	}
	fail = opFail[ExecResult]("exec", rec.ID)
	res, err := a.execIn(ctx, name, cmd)
	if err != nil {
		return fail(err)
	}
	return res, nil
}

// Run behaves like Exec but additionally loads the env file frozen in
// the approved candidate's snapshot, so workloads see the project
// environment without the host ever exporting it.
func (a *App) Run(ctx context.Context, cmd ContainerCommand) (ExecResult, error) {
	fail := opFail[ExecResult]("run", "")
	rec, name, err := a.devContainer(ctx, cmd.Dir)
	if err != nil {
		return fail(err)
	}
	fail = opFail[ExecResult]("run", rec.ID)
	entries, err := a.approvedEnvFile(ctx, rec)
	if err != nil {
		return fail(err)
	}
	cmd.Env = append(entries, cmd.Env...)
	res, err := a.execIn(ctx, name, cmd)
	if err != nil {
		return fail(err)
	}
	return res, nil
}

// shellCandidates is the fixed probe order for Shell.
var shellCandidates = []string{"/bin/zsh", "/bin/bash", "/bin/sh"}

// Shell opens an interactive login shell: the first candidate that
// exists in the container wins.
func (a *App) Shell(ctx context.Context, cmd ContainerCommand) (ExecResult, error) {
	fail := opFail[ExecResult]("shell", "")
	rec, name, err := a.devContainer(ctx, cmd.Dir)
	if err != nil {
		return fail(err)
	}
	fail = opFail[ExecResult]("shell", rec.ID)
	shell := ""
	for _, candidate := range shellCandidates {
		probe, err := a.deps.Docker.Exec(ctx, dockerapi.ExecRequest{
			Container: name,
			User:      devContainerUser,
			Argv:      []string{"test", "-x", candidate},
		})
		if err != nil {
			return fail(err)
		}
		if probe.ExitCode == 0 {
			shell = candidate
			break
		}
	}
	if shell == "" {
		return fail(fmt.Errorf("%w: none of %s exist in the container", domain.ErrNotFound, strings.Join(shellCandidates, ", ")))
	}
	cmd.Argv = []string{shell, "-l"}
	res, err := a.execIn(ctx, name, cmd)
	if err != nil {
		return fail(err)
	}
	return res, nil
}

// AttachRequest connects to the dev container: without Session, the
// container's main process; with one, that named in-container tmux
// session (e.g. the `services` session lifecycle hooks populate, or an
// agent session a tray ghost cell reaches for). Nested marks the exec
// as spawned under `vibe tui`, exactly as on AgentRequest, so the inner
// tmux client it creates is reapable when the UI dies.
type AttachRequest struct {
	ContainerCommand
	Session string
	// Window pre-selects a window within Session before the attach —
	// the sidebar's service-row click lands on the clicked svc window.
	// Best-effort in the container; requires Session.
	Window string
	Nested bool
}

// The inner-session charset lives in tmuxui.SessionNameRe — one
// definition for the write side (porcelain emit), the read side
// (parsers), and these verb gates (2026-08-04, deduped).

func (a *App) Attach(ctx context.Context, req AttachRequest) (ExecResult, error) {
	fail := opFail[ExecResult]("attach", "")
	rec, name, err := a.devContainer(ctx, req.Dir)
	if err != nil {
		return fail(err)
	}
	fail = opFail[ExecResult]("attach", rec.ID)
	if req.Session == "" {
		if err := a.deps.Docker.Attach(ctx, dockerapi.AttachRequest{
			Container: name,
			TTY:       req.TTY,
			Streams:   req.Streams,
		}); err != nil {
			return fail(err)
		}
		return ExecResult{}, nil
	}

	if !tmuxui.SessionNameRe.MatchString(req.Session) {
		return fail(fmt.Errorf("%w: session name %q", domain.ErrInvalid, req.Session))
	}
	if req.Window != "" && !tmuxui.SessionNameRe.MatchString(req.Window) {
		return fail(fmt.Errorf("%w: window name %q", domain.ErrInvalid, req.Window))
	}
	ok, err := a.probeAgentSession(ctx, name)
	if err != nil {
		return fail(err)
	}
	if !ok {
		return fail(fmt.Errorf("%w: attaching a session needs the payload mounted and a tmux-capable image", domain.ErrUnavailable))
	}
	// Self-stamp the viewer join key before the long-lived exec (the
	// stamp is a no-op outside the vibe-engine server).
	a.stampViewerWindow(ctx, req.Session)
	cmd := req.ContainerCommand
	cmd.Argv = []string{"bash", model.PayloadAgentSession, "attach", req.Session}
	if req.Window != "" {
		cmd.Argv = append(cmd.Argv, req.Window)
	}
	if req.Nested {
		cmd.Env = append(cmd.Env, envfile.Entry{Key: "VIBE_NESTED", Value: "1"})
	}
	res, err := a.execIn(ctx, name, cmd)
	if err != nil {
		return fail(err)
	}
	return res, nil
}

// StopSessionRequest ends one agent session by its FULL address — the
// tui's right-click menu door (`vibe _stop`). `vibe agent --stop`
// computes the address from flags; the menu already holds the address
// the `vibe ps` join reported, and reverse-mapping it into flags would
// recompute the grammar in a second place.
type StopSessionRequest struct {
	Dir     string
	Session string
}

type StopSessionResult struct {
	Session string
}

func (a *App) StopSession(ctx context.Context, req StopSessionRequest) (StopSessionResult, error) {
	fail := opFail[StopSessionResult]("stop", "")
	rec, name, err := a.devContainer(ctx, req.Dir)
	if err != nil {
		return fail(err)
	}
	fail = opFail[StopSessionResult]("stop", rec.ID)
	// Same policy both sides of the exec (agent-session.sh kill
	// re-checks): charset-vetted, agent-convention addresses only.
	if !tmuxui.SessionNameRe.MatchString(req.Session) ||
		(req.Session != "agent" && !strings.HasPrefix(req.Session, "agent-")) {
		return fail(fmt.Errorf("%w: agent session address %q", domain.ErrInvalid, req.Session))
	}
	ok, err := a.probeAgentSession(ctx, name)
	if err != nil {
		return fail(err)
	}
	if !ok {
		return fail(fmt.Errorf("%w: stopping a session needs the payload mounted and a tmux-capable image", domain.ErrUnavailable))
	}
	res, err := a.execIn(ctx, name, ContainerCommand{
		Dir:  req.Dir,
		Argv: []string{"bash", model.PayloadAgentSession, "kill", req.Session},
	})
	if err != nil {
		return fail(err)
	}
	if res.ExitCode != 0 {
		return fail(fmt.Errorf("%w: agent-session kill exited %d", domain.ErrUnavailable, res.ExitCode))
	}
	return StopSessionResult{Session: req.Session}, nil
}

// StopServiceRequest ends one workspace-service window of the
// in-container `services` session by its svc.sh window name — the
// TUI's stop (live) and dismiss (kept corpse) verbs, which are one
// tmux operation with different intents (`vibe _svcstop`).
type StopServiceRequest struct {
	Dir  string
	Name string
}

type StopServiceResult struct {
	Name string
}

func (a *App) StopService(ctx context.Context, req StopServiceRequest) (StopServiceResult, error) {
	fail := opFail[StopServiceResult]("svcstop", "")
	rec, name, err := a.devContainer(ctx, req.Dir)
	if err != nil {
		return fail(err)
	}
	fail = opFail[StopServiceResult]("svcstop", rec.ID)
	// Same policy both sides of the exec (agent-session.sh svc-kill
	// re-checks): the shared closed charset, window names only.
	if !tmuxui.SessionNameRe.MatchString(req.Name) {
		return fail(fmt.Errorf("%w: service window name %q", domain.ErrInvalid, req.Name))
	}
	ok, err := a.probeAgentSession(ctx, name)
	if err != nil {
		return fail(err)
	}
	if !ok {
		return fail(fmt.Errorf("%w: stopping a service needs the payload mounted and a tmux-capable image", domain.ErrUnavailable))
	}
	res, err := a.execIn(ctx, name, ContainerCommand{
		Dir:  req.Dir,
		Argv: []string{"bash", model.PayloadAgentSession, "svc-kill", req.Name},
	})
	if err != nil {
		return fail(err)
	}
	if res.ExitCode != 0 {
		return fail(fmt.Errorf("%w: agent-session svc-kill exited %d", domain.ErrUnavailable, res.ExitCode))
	}
	return StopServiceResult{Name: req.Name}, nil
}

// SelectServiceRequest re-aims the in-container `services` session at
// one svc window (`vibe _svcselect`) — the sidebar's service-row click
// when a shared viewer is already open: the current window is session
// state, so every attached client follows.
type SelectServiceRequest struct {
	Dir  string
	Name string
}

type SelectServiceResult struct {
	Name string
}

func (a *App) SelectService(ctx context.Context, req SelectServiceRequest) (SelectServiceResult, error) {
	fail := opFail[SelectServiceResult]("svcselect", "")
	rec, name, err := a.devContainer(ctx, req.Dir)
	if err != nil {
		return fail(err)
	}
	fail = opFail[SelectServiceResult]("svcselect", rec.ID)
	if !tmuxui.SessionNameRe.MatchString(req.Name) {
		return fail(fmt.Errorf("%w: service window name %q", domain.ErrInvalid, req.Name))
	}
	ok, err := a.probeAgentSession(ctx, name)
	if err != nil {
		return fail(err)
	}
	if !ok {
		return fail(fmt.Errorf("%w: selecting a service needs the payload mounted and a tmux-capable image", domain.ErrUnavailable))
	}
	res, err := a.execIn(ctx, name, ContainerCommand{
		Dir:  req.Dir,
		Argv: []string{"bash", model.PayloadAgentSession, "svc-select", req.Name},
	})
	if err != nil {
		return fail(err)
	}
	if res.ExitCode != 0 {
		return fail(fmt.Errorf("%w: agent-session svc-select exited %d", domain.ErrUnavailable, res.ExitCode))
	}
	return SelectServiceResult{Name: req.Name}, nil
}

// DismissSessionRequest clears a DEAD session's state record — the
// sidebar right-click's "seen it" gesture (`vibe _dismiss`). The ✗
// roster row exists only as that record; the container side refuses
// running sessions, so dismiss can never eat live truth.
type DismissSessionRequest struct {
	Dir     string
	Session string
}

type DismissSessionResult struct {
	Session string
}

func (a *App) DismissSession(ctx context.Context, req DismissSessionRequest) (DismissSessionResult, error) {
	fail := opFail[DismissSessionResult]("dismiss", "")
	rec, name, err := a.devContainer(ctx, req.Dir)
	if err != nil {
		return fail(err)
	}
	fail = opFail[DismissSessionResult]("dismiss", rec.ID)
	if !tmuxui.SessionNameRe.MatchString(req.Session) ||
		(req.Session != "agent" && !strings.HasPrefix(req.Session, "agent-")) {
		return fail(fmt.Errorf("%w: agent session address %q", domain.ErrInvalid, req.Session))
	}
	ok, err := a.probeAgentSession(ctx, name)
	if err != nil {
		return fail(err)
	}
	if !ok {
		return fail(fmt.Errorf("%w: dismissing a session needs the payload mounted and a tmux-capable image", domain.ErrUnavailable))
	}
	res, err := a.execIn(ctx, name, ContainerCommand{
		Dir:  req.Dir,
		Argv: []string{"bash", model.PayloadAgentSession, "dismiss", req.Session},
	})
	if err != nil {
		return fail(err)
	}
	if res.ExitCode != 0 {
		return fail(fmt.Errorf("%w: agent-session dismiss exited %d", domain.ErrUnavailable, res.ExitCode))
	}
	return DismissSessionResult{Session: req.Session}, nil
}

// agentSessionAddress mirrors agent-session.sh's address grammar —
// agent(-cmd)(-name)(-cold) — for the one host-side consumer that
// needs the address before the container ever runs: the viewer
// window's @vibe_session self-stamp. The script stays the authority
// (it composes the display twin too); this mirror is pinned by test
// and by the chooser porcelain, whose verdicts already speak the same
// addresses.
func agentSessionAddress(req AgentRequest) string {
	addr := "agent"
	if req.Agent != "" {
		addr += "-" + req.Agent
	}
	if req.Session != "" {
		addr += "-" + req.Session
	}
	if req.Cold {
		addr += "-cold"
	}
	return addr
}

// stampViewerWindow marks the tmux window this engine process runs
// inside with the inner session address it carries (@vibe_session —
// the viewer join key the ghost cells, sidebar rows, and chooser
// verdicts dedup on). Self-stamping is the ONE definition for every
// launch door: the tui's startup window, the chooser's launch items,
// the palette's restart, and agent-open viewers all run `vibe agent`
// or `vibe attach` in their pane. Waiting for the agent's own title
// events instead leaves hookless CLIs — and a freshly restored claude
// that has not spoken yet — invisible to the join.
//
// deps.ViewerPane is empty outside the vibe-engine server
// (tmux.SelfPane), so foreign tmux panes are never stamped.
func (a *App) stampViewerWindow(ctx context.Context, session string) {
	if a.deps.Tmux == nil || session == "" || a.deps.ViewerPane == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	_ = a.deps.Tmux.SetWindowOption(ctx, a.deps.ViewerPane, "@vibe_session", session)
}

// devContainer resolves the project and requires its dev container to
// be running.
func (a *App) devContainer(ctx context.Context, dir string) (registry.Record, dockerapi.ContainerName, error) {
	_, rec, err := a.resolveProject(ctx, dir)
	if err != nil {
		return registry.Record{}, "", err
	}
	name, err := a.requireDevContainer(ctx, rec)
	if err != nil {
		return registry.Record{}, "", err
	}
	return rec, name, nil
}

// requireDevContainer requires an already-resolved project's dev
// container to be running.
func (a *App) requireDevContainer(ctx context.Context, rec registry.Record) (dockerapi.ContainerName, error) {
	name := dockerapi.ContainerName(model.DevContainerName(rec.ID))
	state, err := a.deps.Docker.InspectContainer(ctx, name)
	if err != nil {
		return "", fmt.Errorf("dev container: %w (run `vibe up`)", err)
	}
	if !state.Running {
		return "", fmt.Errorf("%w: dev container %s is not running (run `vibe up`)", domain.ErrUnavailable, name)
	}
	return name, nil
}

func (a *App) execIn(ctx context.Context, name dockerapi.ContainerName, cmd ContainerCommand) (ExecResult, error) {
	if len(cmd.Argv) == 0 {
		return ExecResult{}, fmt.Errorf("%w: no command given", domain.ErrInvalid)
	}
	user := cmd.User
	if user == "" {
		user = devContainerUser
	}
	// The container is created without a working directory, so a bare
	// docker exec inherits the image's WORKDIR (e.g. /go on Go bases).
	// Every engine workload starts in the workspace instead — v1's
	// `cd $REPO_ROOT` rule, engine-side; `vibe exec -w` still overrides.
	if cmd.Workdir == "" {
		cmd.Workdir = model.WorkspaceTarget
	}
	env := make([]string, 0, len(cmd.Env))
	for _, e := range cmd.Env {
		env = append(env, e.Key+"="+e.Value)
	}
	res, err := a.deps.Docker.Exec(ctx, dockerapi.ExecRequest{
		Container: name,
		User:      user,
		WorkDir:   cmd.Workdir,
		Env:       env,
		Argv:      cmd.Argv,
		TTY:       cmd.TTY,
		Stdin:     cmd.Stdin,
		Streams:   cmd.Streams,
	})
	if err != nil {
		return ExecResult{}, err
	}
	return ExecResult{ExitCode: res.ExitCode}, nil
}

// LogsRequest streams container logs: the dev container by default, a
// named sidecar with Service. Log bytes are container output and reach
// the terminal raw — the same residual as exec and attach, never part
// of an approval surface.
type LogsRequest struct {
	Dir     string
	Service string
	Follow  bool
	Tail    int
	Streams dockerapi.Streams
}

func (a *App) Logs(ctx context.Context, req LogsRequest) error {
	_, rec, err := a.resolveProject(ctx, req.Dir)
	if err != nil {
		return opError("logs", "", err)
	}
	name := model.DevContainerName(rec.ID)
	if req.Service != "" {
		if !schema.ValidServiceName(req.Service) {
			return opError("logs", rec.ID, fmt.Errorf("%w: service name %q", domain.ErrInvalid, req.Service))
		}
		name = model.SidecarContainerName(rec.ID, req.Service)
	}
	if err := a.deps.Docker.Logs(ctx, dockerapi.LogsRequest{
		Container: dockerapi.ContainerName(name),
		Follow:    req.Follow,
		Tail:      req.Tail,
		Streams:   req.Streams,
	}); err != nil {
		return opError("logs", rec.ID, err)
	}
	return nil
}

// approvedEnvFile loads env entries from the approved candidate's
// frozen snapshot; a project without an approved candidate or without
// an env file yields none.
func (a *App) approvedEnvFile(ctx context.Context, rec registry.Record) ([]envfile.Entry, error) {
	if rec.Approved == nil {
		return nil, nil
	}
	candRecord, err := a.deps.Store.ReadCandidateRecord(*rec.Approved)
	if err != nil {
		return nil, err
	}
	if candRecord.Snapshot.IsZero() {
		return nil, nil
	}
	lease, err := a.deps.Store.Open(ctx, store.SnapshotObject, candRecord.Snapshot)
	if err != nil {
		return nil, err
	}
	defer lease.Close()
	f, err := os.Open(filepath.Join(lease.Object.Path, model.SnapshotEnvFilePath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	return envfile.Parse(f, envfile.Limits{})
}

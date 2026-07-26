package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/chrisdruta/vibe-tui-box/internal/dockerapi"
	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/envfile"
	"github.com/chrisdruta/vibe-tui-box/internal/model"
	"github.com/chrisdruta/vibe-tui-box/internal/registry"
	"github.com/chrisdruta/vibe-tui-box/internal/schema"
	"github.com/chrisdruta/vibe-tui-box/internal/store"
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
// session (e.g. the `services` session lifecycle hooks populate).
type AttachRequest struct {
	ContainerCommand
	Session string
}

// sessionNameRe matches the shared inner-session charset (tmux session
// names, state-file names, the title channel).
var sessionNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

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

	if !sessionNameRe.MatchString(req.Session) {
		return fail(fmt.Errorf("%w: session name %q", domain.ErrInvalid, req.Session))
	}
	ok, err := a.probeAgentSession(ctx, name)
	if err != nil {
		return fail(err)
	}
	if !ok {
		return fail(fmt.Errorf("%w: attaching a session needs the payload mounted and a tmux-capable image", domain.ErrUnavailable))
	}
	cmd := req.ContainerCommand
	cmd.Argv = []string{"bash", model.PayloadAgentSession, "attach", req.Session}
	res, err := a.execIn(ctx, name, cmd)
	if err != nil {
		return fail(err)
	}
	return res, nil
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

package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/chrisdruta/vibe-tui-box/internal/dockerapi"
	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/envfile"
	"github.com/chrisdruta/vibe-tui-box/internal/model"
	"github.com/chrisdruta/vibe-tui-box/internal/paths"
	"github.com/chrisdruta/vibe-tui-box/internal/registry"
	"github.com/chrisdruta/vibe-tui-box/internal/runtime"
	"github.com/chrisdruta/vibe-tui-box/internal/schema"
	"github.com/chrisdruta/vibe-tui-box/internal/store"
	"github.com/chrisdruta/vibe-tui-box/internal/tmux"
	"github.com/chrisdruta/vibe-tui-box/internal/tmuxui"
)

// AgentRequest runs the agent CLI in the dev container. Agent
// overrides the manifest's agent.cmd for this invocation (it must
// still be listed in image.agents); Cold and Session pass through to
// the container-side session script. Nested marks the exec as spawned
// under `vibe tui` (the conf exports VIBE_NESTED=1) so the inner tmux
// client is reapable when the UI dies.
type AgentRequest struct {
	ContainerCommand
	Cold    bool
	Agent   string
	Session string
	Nested  bool
	// Stop ends the addressed persistent session instead of attaching;
	// Restart replaces it (end, then a fresh launch). Both are
	// carrier-only: without agent.tmux there is no session to address.
	Stop    bool
	Restart bool
}

// agentTmuxPath is the probe target for the session carrier: the tools
// recipe installs distro tmux, so apt's path is deterministic.
const agentTmuxPath = "/usr/bin/tmux"

// Agent runs the agent CLI in the dev container with the frozen env
// file — the tmux session's main window command. With agent.tmux set
// and a tmux-capable image, the CLI is wrapped in the payload's
// agent-session.sh so the conversation survives its viewer
// (docs/architecture.md (agent sessions)); otherwise it execs directly, exactly
// the pre-session behavior. The engine passes real argv throughout —
// the one tmux shell-string quoting layer lives at the bottom of the
// script.
func (a *App) Agent(ctx context.Context, req AgentRequest) (ExecResult, error) {
	root, rec, err := a.resolveProject(ctx, req.Dir)
	if err != nil {
		return ExecResult{}, &domain.OpError{Op: "agent", Err: err}
	}
	fail := func(err error) (ExecResult, error) {
		return ExecResult{}, &domain.OpError{Op: "agent", Project: rec.ID, Err: err}
	}
	doc, err := loadManifestFile(filepath.Join(root.Path, paths.ManifestRelPath))
	if err != nil {
		return fail(err)
	}
	if ferrs := doc.Validate(); len(ferrs) > 0 {
		return fail(fieldErrs(ferrs))
	}
	agentCmd := string(doc.Manifest.Agent.Cmd)
	if req.Agent != "" {
		if !slices.Contains(doc.Manifest.Image.Agents, schema.AgentKind(req.Agent)) {
			return fail(fmt.Errorf("%w: agent %q is not listed in image.agents", domain.ErrInvalid, req.Agent))
		}
		agentCmd = req.Agent
	}
	name, err := a.requireDevContainer(ctx, rec)
	if err != nil {
		return fail(err)
	}
	session := doc.Manifest.Agent.Tmux
	if session {
		ok, err := a.probeAgentSession(ctx, name)
		if err != nil {
			return fail(err)
		}
		session = ok
	}
	cmd := req.ContainerCommand
	if session {
		scriptMode := "agent"
		if req.Stop {
			scriptMode = "stop"
		}
		cmd.Argv = []string{"bash", model.PayloadAgentSession, scriptMode}
		if req.Cold {
			cmd.Argv = append(cmd.Argv, "--cold")
		}
		if req.Agent != "" {
			cmd.Argv = append(cmd.Argv, "-a")
		}
		if req.Session != "" {
			cmd.Argv = append(cmd.Argv, "-s", req.Session)
		}
		if req.Restart {
			cmd.Argv = append(cmd.Argv, "--restart")
		}
		cmd.Argv = append(cmd.Argv, "--", agentCmd)
	} else {
		// The cold/session/stop variants only exist inside the carrier.
		if req.Cold || req.Session != "" || req.Stop || req.Restart {
			return fail(fmt.Errorf("%w: --cold, -s, --stop, and --restart need agent.tmux and a tmux-capable image", domain.ErrUnavailable))
		}
		cmd.Argv = []string{agentCmd}
	}
	// A stop exec only addresses the inner tmux server — the frozen env
	// file (secrets) has no business riding along on a kill.
	var entries []envfile.Entry
	if !req.Stop {
		entries, err = a.approvedEnvFile(ctx, rec)
		if err != nil {
			return fail(err)
		}
	}
	// Identity for container-side scripts, which never parse workspace
	// files for it: appended after the env file so it cannot be shadowed.
	// agent.memory rides along the same way (read live like agent.tmux,
	// so it never touches candidate digests); absent means off.
	memory := doc.Manifest.Agent.Memory
	if memory == "" {
		memory = schema.MemoryOff
	}
	cmd.Env = append(append(entries, cmd.Env...),
		envfile.Entry{Key: "VIBE_PROJECT", Value: string(rec.ID)},
		envfile.Entry{Key: "VIBE_PROJECT_NAME", Value: rec.DisplayName},
		envfile.Entry{Key: "VIBE_AGENT_MEMORY", Value: string(memory)},
	)
	if req.Nested {
		cmd.Env = append(cmd.Env, envfile.Entry{Key: "VIBE_NESTED", Value: "1"})
	}
	res, err := a.execIn(ctx, name, cmd)
	if err != nil {
		return fail(err)
	}
	return res, nil
}

// probeAgentSession reports whether the dev container can carry the
// agent session: tmux in the image and the payload mounted. One
// fixed-argv exec covers both; base-image projects and pre-tmux tools
// images fail it and get today's direct exec.
func (a *App) probeAgentSession(ctx context.Context, name dockerapi.ContainerName) (bool, error) {
	res, err := a.deps.Docker.Exec(ctx, dockerapi.ExecRequest{
		Container: name,
		User:      devContainerUser,
		Argv:      []string{"test", "-x", agentTmuxPath, "-a", "-r", model.PayloadAgentSession},
	})
	if err != nil {
		return false, err
	}
	return res.ExitCode == 0, nil
}

// TuiRequest opens (or joins) the project's tmux session: one window
// running `vibe agent`, with the engine state in the status line.
type TuiRequest struct {
	Dir string
}

func (a *App) Tui(ctx context.Context, req TuiRequest) error {
	if a.deps.Tmux == nil {
		return &domain.OpError{Op: "tui", Err: fmt.Errorf("%w: tmux not found on PATH", domain.ErrUnavailable)}
	}
	root, rec, err := a.resolveProject(ctx, req.Dir)
	if err != nil {
		return &domain.OpError{Op: "tui", Err: err}
	}
	session := tmux.SessionFor(rec.ID)
	// The morning path: start what was already approved before any tmux
	// session exists, so the agent pane never races a stopped container
	// into a dead server. When starting is impossible but a session
	// already exists, attach anyway — scrollback and the status line
	// tell that story better than a refusal; with no session either,
	// the engine error (in the caller's terminal) beats the race.
	if err := a.startApproved(ctx, rec); err != nil {
		if ok, hasErr := a.deps.Tmux.HasSession(ctx, session); hasErr != nil || !ok {
			return &domain.OpError{Op: "tui", Project: rec.ID, Err: err}
		}
	}
	conf, payloadHostDir, err := a.materializeTuiConf(ctx, rec)
	if err != nil {
		return &domain.OpError{Op: "tui", Project: rec.ID, Err: err}
	}
	if conf != "" {
		a.deps.Tmux.ConfigureServer(conf)
	}
	if err := a.deps.Tmux.EnsureSession(ctx, tmux.SessionSpec{
		ID:      session,
		Workdir: root.Path,
		Window:  "agent",
		Command: []string{a.deps.Executable, "agent"},
	}); err != nil {
		return &domain.OpError{Op: "tui", Project: rec.ID, Err: err}
	}
	status := fmt.Sprintf("#(%s _state --project %s) %s", a.deps.Executable, rec.ID, rec.DisplayName)
	if conf != "" {
		// A server that predates this conf (conf applies at server start
		// only) still gets current paths stamped onto it.
		if err := a.deps.Tmux.SetEnvironment(ctx, "VIBE_TUI_CONF", conf); err != nil {
			return &domain.OpError{Op: "tui", Project: rec.ID, Err: err}
		}
		if err := a.deps.Tmux.SetGlobalOption(ctx, "@vibe_exe", a.deps.Executable); err != nil {
			return &domain.OpError{Op: "tui", Project: rec.ID, Err: err}
		}
		// The conf's host scripts (sidebar/dock/clip) resolve through this
		// store-owned payload dir — never workspace files.
		if err := a.deps.Tmux.SetGlobalOption(ctx, "@vibe_payload_dir", payloadHostDir); err != nil {
			return &domain.OpError{Op: "tui", Project: rec.ID, Err: err}
		}
		// The sidebar shows display names; session names stay ID-derived.
		if err := a.deps.Tmux.SetOption(ctx, session, "@vibe_name", rec.DisplayName); err != nil {
			return &domain.OpError{Op: "tui", Project: rec.ID, Err: err}
		}
		// The FULL project ID (session names carry a truncated one), so
		// host scripts can address the engine renderers per session
		// (`vibe _sidebar --project …`) without a reverse lookup.
		if err := a.deps.Tmux.SetOption(ctx, session, "@vibe_project", string(rec.ID)); err != nil {
			return &domain.OpError{Op: "tui", Project: rec.ID, Err: err}
		}
		// The tray's right cluster: the v1 prefix/copy flashes, the
		// clickable engine-state cell (range "req" → request list in the
		// conf's mouse dispatch), and the clock. The display name stays
		// out — the sidebar and the OS title carry identity; the bar
		// never repeats it.
		status = fmt.Sprintf("#{?client_prefix,#[fg=#{@thm_coral}#,bold]⌨  ,}#{?pane_in_mode,#[fg=#{@thm_yellow}#,bold]copy ,}#[default]"+
			"#[range=user|req]#(%s _state --project %s)#[norange] #[fg=#{@thm_dim}]%%H:%%M#[default] ",
			a.deps.Executable, rec.ID)
	}
	if err := a.deps.Tmux.SetOption(ctx, session, "status-right", status); err != nil {
		return &domain.OpError{Op: "tui", Project: rec.ID, Err: err}
	}
	if err := a.deps.Tmux.Attach(ctx, session); err != nil {
		return &domain.OpError{Op: "tui", Project: rec.ID, Err: err}
	}
	// Attach returns on detach AND on quit. Only a quit (the session is
	// gone, so every nested docker-exec client is dead by definition)
	// reaps the ghost viewers left inside the container — on a plain
	// detach the panes still run and their inner clients are live.
	if ok, err := a.deps.Tmux.HasSession(ctx, session); err == nil && !ok {
		a.reapAgentClients(ctx, rec)
	}
	return nil
}

// startApproved reconciles the project to its already-approved
// candidate — the `vibe tui` pre-flight. Unlike Up there is no input
// freeze and the approved pointer never moves: what was approved is
// exactly what starts, so a changed manifest stays a deliberate
// `vibe up`. Already running and in sync short-circuits to one status
// listing.
func (a *App) startApproved(ctx context.Context, rec registry.Record) error {
	if rec.Approved == nil {
		return fmt.Errorf("%w: no approved candidate to start (run `vibe up` first)", domain.ErrUnavailable)
	}
	if err := a.deps.Docker.Ping(ctx); err != nil {
		return err
	}
	if state, err := a.runtime.Status(ctx, rec); err == nil && state.Running() {
		return nil
	}
	cand, lease, err := a.runtime.LoadCandidate(ctx, *rec.Approved)
	if err != nil {
		return err
	}
	defer lease.Close()
	if _, err := a.runtime.Up(ctx, cand, runtime.UpOptions{
		Progress:     a.deps.Progress,
		LifecycleOut: a.deps.LifecycleOut,
	}); err != nil {
		return err
	}
	a.bumpTuiSerial(ctx)
	return nil
}

// reapAgentClients detaches VIBE_NESTED ghost tmux clients inside the
// dev container after the tui died (docs/architecture.md (agent sessions) reap
// mode). Best-effort by contract: no container, no payload, no tmux —
// nothing to reap, nothing to report. Agents keep running.
func (a *App) reapAgentClients(ctx context.Context, rec registry.Record) {
	name, err := a.requireDevContainer(ctx, rec)
	if err != nil {
		return
	}
	_, _ = a.deps.Docker.Exec(ctx, dockerapi.ExecRequest{
		Container: name,
		User:      devContainerUser,
		Argv:      []string{"bash", model.PayloadAgentSession, "reap"},
	})
}

// materializeTuiConf copies the pinned artifact's tmux conf into engine
// state with a stamped prologue (engine binary path, reload path) and
// returns it beside the artifact's payload host dir, which the conf's
// scripts resolve through (@vibe_payload_dir). An artifact without the
// conf — or no artifact at all — yields "" and the TUI runs bare, as
// before the conf existed.
func (a *App) materializeTuiConf(ctx context.Context, rec registry.Record) (string, string, error) {
	artifact, release, err := a.loadArtifact(ctx, rec)
	if err != nil {
		return "", "", err
	}
	defer release()
	if artifact.IsZero() {
		return "", "", nil
	}
	hostDir := filepath.Join(artifact.PayloadPath(), "host")
	src, err := os.ReadFile(filepath.Join(hostDir, "tmux-tui.conf"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", nil
		}
		return "", "", err
	}
	path := filepath.Join(a.deps.Layout.State, "tui", "tmux-tui.conf")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", "", err
	}
	prologue := fmt.Sprintf("# Materialized by `vibe tui` from artifact %s; regenerated every run.\n"+
		"set-environment -g VIBE_TUI_CONF \"%s\"\nset -g @vibe_exe \"%s\"\nset -g @vibe_payload_dir \"%s\"\n\n",
		artifact.Record.Digest, path, a.deps.Executable, hostDir)
	// The sanctioned customization point (docs/tui-layout.md): the user
	// conf loads after the payload body so it wins, -q keeps a missing
	// file silent, and the store-owned conf is never forked. Home is the
	// layout root's parent — the app reads no ambient environment.
	userConf := filepath.Join(filepath.Dir(a.deps.Layout.Root), ".config", "vibe", "tui.conf")
	epilogue := fmt.Sprintf("\n# User overrides, applied last (docs/tui-layout.md).\nsource-file -q %q\n", userConf)
	body := append([]byte(prologue), src...)
	body = append(body, []byte(epilogue)...)
	if err := store.WriteFileAtomic(path, body, 0o600); err != nil {
		return "", "", err
	}
	return path, hostDir, nil
}

// projectView assembles the pure view model the tmux renderers consume.
func (a *App) projectView(ctx context.Context, rec registry.Record) tmuxui.ProjectView {
	view := tmuxui.ProjectView{
		ID:      string(rec.ID),
		Name:    rec.DisplayName,
		Mode:    string(rec.Mode),
		Version: rec.ReleaseVersion,
	}
	if state, err := a.runtime.Status(ctx, rec); err == nil {
		for _, c := range state.Containers {
			view.Containers = append(view.Containers, tmuxui.ContainerView{
				Role:    c.Role,
				Running: c.Running,
				InSync:  c.InSync,
			})
		}
	}
	if bs, err := a.brokerStore(rec.ID); err == nil {
		if pending, err := bs.ListPending(); err == nil {
			view.Pending = len(pending)
		}
	}
	return view
}

// RenderRequest drives the hidden renderer commands. Project selects a
// record by ID; when empty the current directory's project is used.
type RenderRequest struct {
	Dir     string
	Project domain.ProjectID
	Width   int
}

type RenderResult struct {
	Lines []string
}

func (a *App) renderProject(ctx context.Context, req RenderRequest) (registry.Record, error) {
	if req.Project != "" {
		return a.deps.Registry.Get(ctx, req.Project)
	}
	_, rec, err := a.resolveProject(ctx, req.Dir)
	return rec, err
}

func (a *App) RenderSidebar(ctx context.Context, req RenderRequest) (RenderResult, error) {
	rec, err := a.renderProject(ctx, req)
	if err != nil {
		return RenderResult{}, &domain.OpError{Op: "_sidebar", Err: err}
	}
	return RenderResult{Lines: tmuxui.Sidebar(a.projectView(ctx, rec), req.Width)}, nil
}

func (a *App) RenderState(ctx context.Context, req RenderRequest) (RenderResult, error) {
	rec, err := a.renderProject(ctx, req)
	if err != nil {
		return RenderResult{}, &domain.OpError{Op: "_state", Err: err}
	}
	return RenderResult{Lines: []string{tmuxui.State(a.projectView(ctx, rec))}}, nil
}

func (a *App) RenderFleet(ctx context.Context, req RenderRequest) (RenderResult, error) {
	records, err := a.deps.Registry.List(ctx)
	if err != nil {
		return RenderResult{}, &domain.OpError{Op: "_fleet", Err: err}
	}
	views := make([]tmuxui.ProjectView, 0, len(records))
	for _, rec := range records {
		views = append(views, a.projectView(ctx, rec))
	}
	return RenderResult{Lines: tmuxui.Fleet(views, req.Width)}, nil
}

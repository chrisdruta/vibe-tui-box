package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

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

// AgentMode selects what an Agent call does with the addressed
// persistent session: attach (the zero value), stop it, or replace it.
// One mode instead of two bools so the invalid stop+restart combo is
// unrepresentable (branch-review remainder item 4) — the CLI's
// mutual-exclusion check is a flag-surface nicety, not the guard.
type AgentMode string

const (
	AgentAttach  AgentMode = "" // reattach or launch — the default
	AgentStop    AgentMode = "stop"
	AgentRestart AgentMode = "restart"
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
	// Mode stops or replaces the addressed persistent session instead
	// of attaching. Non-attach modes are carrier-only: without
	// agent.tmux there is no session to address.
	Mode AgentMode
}

// Probe targets for the session carrier: the tools recipe builds the
// engine-pinned tmux at /usr/local/bin (which precedes /usr/bin in
// PATH, so it also wins at exec time); the apt path keeps images built
// before the pin probing true until their next rebuild.
const (
	pinnedTmuxPath = "/usr/local/bin/tmux"
	distroTmuxPath = "/usr/bin/tmux"
)

// Agent runs the agent CLI in the dev container with the frozen env
// file — the tmux session's main window command. With agent.tmux set
// and a tmux-capable image, the CLI is wrapped in the payload's
// agent-session.sh so the conversation survives its viewer
// (docs/architecture.md (agent sessions)); otherwise it execs directly, exactly
// the pre-session behavior. The engine passes real argv throughout —
// the one tmux shell-string quoting layer lives at the bottom of the
// script.
func (a *App) Agent(ctx context.Context, req AgentRequest) (ExecResult, error) {
	fail := opFail[ExecResult]("agent", "")
	root, rec, err := a.resolveProject(ctx, req.Dir)
	if err != nil {
		return fail(err)
	}
	fail = opFail[ExecResult]("agent", rec.ID)
	doc, err := loadManifestFile(filepath.Join(root.Path, paths.ManifestRelPath))
	if err != nil {
		return fail(err)
	}
	if ferrs := doc.Validate(); len(ferrs) > 0 {
		return fail(fieldErrs(ferrs))
	}
	agentCmd := string(doc.Manifest.Agent.Cmd)
	if req.Agent != "" {
		listed := slices.ContainsFunc(doc.Manifest.Image.Agents, func(a schema.AgentSpec) bool {
			return a.Kind == schema.AgentKind(req.Agent)
		})
		if !listed {
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
		// Self-stamp the viewer join key before the long-lived exec: a
		// stop runs in a popup, everything else IS this window's
		// session view.
		if req.Mode != AgentStop {
			a.stampViewerWindow(ctx, agentSessionAddress(req))
		}
		// The wire format is the script's asymmetric grammar: stop is a
		// MODE, restart is a FLAG on agent mode (agent-session.sh usage)
		// — the one-mode request fans back out here.
		scriptMode := "agent"
		if req.Mode == AgentStop {
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
		if req.Mode == AgentRestart {
			cmd.Argv = append(cmd.Argv, "--restart")
		}
		cmd.Argv = append(cmd.Argv, "--", agentCmd)
	} else {
		// The cold/session/stop variants only exist inside the carrier.
		if req.Cold || req.Session != "" || req.Mode != AgentAttach {
			return fail(fmt.Errorf("%w: --cold, -s, --stop, and --restart need agent.tmux and a tmux-capable image", domain.ErrUnavailable))
		}
		cmd.Argv = []string{agentCmd}
	}
	// A stop exec only addresses the inner tmux server — the frozen env
	// file (secrets) has no business riding along on a kill.
	var entries []envfile.Entry
	if req.Mode != AgentStop {
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
		Argv: []string{"test", "-r", model.PayloadAgentSession, "-a",
			"(", "-x", pinnedTmuxPath, "-o", "-x", distroTmuxPath, ")"},
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
	fail := func(err error) error { return opError("tui", "", err) }
	if a.deps.Tmux == nil {
		return fail(fmt.Errorf("%w: tmux not found on PATH", domain.ErrUnavailable))
	}
	root, rec, err := a.resolveProject(ctx, req.Dir)
	if err != nil {
		return fail(err)
	}
	fail = func(err error) error { return opError("tui", rec.ID, err) }
	session := tmux.SessionFor(rec.ID)
	// The morning path: start what was already approved before any tmux
	// session exists, so the agent pane never races a stopped container
	// into a dead server. When starting is impossible but a session
	// already exists, attach anyway — scrollback and the status line
	// tell that story better than a refusal; with no session either,
	// the engine error (in the caller's terminal) beats the race.
	if err := a.startApproved(ctx, rec); err != nil {
		if ok, hasErr := a.deps.Tmux.HasSession(ctx, session); hasErr != nil || !ok {
			return fail(err)
		}
	}
	conf, payloadHostDir, err := a.materializeTuiConf(ctx, rec, a.deps.Executable)
	if err != nil {
		return fail(err)
	}
	if conf != "" {
		a.deps.Tmux.ConfigureServer(conf)
	}
	// Whether a server already runs decides the heal below: -f applies
	// the conf only at server START, so joining a live server would
	// keep every binding from whatever engine started it. Checked
	// before EnsureSession mints our session.
	preexisting, lsErr := a.deps.Tmux.ListSessions(ctx)
	heal := conf != "" && lsErr == nil && len(preexisting) > 0
	if err := a.mintSession(ctx, rec, root.Path, conf != ""); err != nil {
		return fail(err)
	}
	if conf != "" {
		// A server that predates this conf (conf applies at server start
		// only) still gets current paths stamped onto it. These global
		// stamps come AFTER the mint: a set-option against a serverless
		// tmux would start a session-less server that exits immediately,
		// losing everything stamped on it.
		if err := a.deps.Tmux.SetEnvironment(ctx, "VIBE_TUI_CONF", conf); err != nil {
			return fail(err)
		}
		if err := a.deps.Tmux.SetGlobalOption(ctx, "@vibe_exe", a.deps.Executable); err != nil {
			return fail(err)
		}
		// The conf's host scripts (sidebar/dock/clip) resolve through this
		// store-owned payload dir — never workspace files.
		if err := a.deps.Tmux.SetGlobalOption(ctx, "@vibe_payload_dir", payloadHostDir); err != nil {
			return fail(err)
		}
		// The attach-time heal (2026-07-28): a pre-existing server keeps
		// the conf it was born with — stale bindings after every dev
		// cycle until someone remembers prefix+R. Re-source the fresh
		// conf on every join to a live server; the generated conf is
		// reload-idempotent (-o option defaults) by its own prefix+R
		// contract. Sidebar render loops heal themselves the same way
		// (sidebar.sh watches @vibe_payload_dir drift on the slow tick).
		if heal {
			if err := a.deps.Tmux.SourceFile(ctx, conf); err != nil {
				return fail(err)
			}
		}
	}
	if err := a.deps.Tmux.Attach(ctx, session); err != nil {
		return fail(err)
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

// mintSession is the session-scoped half of opening a project's UI:
// ensure the detached session exists (EnsureSession converges when a
// concurrent minter wins the race) and stamp everything session-owned —
// identity options and the status-right. Shared by `vibe tui` (which
// then attaches) and OpenProject (which does not). Deliberately NOT the
// global half — conf materialization, @vibe_exe/@vibe_payload_dir, the
// attach heal — which belongs to joins (Tui) and shim handoffs
// (restampTui): OpenProject is only reachable from a live, configured
// server. chrome is false for the bare no-artifact tui.
func (a *App) mintSession(ctx context.Context, rec registry.Record, rootPath string, chrome bool) error {
	session := tmux.SessionFor(rec.ID)
	if err := a.deps.Tmux.EnsureSession(ctx, tmux.SessionSpec{
		ID:      session,
		Workdir: rootPath,
		Window:  "agent",
		Command: []string{a.deps.Executable, "agent"},
	}); err != nil {
		return err
	}
	// The display name is operator input landing in a format string
	// where #() executes commands; escape it like any other data. No
	// space between the splice and the name: a non-empty state cell
	// brings its own trailing separator, and nominal renders nothing.
	// The bare form splices this process's executable directly (no conf
	// means no @vibe_exe stamped to resolve through).
	status := fmt.Sprintf("#(%s _state --project %s)%s", a.deps.Executable, rec.ID, tmux.EscapeFormat(rec.DisplayName))
	if chrome {
		// The sidebar shows display names; session names stay ID-derived.
		if err := a.deps.Tmux.SetOption(ctx, session, "@vibe_name", rec.DisplayName); err != nil {
			return err
		}
		// The FULL project ID (session names carry a truncated one), so
		// host scripts can address the engine renderers per session
		// (`vibe _sidebar --project …`) without a reverse lookup.
		if err := a.deps.Tmux.SetOption(ctx, session, "@vibe_project", string(rec.ID)); err != nil {
			return err
		}
		// The tray's right cluster: the v1 prefix/copy flashes, the
		// clickable engine-state cell (range "req" → request list in the
		// conf's mouse dispatch), and the 12-hour clock (2026-07-31; %l
		// space-pads the hour — stable width — and unlike glibc-only
		// %-I it exists in darwin's strftime too). The state cell owns
		// the dim ` · ` toward the clock (views.go State: nominal is
		// EMPTY, so a static separator would dangle — this supersedes
		// the 2026-07-26 always-on separator, whose dim `·` beside the
		// always-on ● read as two dots). The display name stays
		// out — the sidebar and the OS title carry identity; the bar
		// never repeats it. The splice resolves the engine THROUGH
		// @vibe_exe (formats expand inside #() before the job runs, and
		// the job re-keys on the expanded command), so the cell follows
		// every shim handoff without a per-session restamp — and both
		// minting paths (tui join, sidebar _open) stamp the identical
		// string, so re-minting never flip-flops the option.
		status = fmt.Sprintf("#{?client_prefix,#[fg=#{@thm_coral}#,bold]⌨#[default]#[fg=#{@thm_dim}] · #[default],}"+
			"#{?pane_in_mode,#[fg=#{@thm_yellow}#,bold]copy#[default]#[fg=#{@thm_dim}] · #[default],}"+
			"#[range=user|req]#(#{@vibe_exe} _state --project %s)#[norange]#[fg=#{@thm_dim}]%%l:%%M %%p#[default] ",
			rec.ID)
	}
	return a.deps.Tmux.SetOption(ctx, session, "status-right", status)
}

// OpenProjectRequest opens a project's UI session by registry ID — the
// sidebar's make-it-live click (`vibe _open`), which has no workspace
// cwd to offer and no client to attach.
type OpenProjectRequest struct {
	Project domain.ProjectID
}

type OpenProjectResult struct {
	Session tmux.SessionID
	// Approved reports the fast path: the project already had an
	// approved candidate and startApproved brought it up (or it was
	// already running). False means this call ran the FULL first
	// `up` — the caller should not auto-switch a client to a session
	// whose approval just landed minutes after the click.
	Approved bool
}

// OpenProject makes a registered project live from inside the tui: reap
// stale ghost viewers, ensure containers (the approved candidate when
// one exists, the full first `up` otherwise — delegated, so the
// approved-pointer ordering and the fail-closed interactive-approval
// path stay Up's), then mint the session without attaching. The
// sidebar's click handler switches the clicking client over on the
// approved path only.
func (a *App) OpenProject(ctx context.Context, req OpenProjectRequest) (OpenProjectResult, error) {
	fail := opFail[OpenProjectResult]("open", req.Project)
	if a.deps.Tmux == nil {
		return fail(fmt.Errorf("%w: tmux not found on PATH", domain.ErrUnavailable))
	}
	rec, err := a.deps.Registry.Get(ctx, req.Project)
	if err != nil {
		return fail(err)
	}
	// A quit that happened while another project's client was visiting
	// left this project's container-side ghost viewers unreaped (no
	// `vibe tui` process was watching); reopening is the natural heal
	// point.
	a.reapAgentClients(ctx, rec)
	approved := rec.Approved != nil
	if approved {
		if err := a.startApproved(ctx, rec); err != nil {
			return fail(err)
		}
	} else {
		up, err := a.Up(ctx, UpRequest{Project: rec.ID})
		if err != nil {
			return fail(err)
		}
		rec = up.Record
	}
	if err := a.mintSession(ctx, rec, rec.Root, true); err != nil {
		return fail(err)
	}
	a.bumpTuiSerial(ctx)
	return OpenProjectResult{Session: tmux.SessionFor(rec.ID), Approved: approved}, nil
}

// startApproved reconciles the project to its already-approved
// candidate — the `vibe tui` pre-flight. Unlike Up there is no input
// freeze and the approved pointer never moves: what was approved is
// exactly what starts, so a changed manifest stays a deliberate
// `vibe up`. Completeness is judged against the PLAN, not the state
// alone — a state listing cannot see a container that was removed
// outright. And a running container on a DIFFERENT candidate is live
// work a UI open must never destroy (reconciling would replace it,
// inner agent sessions and all): that refuses with the explicit
// commands instead.
func (a *App) startApproved(ctx context.Context, rec registry.Record) error {
	if rec.Approved == nil {
		return fmt.Errorf("%w: no approved candidate to start (run `vibe up` first)", domain.ErrUnavailable)
	}
	// No Docker pre-ping (branch-review remainder item 5): a dead
	// daemon surfaces from the reconcile's own calls with the same
	// connect error the ping would have produced — the extra round
	// trip bought nothing on the happy path.
	cand, lease, err := a.runtime.LoadCandidate(ctx, *rec.Approved)
	if err != nil {
		return err
	}
	defer lease.Close()
	if state, err := a.runtime.Status(ctx, rec); err == nil {
		for _, c := range state.Containers {
			if c.Running && !c.InSync {
				return fmt.Errorf("%w: running containers don't match the approved candidate (run `vibe up` or `vibe rebuild`)", domain.ErrConflict)
			}
		}
		if plannedRunning(cand.Plan, state) {
			return nil
		}
	}
	if _, err := a.runtime.Up(ctx, cand, runtime.UpOptions{
		Progress:     a.deps.Progress,
		LifecycleOut: a.deps.LifecycleOut,
	}); err != nil {
		return err
	}
	a.bumpTuiSerial(ctx)
	return nil
}

// plannedRunning reports whether every container the plan wants exists
// in the state, running and in sync. Stopped or stale-but-stopped
// containers fall through to the reconcile (replacing a stopped stale
// container destroys nothing).
func plannedRunning(plan model.Plan, state runtime.State) bool {
	byName := make(map[string]runtime.ContainerStatus, len(state.Containers))
	for _, c := range state.Containers {
		byName[c.Name] = c
	}
	for _, c := range append([]model.Container{plan.Dev}, plan.Services...) {
		got, ok := byName[c.Name]
		if !ok || !got.Running || !got.InSync {
			return false
		}
	}
	return true
}

// ReapSession resolves a host session name back to its registered
// project and reaps that project's ghost viewers — the conf's
// session-closed hook (`vibe _reap` via reap.sh). With
// detach-on-destroy hopping the quitting client to a sibling session,
// no `vibe tui` process observes a quit anymore, so the hook that saw
// the session die owns triggering the reap — and it covers every kill
// path (prefix+Q, choose-tree x, `vibe down`'s killUI) in one place.
// Only the truncated ID-derived name survives the session's death
// (#{hook_session_name}); resolving it through SessionFor keeps the
// truncation rule engine-owned. A name no registered project maps to
// is a quiet no-op.
func (a *App) ReapSession(ctx context.Context, session string) error {
	records, err := a.deps.Registry.List(ctx)
	if err != nil {
		return opError("reap", "", err)
	}
	for _, rec := range records {
		if tmux.SessionFor(rec.ID) == tmux.SessionID(session) {
			a.reapAgentClients(ctx, rec)
			return nil
		}
	}
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
// before the conf existed. exe is the engine path the prologue stamps
// as @vibe_exe: `vibe tui` passes its own executable, restampTui the
// freshly repointed shim symlink — the prologue must agree with the
// live options or a prefix+R re-source would revert them.
func (a *App) materializeTuiConf(ctx context.Context, rec registry.Record, exe string) (string, string, error) {
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
		artifact.Record.Digest, path, exe, hostDir)
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
	fail := opFail[RenderResult]("_sidebar", "")
	rec, err := a.renderProject(ctx, req)
	if err != nil {
		return fail(err)
	}
	return RenderResult{Lines: tmuxui.Sidebar(a.projectView(ctx, rec), req.Width)}, nil
}

func (a *App) RenderState(ctx context.Context, req RenderRequest) (RenderResult, error) {
	fail := opFail[RenderResult]("_state", "")
	rec, err := a.renderProject(ctx, req)
	if err != nil {
		return fail(err)
	}
	return RenderResult{Lines: []string{tmuxui.State(a.projectView(ctx, rec))}}, nil
}

// RenderAgents renders `vibe _agents`: every container-side agent
// session the fleet knows about, keyed to its project — the truth the
// sidebar's viewer-less rows and the tray's ghost cells join against
// tmux's own windows. Project scopes it to one record.
//
// Cost: one container exec per project whose dev container is running
// (the inspect that answers "running" is the same one _fleet already
// pays) plus one runtime.Status listing per project for the sidecar
// rows. It is a FETCH-path renderer by design — the sidebar calls it
// on the engine cadence, never per frame.
func (a *App) RenderAgents(ctx context.Context, req RenderRequest) (RenderResult, error) {
	fail := opFail[RenderResult]("_agents", "")
	records, err := a.deps.Registry.List(ctx)
	if err != nil {
		return fail(err)
	}
	var entries []tmuxui.AgentEntry
	for _, rec := range records {
		if req.Project != "" && rec.ID != req.Project {
			continue
		}
		agents, services := a.agentRows(ctx, rec)
		for _, row := range agents {
			entries = append(entries, tmuxui.AgentEntry{
				Project: string(rec.ID),
				Session: row.Name,
				State:   row.State,
				CLI:     row.CLI,
				Model:   row.Model,
				Epoch:   row.Since,
				Detail:  row.Detail,
			})
		}
		// Workspace services ride the same porcelain with the kind
		// marker; Session carries the svc window name.
		for _, row := range services {
			entries = append(entries, tmuxui.AgentEntry{
				Project: string(rec.ID),
				Session: row.Name,
				State:   row.State,
				Epoch:   row.Since,
				Kind:    tmuxui.AgentEntryKindService,
			})
		}
		// Engine sidecars ride it too (2026-07-31): Docker truth, not a
		// container feeder, so a stopped or stale sidecar stays visible
		// in the sidebar's services group even with the dev container
		// down. Session is the manifest name (role minus the prefix);
		// state folds to the running/stale/stopped vocabulary
		// SidecarStyle draws.
		if state, err := a.runtime.Status(ctx, rec); err == nil {
			for _, c := range state.Containers {
				name, ok := strings.CutPrefix(c.Role, "sidecar:")
				if !ok {
					continue
				}
				st := "running"
				switch {
				case !c.Running:
					st = "stopped"
				case !c.InSync:
					st = "stale"
				}
				entries = append(entries, tmuxui.AgentEntry{
					Project: string(rec.ID),
					Session: name,
					State:   st,
					Kind:    tmuxui.AgentEntryKindSidecar,
				})
			}
		}
	}
	return RenderResult{Lines: tmuxui.Agents(entries, req.Width)}, nil
}

func (a *App) RenderFleet(ctx context.Context, req RenderRequest) (RenderResult, error) {
	fail := opFail[RenderResult]("_fleet", "")
	records, err := a.deps.Registry.List(ctx)
	if err != nil {
		return fail(err)
	}
	views := make([]tmuxui.ProjectView, 0, len(records))
	for _, rec := range records {
		view := a.projectView(ctx, rec)
		// Churn for the branch line: only for in-use projects (a
		// running container), and only on THIS fetch-path renderer —
		// projectView is shared with `_state`, the frame-splice hot
		// path, which must never grow a subprocess.
		for _, c := range view.Containers {
			if c.Running {
				view.Churn = gitChurn(ctx, rec.Root)
				break
			}
		}
		views = append(views, view)
	}
	return RenderResult{Lines: tmuxui.Fleet(views, req.Width)}, nil
}

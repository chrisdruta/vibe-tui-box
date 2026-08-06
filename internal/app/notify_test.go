package app

import (
	"context"
	"errors"
	"testing"

	"vibe/internal/tmux"
)

// recordingTmux records the calls the engine makes against the tmux
// client. It was originally minimal (global option sets and session
// kills); the extra recorders below let the Tui tests assert the full
// interaction without changing any existing behavior other tests rely
// on — every method still returns the same values it always did.
type recordingTmux struct {
	fail    bool
	globals []struct{ Option, Value string }
	killed  []tmux.SessionID

	// hasSession is the answer HasSession returns (default false, the
	// original behavior). hasSessionErr, when set, is returned instead.
	hasSession    bool
	hasSessionErr error

	configured []string            // ConfigureServer conf paths
	ensured    []tmux.SessionSpec  // EnsureSession specs
	attached   []tmux.SessionID    // Attach targets
	options    []recordedOption    // SetOption calls
	winOptions []recordedWinOption // SetWindowOption calls
	envs       []recordedEnv       // SetEnvironment calls
	sourced    []string            // SourceFile conf paths

	// sessions is what ListSessions answers (default none — no server).
	sessions []tmux.Session
}

type recordedOption struct {
	ID            tmux.SessionID
	Option, Value string
}

type recordedWinOption struct{ Pane, Option, Value string }

type recordedEnv struct{ Name, Value string }

func (r *recordingTmux) ConfigureServer(path string) { r.configured = append(r.configured, path) }
func (r *recordingTmux) HasSession(context.Context, tmux.SessionID) (bool, error) {
	return r.hasSession, r.hasSessionErr
}
func (r *recordingTmux) EnsureSession(_ context.Context, spec tmux.SessionSpec) error {
	r.ensured = append(r.ensured, spec)
	return nil
}
func (r *recordingTmux) KillSession(_ context.Context, id tmux.SessionID) error {
	r.killed = append(r.killed, id)
	if r.fail {
		return errors.New("no server running")
	}
	return nil
}
func (r *recordingTmux) Attach(_ context.Context, id tmux.SessionID) error {
	r.attached = append(r.attached, id)
	return nil
}
func (r *recordingTmux) SetOption(_ context.Context, id tmux.SessionID, option, value string) error {
	r.options = append(r.options, recordedOption{ID: id, Option: option, Value: value})
	return nil
}
func (r *recordingTmux) SetWindowOption(_ context.Context, pane, option, value string) error {
	r.winOptions = append(r.winOptions, recordedWinOption{Pane: pane, Option: option, Value: value})
	return nil
}
func (r *recordingTmux) SetGlobalOption(_ context.Context, option, value string) error {
	if r.fail {
		return errors.New("no server running")
	}
	r.globals = append(r.globals, struct{ Option, Value string }{option, value})
	return nil
}
func (r *recordingTmux) SetEnvironment(_ context.Context, name, value string) error {
	r.envs = append(r.envs, recordedEnv{Name: name, Value: value})
	return nil
}
func (r *recordingTmux) ListSessions(context.Context) ([]tmux.Session, error) {
	return r.sessions, nil
}
func (r *recordingTmux) Version(context.Context) (string, error) { return "3.7b", nil }
func (r *recordingTmux) SourceFile(_ context.Context, path string) error {
	r.sourced = append(r.sourced, path)
	return nil
}

// optionValue returns the last value SetOption recorded for (id, option),
// and whether it was set at all.
func (r *recordingTmux) optionValue(id tmux.SessionID, option string) (string, bool) {
	value, ok := "", false
	for _, o := range r.options {
		if o.ID == id && o.Option == option {
			value, ok = o.Value, true
		}
	}
	return value, ok
}

// globalValue returns the last value SetGlobalOption recorded for option.
func (r *recordingTmux) globalValue(option string) (string, bool) {
	value, ok := "", false
	for _, g := range r.globals {
		if g.Option == option {
			value, ok = g.Value, true
		}
	}
	return value, ok
}

func (r *recordingTmux) serials() []string {
	var out []string
	for _, g := range r.globals {
		if g.Option == "@vibe_engine_serial" {
			out = append(out, g.Value)
		}
	}
	return out
}

func TestEngineSerialBumps(t *testing.T) {
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
	if _, err := a.Down(ctx, DownRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Forget(ctx, ForgetRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	serials := rt.serials()
	if len(serials) != 4 {
		t.Fatalf("want 4 bumps (register/up/down/forget), got %d: %v", len(serials), serials)
	}
	seen := map[string]bool{}
	for _, s := range serials {
		if seen[s] {
			t.Fatalf("serial values must be distinct: %v", serials)
		}
		seen[s] = true
	}

	// Reads never bump: a request list that adopts nothing stays silent.
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	before := len(rt.serials())
	if _, err := a.RequestList(ctx, RequestListRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Status(ctx, StatusRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if got := len(rt.serials()); got != before {
		t.Fatalf("read paths bumped the serial: %d -> %d", before, got)
	}
}

func TestEngineSerialBumpNeverFailsOps(t *testing.T) {
	a, _ := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}

	// Nil tmux (the newTestApp default): no panic, no error.
	if _, err := a.Up(ctx, UpRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}

	// A failing tmux (dead server) must not fail the operation either.
	a.deps.Tmux = &recordingTmux{fail: true}
	if _, err := a.Down(ctx, DownRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
}

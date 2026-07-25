package app

import (
	"context"
	"errors"
	"testing"

	"github.com/chrisdruta/vibe-tui-box/internal/tmux"
)

// recordingTmux records global option sets and session kills; every
// other method is inert.
type recordingTmux struct {
	fail    bool
	globals []struct{ Option, Value string }
	killed  []tmux.SessionID
}

func (r *recordingTmux) ConfigureServer(string) {}
func (r *recordingTmux) HasSession(context.Context, tmux.SessionID) (bool, error) {
	return false, nil
}
func (r *recordingTmux) EnsureSession(context.Context, tmux.SessionSpec) error { return nil }
func (r *recordingTmux) KillSession(_ context.Context, id tmux.SessionID) error {
	r.killed = append(r.killed, id)
	if r.fail {
		return errors.New("no server running")
	}
	return nil
}
func (r *recordingTmux) Attach(context.Context, tmux.SessionID) error { return nil }
func (r *recordingTmux) SetOption(context.Context, tmux.SessionID, string, string) error {
	return nil
}
func (r *recordingTmux) SetGlobalOption(_ context.Context, option, value string) error {
	if r.fail {
		return errors.New("no server running")
	}
	r.globals = append(r.globals, struct{ Option, Value string }{option, value})
	return nil
}
func (r *recordingTmux) SetEnvironment(context.Context, string, string) error { return nil }
func (r *recordingTmux) ListSessions(context.Context) ([]tmux.Session, error) { return nil, nil }

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

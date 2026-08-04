package tmux

import (
	"context"
	"strings"
	"testing"

	"github.com/chrisdruta/vibe-tui-box/internal/runner"
)

// fakeRunner records invocations and scripts exit codes by subcommand.
type fakeRunner struct {
	calls []runner.Invocation
	exit  map[string]int
	out   map[string]string
}

func (f *fakeRunner) Run(ctx context.Context, inv runner.Invocation) (runner.Output, error) {
	f.calls = append(f.calls, inv)
	// The subcommand follows the global -L/-f flags and their values.
	sub := ""
	for i := 0; i < len(inv.Args); i++ {
		if inv.Args[i] == "-L" || inv.Args[i] == "-f" {
			i++
			continue
		}
		sub = inv.Args[i]
		break
	}
	return runner.Output{
		ExitCode: f.exit[sub],
		Stdout:   []byte(f.out[sub]),
	}, nil
}

func TestSessionFor(t *testing.T) {
	id := SessionFor("abcdefghijklmnopqrstuvwxyz")
	if id != "vibe-abcdefghijkl" {
		t.Fatalf("session id %q", id)
	}
}

func TestEscapeFormat(t *testing.T) {
	cases := map[string]string{
		"plain":              "plain",
		"my #1 project":      "my ##1 project",
		"#(rm -rf ~)":        "##(rm -rf ~)",
		"#{@vibe_exe}":       "##{@vibe_exe}",
		"already ## escaped": "already #### escaped", // data stays data
	}
	for in, want := range cases {
		if got := EscapeFormat(in); got != want {
			t.Errorf("EscapeFormat(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEnsureSessionArgv(t *testing.T) {
	fr := &fakeRunner{exit: map[string]int{"has-session": 1}, out: map[string]string{}}
	b, err := NewBinary("/usr/bin/tmux", fr)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	err = b.EnsureSession(ctx, SessionSpec{
		ID: "vibe-abc", Workdir: "/proj", Window: "agent",
		Command: []string{"/bin/vibe", "agent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(fr.calls) != 2 {
		t.Fatalf("want has-session + new-session, got %d calls", len(fr.calls))
	}
	got := strings.Join(fr.calls[1].Args, " ")
	want := "-L " + Socket + " new-session -d -s vibe-abc -c /proj -n agent /bin/vibe agent"
	if got != want {
		t.Fatalf("new-session argv:\ngot  %s\nwant %s", got, want)
	}

	// Existing session: no new-session call.
	fr.exit["has-session"] = 0
	fr.calls = nil
	if err := b.EnsureSession(ctx, SessionSpec{ID: "vibe-abc"}); err != nil {
		t.Fatal(err)
	}
	if len(fr.calls) != 1 {
		t.Fatalf("existing session should only probe, got %d calls", len(fr.calls))
	}
}

// raceRunner models the duplicate-session race: absent until a
// new-session was attempted (the concurrent winner's), then exists —
// and the loser's own new-session fails.
type raceRunner struct {
	calls  []runner.Invocation
	minted bool
}

func (r *raceRunner) Run(_ context.Context, inv runner.Invocation) (runner.Output, error) {
	r.calls = append(r.calls, inv)
	sub := ""
	for i := 0; i < len(inv.Args); i++ {
		if inv.Args[i] == "-L" || inv.Args[i] == "-f" {
			i++
			continue
		}
		sub = inv.Args[i]
		break
	}
	switch sub {
	case "has-session":
		if r.minted {
			return runner.Output{ExitCode: 0}, nil
		}
		return runner.Output{ExitCode: 1}, nil
	case "new-session":
		r.minted = true
		return runner.Output{ExitCode: 1, Stderr: []byte("duplicate session: vibe-abc")}, nil
	}
	return runner.Output{}, nil
}

// TestEnsureSessionConvergesOnRace pins the TOCTOU fix: the loser of a
// concurrent mint (a `vibe tui` join vs a sidebar `_open`) whose
// new-session fails must re-check and treat exists-now as its own
// postcondition met.
func TestEnsureSessionConvergesOnRace(t *testing.T) {
	rr := &raceRunner{}
	b, err := NewBinary("/usr/bin/tmux", rr)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.EnsureSession(context.Background(), SessionSpec{ID: "vibe-abc"}); err != nil {
		t.Fatalf("racing EnsureSession must converge, got %v", err)
	}
	if len(rr.calls) != 3 {
		t.Fatalf("want has-session + new-session + has-session, got %d calls", len(rr.calls))
	}
}

func TestListSessionsFiltersVibe(t *testing.T) {
	fr := &fakeRunner{
		exit: map[string]int{},
		out: map[string]string{
			"list-sessions": "vibe-aaa\x1f1\nother\x1f0\nvibe-bbb\x1f0\n",
		},
	}
	b, _ := NewBinary("/usr/bin/tmux", fr)
	sessions, err := b.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 || sessions[0].ID != "vibe-aaa" || !sessions[0].Attached || sessions[1].Attached {
		t.Fatalf("sessions: %+v", sessions)
	}
}

func TestSelfPane(t *testing.T) {
	cases := []struct {
		name, pane, tmuxEnv, want string
	}{
		{"engine server", "%3", "/tmp/tmux-1000/vibe-engine,123,0", "%3"},
		{"foreign server", "%3", "/tmp/tmux-1000/default,123,0", ""},
		{"no pane", "", "/tmp/tmux-1000/vibe-engine,123,0", ""},
		{"no tmux", "%3", "", ""},
	}
	for _, tc := range cases {
		if got := SelfPane(tc.pane, tc.tmuxEnv); got != tc.want {
			t.Errorf("%s: SelfPane(%q, %q) = %q, want %q", tc.name, tc.pane, tc.tmuxEnv, got, tc.want)
		}
	}
}

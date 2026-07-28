package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chrisdruta/vibe-tui-box/internal/app"
	dockerfake "github.com/chrisdruta/vibe-tui-box/internal/dockerapi/fake"
	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/lock"
	"github.com/chrisdruta/vibe-tui-box/internal/paths"
	"github.com/chrisdruta/vibe-tui-box/internal/registry"
	"github.com/chrisdruta/vibe-tui-box/internal/runner"
	"github.com/chrisdruta/vibe-tui-box/internal/snapshot"
	"github.com/chrisdruta/vibe-tui-box/internal/store"
	"github.com/chrisdruta/vibe-tui-box/internal/version"
)

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC) }

// newTestApp builds a real app over fakes and a temp layout, so run()
// can be exercised end to end without Docker or a real ~/.vibe.
func newTestApp(t *testing.T) *app.App {
	t.Helper()
	layout, err := paths.NewLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	locks := lock.NewFlock(layout.Locks)
	st, err := store.New(layout, locks)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := registry.New(layout.Projects, locks, fixedClock{})
	if err != nil {
		t.Fatal(err)
	}
	snaps, err := snapshot.NewService(st)
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.New(app.Dependencies{
		Clock:     fixedClock{},
		Layout:    layout,
		Locks:     locks,
		Store:     st,
		Registry:  reg,
		Snapshots: snaps,
		Docker:    dockerfake.New(),
		Runner:    runner.NewHost(),
		Version:   version.Info{Release: "test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// dispatch runs args through the real dispatcher with captured streams.
// Commands whose Run must not execute (parse failures, help) pass a nil
// app.
func dispatch(t *testing.T, a *app.App, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code = run(context.Background(), a, args, &out, &errBuf)
	return code, out.String(), errBuf.String()
}

func TestExitCode(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{domain.ErrCanceled, ExitInterrupted},
		{context.Canceled, ExitInterrupted},
		{domain.ErrInvalid, ExitConfig},
		{domain.ErrNotFound, ExitUnknown},
		{domain.ErrConflict, ExitConflict},
		{domain.ErrUnavailable, ExitUnavailable},
		{domain.ErrNotSupported, ExitUnavailable},
		{errors.New("anything else"), ExitFailure},
		// Wrapped sentinels classify identically.
		{fmt.Errorf("op: %w", domain.ErrConflict), ExitConflict},
		{&domain.OpError{Op: "up", Err: fmt.Errorf("x: %w", domain.ErrInvalid)}, ExitConfig},
	}
	for _, tc := range cases {
		if got := exitCode(tc.err); got != tc.want {
			t.Errorf("exitCode(%v) = %d, want %d", tc.err, got, tc.want)
		}
	}
}

func TestRunHelp(t *testing.T) {
	t.Run("no args lists commands and fails usage", func(t *testing.T) {
		code, out, _ := dispatch(t, nil)
		if code != ExitUsage {
			t.Fatalf("code = %d, want %d", code, ExitUsage)
		}
		if !strings.Contains(out, "Commands:") || !strings.Contains(out, "up") {
			t.Fatalf("help output missing commands: %q", out)
		}
	})
	t.Run("explicit help succeeds", func(t *testing.T) {
		for _, args := range [][]string{{"help"}, {"-h"}, {"--help"}} {
			code, out, _ := dispatch(t, nil, args...)
			if code != ExitOK {
				t.Fatalf("%v: code = %d, want 0", args, code)
			}
			if !strings.Contains(out, "Commands:") {
				t.Fatalf("%v: no command list", args)
			}
		}
	})
	t.Run("help COMMAND renders usage and flags", func(t *testing.T) {
		code, out, _ := dispatch(t, nil, "help", "up")
		if code != ExitOK {
			t.Fatalf("code = %d, want 0", code)
		}
		for _, want := range []string{"vibe up —", "usage: vibe up", "json"} {
			if !strings.Contains(out, want) {
				t.Fatalf("help up output missing %q:\n%s", want, out)
			}
		}
	})
	t.Run("help for unknown command fails usage", func(t *testing.T) {
		code, _, errOut := dispatch(t, nil, "help", "frobnicate")
		if code != ExitUsage || !strings.Contains(errOut, "unknown command") {
			t.Fatalf("code = %d, stderr = %q", code, errOut)
		}
	})
	t.Run("-h on a subcommand is help, not an error", func(t *testing.T) {
		for _, args := range [][]string{
			{"up", "-h"}, {"up", "--help"}, {"request", "-h"}, {"dev", "-h"},
			{"clip", "-h"}, {"agent", "-h"}, {"logs", "-h"}, {"init", "-h"},
		} {
			code, out, errOut := dispatch(t, nil, args...)
			if code != ExitOK {
				t.Fatalf("%v: code = %d, want 0 (stderr %q)", args, code, errOut)
			}
			if !strings.Contains(out, "usage: vibe "+args[0]) {
				t.Fatalf("%v: no usage line in %q", args, out)
			}
		}
	})
	t.Run("flag defaults reach the -h output", func(t *testing.T) {
		_, out, _ := dispatch(t, nil, "clip", "-h")
		if !strings.Contains(out, "path-only") {
			t.Fatalf("clip -h missing its own flag:\n%s", out)
		}
	})
}

func TestRunUsageErrors(t *testing.T) {
	cases := []struct {
		args    []string
		wantErr string
	}{
		{[]string{"frobnicate"}, "unknown command"},
		{[]string{"up", "--bogus"}, "flag provided but not defined"},
		{[]string{"version", "extra"}, "unexpected argument"},
		{[]string{"dev"}, "want one of"},
		{[]string{"dev", "frobnicate"}, "unknown dev subcommand"},
		{[]string{"exec"}, "no command given"},
		{[]string{"shell", "extra"}, "unexpected argument"},
		{[]string{"logs", "a", "b"}, "at most one SERVICE"},
		{[]string{"request"}, "want one of"},
		{[]string{"request", "frobnicate"}, "unknown request subcommand"},
		{[]string{"request", "show"}, "show needs a request ID"},
		{[]string{"agent", "--stop", "--restart"}, "mutually exclusive"},
	}
	for _, tc := range cases {
		code, _, errOut := dispatch(t, nil, tc.args...)
		if code != ExitUsage {
			t.Errorf("%v: code = %d, want %d", tc.args, code, ExitUsage)
		}
		if !strings.Contains(errOut, tc.wantErr) {
			t.Errorf("%v: stderr %q missing %q", tc.args, errOut, tc.wantErr)
		}
	}
}

func TestParseClipCmd(t *testing.T) {
	get := func(t *testing.T, args ...string) *ClipCmdRequest {
		t.Helper()
		req, err := parseClipCmd(args)
		if err != nil {
			t.Fatalf("parseClipCmd(%v): %v", args, err)
		}
		return req.(*ClipCmdRequest)
	}
	if r := get(t, "shots", "--path-only"); r.DestDir != "shots" || !r.PathOnly {
		t.Fatalf("trailing flag form: %+v", r)
	}
	if r := get(t, "--path-only", "shots"); r.DestDir != "shots" || !r.PathOnly {
		t.Fatalf("leading flag form: %+v", r)
	}
	if r := get(t, "shots", "--json"); !r.JSON {
		t.Fatalf("trailing --json ignored: %+v", r)
	}
	if _, err := parseClipCmd([]string{"a", "b"}); err == nil {
		t.Fatal("two DIR arguments should fail")
	}
	if _, err := parseClipCmd([]string{"shots", "--bogus"}); err == nil {
		t.Fatal("unknown trailing flag should fail")
	}
}

func TestParseRequestCmd(t *testing.T) {
	req, err := parseRequestCmd([]string{"approve", "add-port", "--yes"})
	if err != nil {
		t.Fatal(err)
	}
	r := req.(*RequestCmdRequest)
	if r.Sub != "approve" || r.ID != "add-port" || r.Candidate != "" || !r.Yes {
		t.Fatalf("approve parse: %+v", r)
	}
	req, err = parseRequestCmd([]string{"approve", "--digest", "sha256:abc", "--yes"})
	if err != nil {
		t.Fatal(err)
	}
	r = req.(*RequestCmdRequest)
	if r.Sub != "approve" || r.ID != "" || r.Candidate != "sha256:abc" || !r.Yes {
		t.Fatalf("approve --digest parse: %+v", r)
	}
	req, err = parseRequestCmd([]string{"reject", "add-port", "-m", "why"})
	if err != nil {
		t.Fatal(err)
	}
	r = req.(*RequestCmdRequest)
	if r.Sub != "reject" || r.ID != "add-port" || r.Message != "why" {
		t.Fatalf("reject parse: %+v", r)
	}
	if _, err := parseRequestCmd([]string{"list", "extra"}); err == nil {
		t.Fatal("list with argument should fail")
	}
	if _, err := parseRequestCmd([]string{"approve"}); err == nil {
		t.Fatal("approve without ID or --digest should fail")
	}
	if _, err := parseRequestCmd([]string{"approve", "add-port", "--digest", "sha256:abc"}); err == nil {
		t.Fatal("approve with both ID and --digest should fail")
	}
}

func TestParseAgentCmdAliases(t *testing.T) {
	req, err := parseAgentCmd([]string{"--session", "review", "--agent", "codex", "--cold"})
	if err != nil {
		t.Fatal(err)
	}
	r := req.(*AgentCmdRequest)
	if r.Session != "review" || r.Agent != "codex" || !r.Cold {
		t.Fatalf("long-form parse: %+v", r)
	}
}

func TestParseInitAutoMemoryTriState(t *testing.T) {
	parse := func(t *testing.T, args ...string) *InitRequest {
		t.Helper()
		req, err := commandTable["init"].Parse(args)
		if err != nil {
			t.Fatal(err)
		}
		return req.(*InitRequest)
	}
	if r := parse(t); r.AutoMemory != nil {
		t.Fatal("absent flag must stay nil (interactive may ask)")
	}
	if r := parse(t, "--auto-memory"); r.AutoMemory == nil || !*r.AutoMemory {
		t.Fatal("--auto-memory must decide true")
	}
	if r := parse(t, "--auto-memory=false"); r.AutoMemory == nil || *r.AutoMemory {
		t.Fatal("--auto-memory=false must decide false")
	}
}

func TestRunVersion(t *testing.T) {
	a := newTestApp(t)
	code, out, _ := dispatch(t, a, "version")
	if code != ExitOK || out != "test\n" {
		t.Fatalf("version: code %d, out %q", code, out)
	}

	code, out, _ = dispatch(t, a, "version", "--quiet")
	if code != ExitOK || out != "" {
		t.Fatalf("version --quiet: code %d, out %q", code, out)
	}

	code, out, _ = dispatch(t, a, "version", "--json")
	if code != ExitOK {
		t.Fatalf("version --json: code %d", code)
	}
	var envelope struct {
		Format int          `json:"format"`
		Kind   string       `json:"kind"`
		Data   version.Info `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("json output does not decode: %v\n%s", err, out)
	}
	if envelope.Format != 1 || envelope.Kind != "version" || envelope.Data.Release != "test" {
		t.Fatalf("envelope contract broken: %+v", envelope)
	}
}

// TestRunDeletedCwd covers the cwd seam: the dispatcher resolves the
// working directory once and fails fast when it is gone, while commands
// declaring NoCwd keep working from a deleted directory.
func TestRunDeletedCwd(t *testing.T) {
	a := newTestApp(t)
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(orig)
	gone := filepath.Join(t.TempDir(), "gone")
	if err := os.Mkdir(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(gone); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}

	// A command needing the project cwd fails fast and loud (not silently
	// relative to some fallback), with the general failure exit code.
	code, _, errOut := dispatch(t, a, "config")
	if code != ExitFailure || !strings.Contains(errOut, "cannot determine working directory") {
		t.Fatalf("config in deleted cwd: code %d, stderr %q", code, errOut)
	}

	// NoCwd commands still work from a deleted directory.
	if code, out, _ := dispatch(t, a, "version"); code != ExitOK || out != "test\n" {
		t.Fatalf("version in deleted cwd: code %d, out %q", code, out)
	}
	if code, _, errOut := dispatch(t, a, "gc"); code != ExitOK {
		t.Fatalf("gc in deleted cwd: code %d, stderr %q", code, errOut)
	}
}

func TestRunNotRegisteredExitCode(t *testing.T) {
	// The test process cwd is inside this repo, which the temp-layout
	// registry has never seen: commands that need a registered project
	// must exit with the stable "not registered" code.
	a := newTestApp(t)
	code, _, errOut := dispatch(t, a, "config")
	if code != ExitUnknown {
		t.Fatalf("config in unregistered dir: code %d (stderr %q), want %d", code, errOut, ExitUnknown)
	}
}

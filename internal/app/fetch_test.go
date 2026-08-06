package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dockerfake "vibe/internal/dockerapi/fake"
	"vibe/internal/model"
)

// TestFetchCachesSinglePass pins the consolidated fetch path: one call
// writes the fleet cache, the agents cache, and EVERY project's detail
// file (aging out forgotten projects' leftovers), pays exactly one
// container listing per project, and bumps the frame serial once —
// never the engine serial, which would tell the sidebar to refetch
// what was just fetched.
func TestFetchCachesSinglePass(t *testing.T) {
	a, docker := newTestApp(t)
	ctx := context.Background()
	dirA, dirB := newProject(t), newProject(t)
	regA, err := a.Register(ctx, RegisterRequest{Dir: dirA})
	if err != nil {
		t.Fatal(err)
	}
	regB, err := a.Register(ctx, RegisterRequest{Dir: dirB})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Up(ctx, UpRequest{Dir: dirA}); err != nil {
		t.Fatal(err)
	}
	docker.ExecOutputs[dockerfake.ExecKey([]string{"bash", model.PayloadAgentPS})] =
		"agent|working|1700000100|claude - opus|claude|opus\n"
	ct := &countingTmux{}
	a.deps.Tmux = ct
	cache := t.TempDir()
	if err := os.WriteFile(filepath.Join(cache, "detail.zzzz"), []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	listsBefore := len(docker.CallsTo("ListProjectContainers"))
	res, err := a.FetchCaches(ctx, FetchRequest{CacheDir: cache, Width: 36})
	if err != nil || res.Skipped || res.Projects != 2 {
		t.Fatalf("fetch: %+v, %v", res, err)
	}
	// One Status listing per project — the single pass.
	if got := len(docker.CallsTo("ListProjectContainers")) - listsBefore; got != 2 {
		t.Fatalf("want 2 container listings (one per project), got %d", got)
	}

	fleet, err := os.ReadFile(filepath.Join(cache, "fleet"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(fleet), "\n"), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "3\x1f") {
		t.Fatalf("fleet cache: %q", lines)
	}
	agents, err := os.ReadFile(filepath.Join(cache, "agents"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(agents), "\x1fagent\x1fworking\x1fclaude\x1f") {
		t.Fatalf("agents cache missing the feeder row: %q", agents)
	}
	for _, id := range []string{string(regA.Record.ID), string(regB.Record.ID)} {
		if _, err := os.Stat(filepath.Join(cache, "detail."+id)); err != nil {
			t.Fatalf("detail cache for %s: %v", id, err)
		}
	}
	if _, err := os.Stat(filepath.Join(cache, "detail.zzzz")); !os.IsNotExist(err) {
		t.Fatal("a forgotten project's detail file must age out")
	}
	if ct.stateBumps() != 1 {
		t.Fatalf("want exactly one state-serial bump, got %d", ct.stateBumps())
	}
	if ct.engineBumps() != 0 {
		t.Fatalf("the fetch must never bump the engine serial, got %d", ct.engineBumps())
	}
	// No temp files linger.
	if leftovers, _ := filepath.Glob(filepath.Join(cache, "*.fetch.tmp")); len(leftovers) != 0 {
		t.Fatalf("temp files linger: %v", leftovers)
	}

	// A non-blocking call that loses the flock skips quietly — another
	// fetch was mid-flight and speaks for it.
	held, err := acquireFlock(filepath.Join(cache, "fetch.lock"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	res, err = a.FetchCaches(ctx, FetchRequest{CacheDir: cache})
	if err != nil || !res.Skipped {
		t.Fatalf("contended fetch must skip: %+v, %v", res, err)
	}
}

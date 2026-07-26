package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitBranch(t *testing.T) {
	dir := t.TempDir()

	if got := gitBranch(dir); got != "" {
		t.Fatalf("no .git should mean no branch, got %q", got)
	}
	if got := gitBranch(""); got != "" {
		t.Fatalf("empty path should mean no branch, got %q", got)
	}

	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/feature/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := gitBranch(dir); got != "feature/x" {
		t.Fatalf("branch = %q, want feature/x", got)
	}

	// Detached HEAD: the short sha is the label.
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("0123456789abcdef0123456789abcdef01234567\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := gitBranch(dir); got != "0123456" {
		t.Fatalf("detached = %q, want 0123456", got)
	}

	// Worktree-style .git FILE indirection.
	wt := t.TempDir()
	real := filepath.Join(dir, "real-gitdir")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "HEAD"), []byte("ref: refs/heads/wt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+real+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := gitBranch(wt); got != "wt" {
		t.Fatalf("worktree branch = %q, want wt", got)
	}
}

func TestRenderFrame(t *testing.T) {
	a, _ := newTestApp(t)
	ctx := context.Background()

	cache := t.TempDir()
	us := "\x1f"
	fleet := strings.Join([]string{
		"1" + us + "projself" + us + "●" + us + "release" + us + "v2.0.0" + us + "0" + us + "self",
		"1" + us + "projother" + us + "◐" + us + "dev" + us + "v2.0.0" + us + "1" + us + "other",
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(cache, "fleet"), []byte(fleet), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "detail.projself"), []byte("  release v2.0.0\n  dev running\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	agents := strings.Join([]string{
		"1" + us + "projself" + us + "agent" + us + "working" + us + "claude" + us + "opus",
		"1" + us + "projself" + us + "agent-ghost" + us + "attention" + us + "codex" + us + "",
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(cache, "agents"), []byte(agents), 0o600); err != nil {
		t.Fatal(err)
	}

	porcelain := strings.Join([]string{
		"G" + us + "30" + us + "24" + us + "$1",
		"S" + us + "$1" + us + "selfname" + us + t.TempDir() + us + "projself",
		"S" + us + "$2" + us + "othername" + us + t.TempDir() + us + "projother",
		"W" + us + "$1" + us + "●" + us + "#9ece6a" + us + "0" + us + "@1" + us + "claude" + us + "1" + us + "opus" +
			us + "working" + us + "agent",
	}, "\n")

	res, err := a.RenderFrame(ctx, FrameRequest{
		Input:    strings.NewReader(porcelain),
		CacheDir: cache,
	})
	if err != nil {
		t.Fatal(err)
	}
	// The self session carries its detail block; the other one gets the
	// dim facts summary from the fleet cache; the working agent gets a
	// nested row, and the viewer-less one in the agents cache gets its
	// own row plus a tray ghost cell.
	for _, want := range []string{"release v2.0.0", "◐ stale", "▲1", "claude", "codex"} {
		if !strings.Contains(res.Body, want) {
			t.Fatalf("frame body missing %q", want)
		}
	}
	if !strings.Contains(res.Map, ":$1") || !strings.Contains(res.Map, "$1:@1") ||
		!strings.Contains(res.Map, "$1:agent-agent-ghost") {
		t.Fatalf("click map missing session, window, or spawn targets: %q", res.Map)
	}
	if !strings.Contains(res.Ghosts, "range=user|ghost-0") {
		t.Fatalf("tray ghost cells missing the viewer-less session: %q", res.Ghosts)
	}
	if res.GhostMap != "agent-ghost" {
		t.Fatalf("ghost map must carry exactly the viewer-less session: %q", res.GhostMap)
	}

	// No cache dir: the frame still renders from tmux truth alone.
	res, err = a.RenderFrame(ctx, FrameRequest{Input: strings.NewReader(porcelain)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Body, "selfname") {
		t.Fatal("cacheless frame must still render sessions")
	}

	if _, err := a.RenderFrame(ctx, FrameRequest{}); err == nil {
		t.Fatal("nil input must fail")
	}
}

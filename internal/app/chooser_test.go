package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chrisdruta/vibe-tui-box/internal/paths"
)

func TestRenderChooser(t *testing.T) {
	a, _ := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	// Two installed CLIs so the non-default dispatch renders too.
	manifest := strings.Replace(testManifest, "agents: [claude]", "agents: [claude, codex]", 1)
	if err := os.WriteFile(filepath.Join(dir, paths.ManifestRelPath), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	rec := mustResolve(t, a, dir)

	us := "\x1f"
	cache := t.TempDir()
	agents := "1" + us + string(rec.ID) + us + "agent" + us + "working" + us + "claude" + us + "fable\n" +
		"1" + us + "otherproject" + us + "agent-codex" + us + "idle" + us + "codex" + us + "\n"
	if err := os.WriteFile(filepath.Join(cache, "agents"), []byte(agents), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := a.RenderChooser(ctx, ChooserRequest{
		Input:    strings.NewReader("W" + us + "@4" + us + "agent\n"),
		CacheDir: cache,
		Project:  rec.ID,
	})
	if err != nil || len(res.Lines) != 4 {
		t.Fatalf("chooser render (2 warm + 2 cold): %+v, %v", res, err)
	}
	// The default's live session has a viewer here: jump to it. The
	// codex row belongs to ANOTHER project — scoping must not leak it
	// into this chooser, so codex renders as a plain launch.
	f := strings.Split(res.Lines[0], us)
	if f[3] != "jump" || f[4] != "@4" {
		t.Fatalf("claude entry: %q", f)
	}
	f = strings.Split(res.Lines[1], us)
	if f[3] != "launcha" || f[4] != "codex" {
		t.Fatalf("codex entry: %q", f)
	}
	// The cold block rides the same render (verdict details are
	// table-tested in tmuxui): default's cold twin then codex's.
	f = strings.Split(res.Lines[2], us)
	if f[3] != "launchc" || f[4] != "claude" {
		t.Fatalf("claude cold entry: %q", f)
	}
	f = strings.Split(res.Lines[3], us)
	if f[3] != "launchac" || f[4] != "codex" {
		t.Fatalf("codex cold entry: %q", f)
	}

	// A project the registry does not know is an error (chooser.sh falls
	// back to the palette), as is a missing input stream.
	if _, err := a.RenderChooser(ctx, ChooserRequest{
		Input: strings.NewReader(""), Project: "no-such-project",
	}); err == nil {
		t.Fatal("unknown project should fail")
	}
	if _, err := a.RenderChooser(ctx, ChooserRequest{Project: rec.ID}); err == nil {
		t.Fatal("nil input should fail")
	}
}

package tmuxui

import (
	"strings"
	"testing"
)

func chooserFields(t *testing.T, line string) []string {
	t.Helper()
	f := strings.Split(line, fleetSep)
	if len(f) != 5 || f[0] != "1" {
		t.Fatalf("chooser porcelain shape: %q", f)
	}
	return f
}

func TestChooser(t *testing.T) {
	out := Chooser(ChooserInput{
		Default: "claude",
		Kinds:   []string{"claude", "codex", "grok"},
		Agents: []AgentEntry{
			{Project: "p", Session: "agent", State: "working", CLI: "claude", Model: "fable"},
			{Project: "p", Session: "agent-codex", State: "idle", CLI: "codex"},
			// A cold/named variant is the tray's business, not an entry here.
			{Project: "p", Session: "agent-cold", State: "attention", CLI: "claude"},
		},
		Windows: []ChooserWindow{
			{ID: "@5", Session: "agent"},
			{ID: "not-a-window", Session: "agent-codex"}, // vetted out: codex stays viewer-less
		},
	})
	if len(out) != 3 {
		t.Fatalf("want 3 entries, got %d: %q", len(out), out)
	}
	// The default's live session has a viewer: reuse it, never double.
	f := chooserFields(t, out[0])
	if f[1] != "● claude · open" || f[2] != "1" || f[3] != "jump" || f[4] != "@5" {
		t.Fatalf("default entry: %q", f)
	}
	// Live but viewer-less: the attach-only spawn, full session address.
	f = chooserFields(t, out[1])
	if f[1] != "● codex · attach" || f[2] != "2" || f[3] != "attach" || f[4] != "agent-codex" {
		t.Fatalf("codex entry: %q", f)
	}
	// Absent from the cache: launch, stopped glyph, `-a` dispatch.
	f = chooserFields(t, out[2])
	if f[1] != "○ grok · launch" || f[2] != "3" || f[3] != "launcha" || f[4] != "grok" {
		t.Fatalf("grok entry: %q", f)
	}
}

func TestChooserDeadSessionLaunchesAgain(t *testing.T) {
	// exited/gone sessions are dead: the glyph keeps the recorded truth
	// but the click launches afresh (`vibe agent` replaces nothing — -A
	// on a dead address creates). A viewer corpse must not turn the
	// verdict into a jump.
	out := Chooser(ChooserInput{
		Default: "claude",
		Kinds:   []string{"claude", "codex"},
		Agents: []AgentEntry{
			{Project: "p", Session: "agent", State: "exited(1)"},
			{Project: "p", Session: "agent-codex", State: "gone"},
		},
		Windows: []ChooserWindow{{ID: "@7", Session: "agent"}},
	})
	f := chooserFields(t, out[0])
	if f[1] != "✗ claude · launch" || f[3] != "launch" || f[4] != "claude" {
		t.Fatalf("exited entry: %q", f)
	}
	f = chooserFields(t, out[1])
	if f[1] != "◌ codex · launch" || f[3] != "launcha" || f[4] != "codex" {
		t.Fatalf("gone entry: %q", f)
	}
}

func TestChooserColdCacheViewerStillJumps(t *testing.T) {
	// No agents cache at all (cold socket dir, fetch not yet run) but a
	// birth-stamped viewer window exists: the verdict must reach it.
	// The launch fallback would run `vibe agent`, whose -A semantics
	// reattach — a second viewer on the running session.
	out := Chooser(ChooserInput{
		Default: "claude",
		Kinds:   []string{"claude", "codex"},
		Windows: []ChooserWindow{{ID: "@3", Session: "agent"}},
	})
	f := chooserFields(t, out[0])
	if f[1] != "○ claude · open" || f[3] != "jump" || f[4] != "@3" {
		t.Fatalf("cold-cache viewer entry: %q", f)
	}
	// Still nothing known about codex: launch stays the honest verdict.
	f = chooserFields(t, out[1])
	if f[3] != "launcha" || f[4] != "codex" {
		t.Fatalf("cold-cache viewerless entry: %q", f)
	}
}

func TestChooserOrderAndVetting(t *testing.T) {
	// The manifest default leads even when image.agents lists it later,
	// and appears once; a kind that could not survive a shell word or
	// tmux target is dropped outright.
	out := Chooser(ChooserInput{
		Default: "codex",
		Kinds:   []string{"claude", "codex", "bad name"},
	})
	if len(out) != 2 {
		t.Fatalf("want 2 entries, got %q", out)
	}
	if f := chooserFields(t, out[0]); f[1] != "○ codex · launch" || f[3] != "launch" {
		t.Fatalf("default-first: %q", f)
	}
	if f := chooserFields(t, out[1]); f[4] != "claude" || f[3] != "launcha" {
		t.Fatalf("non-default claude: %q", f)
	}
	// No manifest default: entries still render, all as -a launches.
	out = Chooser(ChooserInput{Kinds: []string{"claude"}})
	if len(out) != 1 || chooserFields(t, out[0])[3] != "launcha" {
		t.Fatalf("defaultless chooser: %q", out)
	}
}

func TestParseChooserWindows(t *testing.T) {
	got := ParseChooserWindows("W" + fleetSep + "@1" + fleetSep + "agent\n" +
		"W" + fleetSep + "@2\n" + // short record dropped
		"X" + fleetSep + "@3" + fleetSep + "agent\n" + // wrong type dropped
		"\n")
	if len(got) != 1 || got[0].ID != "@1" || got[0].Session != "agent" {
		t.Fatalf("parsed windows: %+v", got)
	}
}

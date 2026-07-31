package tmuxui

import (
	"strings"
	"testing"
)

func runningView() ProjectView {
	return ProjectView{
		ID: "p1", Name: "myproj", Mode: "release", Version: "v2.0.0",
		Containers: []ContainerView{
			{Role: "dev", Running: true, InSync: true},
			{Role: "sidecar:db", Running: true, InSync: true},
		},
	}
}

func TestTokens(t *testing.T) {
	v := runningView()
	if v.Token() != StateRunning {
		t.Fatalf("running token %q", v.Token())
	}
	v.Containers[0].InSync = false
	if v.Token() != StateStale {
		t.Fatalf("stale token %q", v.Token())
	}
	v.Containers[1].Running = false
	if v.Token() != StateStopped {
		t.Fatalf("stopped token %q", v.Token())
	}
	if (ProjectView{}).Token() != StateNone {
		t.Fatal("empty token")
	}
}

func TestStateDisplayForm(t *testing.T) {
	// Nominal is EMPTY — the bar shows nothing next to the clock unless
	// something asks for eyes (2026-07-31, the two-dots dogfood).
	v := runningView()
	if got := State(v); got != "" {
		t.Fatalf("state line %q, want empty when nominal", got)
	}
	if got := State(ProjectView{}); got != "" {
		t.Fatalf("state line %q, want empty with no containers", got)
	}
	// A non-empty cell carries its own trailing dim separator toward
	// the clock — the format string cannot make it conditional.
	sep := "#[fg=" + PaletteHex("dim") + "] · #[default]"
	v.Pending = 2
	if got := State(v); got != "▲2"+sep {
		t.Fatalf("state line %q", got)
	}
	v.Containers[0].InSync = false
	if got := State(v); got != "◐ ▲2"+sep {
		t.Fatalf("state line %q", got)
	}
	v.Pending = 0
	v.Containers[1].Running = false
	if got := State(v); got != "○"+sep {
		t.Fatalf("state line %q", got)
	}
}

func TestSidebarDetailBlock(t *testing.T) {
	v := runningView()
	v.Pending = 1
	for _, width := range []int{20, 40, 80} {
		for _, line := range Sidebar(v, width) {
			if len([]rune(line)) > width {
				t.Fatalf("width %d: line %q too long", width, line)
			}
		}
	}
	// ONE compact line: a segment per non-sidecar container (bare role
	// when nominal — no ● here, that dot belongs to agents; the version
	// rides the first; sidecars render as services-tree rows instead),
	// then ▲n — never the display name (bash-drawn) and never the old
	// mode/state words.
	lines := Sidebar(v, 60)
	if len(lines) != 1 || lines[0] != "dev v2.0.0 · ▲1" {
		t.Fatalf("sidebar shape: %q", lines)
	}
	for _, line := range lines {
		if strings.Contains(line, "myproj") {
			t.Fatalf("detail block leaked the display name: %q", line)
		}
	}
}

func TestSidebarDetailGlyphsAndDevHash(t *testing.T) {
	v := runningView()
	v.Mode, v.Version = "dev", "dev-9766b8d8ddce0f00"
	v.Containers[0].InSync = false
	v.Containers[1].Running = false
	lines := Sidebar(v, 60)
	// Stale renders ◐; the dev hash drops its prefix, cuts to 8, and
	// wears the `build` label (the hex is the binary digest `vibe dev
	// status` names). The stopped sidecar is NOT a segment — its row
	// lives in the services tree.
	if len(lines) != 1 || lines[0] != "◐ dev build 9766b8d8" {
		t.Fatalf("stale/dev-hash line wrong: %q", lines)
	}
	// No containers: mode + version keep the line from rendering
	// empty.
	v.Containers = nil
	lines = Sidebar(v, 60)
	if len(lines) != 1 || lines[0] != "dev build 9766b8d8" {
		t.Fatalf("containerless line wrong: %q", lines)
	}
}

func TestFleetPorcelain(t *testing.T) {
	if lines := Fleet(nil, 80); len(lines) != 0 {
		t.Fatalf("empty fleet must render no lines: %q", lines)
	}
	lines := Fleet([]ProjectView{runningView(), {ID: "p2", Name: "other", Mode: "dev", Pending: 3}}, 80)
	if len(lines) != 2 {
		t.Fatalf("fleet: %q", lines)
	}
	first := strings.Split(lines[0], fleetSep)
	if len(first) != 8 || first[0] != "2" || first[1] != "p1" || first[2] != "●" ||
		first[3] != "release" || first[4] != "v2.0.0" || first[5] != "0" || first[6] != "" || first[7] != "myproj" {
		t.Fatalf("fleet fields: %q", first)
	}
	second := strings.Split(lines[1], fleetSep)
	if second[1] != "p2" || second[2] != "·" || second[5] != "3" || second[7] != "other" {
		t.Fatalf("fleet fields: %q", second)
	}
	// The churn field rides before the name (the only free-text field
	// stays last).
	v := runningView()
	v.Churn = "+128 −40"
	churned := strings.Split(Fleet([]ProjectView{v}, 80)[0], fleetSep)
	if churned[6] != "+128 −40" || churned[7] != "myproj" {
		t.Fatalf("churn field: %q", churned)
	}
}

// Hostile display names must neither leak control bytes nor smuggle a
// field separator into the porcelain.
func TestFleetSanitizesDisplayName(t *testing.T) {
	v := runningView()
	v.Name = "evil\x1b]0;pwned\aname" + fleetSep + "forged-field"
	lines := Fleet([]ProjectView{v}, 80)
	if len(lines) != 1 {
		t.Fatalf("fleet: %q", lines)
	}
	if strings.ContainsAny(lines[0], "\x1b\a\n") {
		t.Fatalf("fleet leaked control bytes: %q", lines[0])
	}
	if got := len(strings.Split(lines[0], fleetSep)); got != 8 {
		t.Fatalf("hostile name forged extra fields (%d): %q", got, lines[0])
	}
}

func TestSidebarSanitizesSemiTrustedText(t *testing.T) {
	v := runningView()
	v.Version = "v2\x1b]0;pwned\a.0"
	v.Containers[0].Role = "dev\adev"
	for _, line := range Sidebar(v, 80) {
		if strings.ContainsAny(line, "\x1b\a\n") {
			t.Fatalf("sidebar leaked control bytes: %q", line)
		}
	}
}

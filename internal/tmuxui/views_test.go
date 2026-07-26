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
	v := runningView()
	if got := State(v); got != "●" {
		t.Fatalf("state line %q, want bare glyph", got)
	}
	v.Pending = 2
	if got := State(v); got != "● ▲2" {
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
	// One line per container (glyph + role, version riding the first),
	// then the pending row — never the display name (bash-drawn) and
	// never the old mode/state words.
	lines := Sidebar(v, 60)
	if len(lines) != 3 || !strings.Contains(lines[0], "● dev · v2.0.0") ||
		!strings.Contains(lines[1], "● sidecar:db") ||
		!strings.Contains(lines[2], "▲ 1 pending") {
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
	// Stale renders ◐, stopped ○; the dev hash drops its prefix and
	// cuts to 8.
	if !strings.Contains(lines[0], "◐ dev · 9766b8d8") || strings.Contains(lines[0], "ddce") {
		t.Fatalf("stale/dev-hash line wrong: %q", lines[0])
	}
	if !strings.Contains(lines[1], "○ sidecar:db") {
		t.Fatalf("stopped line wrong: %q", lines[1])
	}
	// No containers: one StateNone facts line so the block never
	// renders empty.
	v.Containers = nil
	lines = Sidebar(v, 60)
	if len(lines) != 1 || !strings.Contains(lines[0], "· dev 9766b8d8") {
		t.Fatalf("containerless block wrong: %q", lines)
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
	if len(first) != 7 || first[0] != "1" || first[1] != "p1" || first[2] != "●" ||
		first[3] != "release" || first[4] != "v2.0.0" || first[5] != "0" || first[6] != "myproj" {
		t.Fatalf("fleet fields: %q", first)
	}
	second := strings.Split(lines[1], fleetSep)
	if second[1] != "p2" || second[2] != "·" || second[5] != "3" || second[6] != "other" {
		t.Fatalf("fleet fields: %q", second)
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
	if got := len(strings.Split(lines[0], fleetSep)); got != 7 {
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

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

func TestStateProtocol(t *testing.T) {
	v := runningView()
	v.Pending = 2
	if got := State(v); got != "1 ● 2" {
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
	// The block carries mode/version, one line per container, and the
	// pending row — never the display name (that row is bash-drawn).
	lines := Sidebar(v, 60)
	if len(lines) != 4 || !strings.Contains(lines[0], "release v2.0.0") ||
		!strings.Contains(lines[3], "request(s) pending") {
		t.Fatalf("sidebar shape: %q", lines)
	}
	for _, line := range lines {
		if strings.Contains(line, "myproj") {
			t.Fatalf("detail block leaked the display name: %q", line)
		}
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

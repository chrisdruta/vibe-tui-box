package tmuxui

import (
	"strings"
	"testing"
)

func runningView() ProjectView {
	return ProjectView{
		Name: "myproj", Mode: "release", Version: "v2.0.0",
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

func TestSidebarWidths(t *testing.T) {
	v := runningView()
	v.Pending = 1
	for _, width := range []int{20, 40, 80} {
		for _, line := range Sidebar(v, width) {
			if len([]rune(line)) > width {
				t.Fatalf("width %d: line %q too long", width, line)
			}
		}
	}
	lines := Sidebar(v, 60)
	if len(lines) != 5 || !strings.Contains(lines[4], "request(s) pending") {
		t.Fatalf("sidebar shape: %q", lines)
	}
}

func TestFleet(t *testing.T) {
	lines := Fleet(nil, 80)
	if len(lines) != 1 || lines[0] != "no registered projects" {
		t.Fatalf("empty fleet: %q", lines)
	}
	lines = Fleet([]ProjectView{runningView(), {Name: "other", Mode: "dev", Pending: 3}}, 80)
	if len(lines) != 2 || !strings.Contains(lines[1], "▲3") {
		t.Fatalf("fleet: %q", lines)
	}
}

func TestSidebarSanitizesDisplayName(t *testing.T) {
	v := runningView()
	v.Name = "evil\x1b]0;pwned\aname"
	for _, line := range Sidebar(v, 80) {
		if strings.ContainsAny(line, "\x1b\a\n") {
			t.Fatalf("sidebar leaked control bytes: %q", line)
		}
	}
}

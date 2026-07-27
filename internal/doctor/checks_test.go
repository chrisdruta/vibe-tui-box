package doctor

import (
	"context"
	"strings"
	"testing"
)

func TestCheckTmuxSixelFloor(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		version string
		status  Status
		part    string
	}{
		{"missing binary", "", "", Warn, "not found on PATH"},
		{"version unknown stays quiet", "/usr/bin/tmux", "", OK, "/usr/bin/tmux"},
		{"3.7b clears the floor", "/usr/local/bin/tmux", "3.7b", OK, "can render sixel"},
		{"4.0 clears the floor", "/usr/local/bin/tmux", "4.0", OK, "can render sixel"},
		{"3.5a degrades loudly", "/usr/bin/tmux", "3.5a", Warn, "degrade to low-fi"},
		{"unparseable stays quiet", "/usr/bin/tmux", "next-3.8", OK, "/usr/bin/tmux"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := checkTmux(context.Background(), &Input{TmuxPath: tt.path, TmuxVersion: tt.version})
			if res.Status != tt.status {
				t.Fatalf("status = %q, want %q (summary %q)", res.Status, tt.status, res.Summary)
			}
			if !strings.Contains(res.Summary, tt.part) {
				t.Fatalf("summary %q missing %q", res.Summary, tt.part)
			}
		})
	}
}

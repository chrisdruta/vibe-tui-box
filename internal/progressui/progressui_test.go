package progressui

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/chrisdruta/vibe-tui-box/internal/dockerapi"
)

func testRenderer(buf *bytes.Buffer) *Renderer {
	r := New(buf, func() int { return 100 })
	base := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	tick := 0
	r.now = func() time.Time {
		tick++
		return base.Add(time.Duration(tick) * time.Second)
	}
	return r
}

// lastFrame returns everything painted after the final cursor-up
// sequence — the block as it was left on screen.
func lastFrame(out string) string {
	frames := strings.Split(out, "A\r") // each repaint starts with cursor-up N
	return frames[len(frames)-1]
}

// An announced plan renders as BoM rows that end cached or built, with
// the header summarizing the split.
func TestRendererBoMLifecycle(t *testing.T) {
	var buf bytes.Buffer
	r := testRenderer(&buf)
	sink := r.Sink()

	sink(dockerapi.Progress{Stage: dockerapi.StageBuildPlan, Part: "base", Detail: "ubuntu:24.04", Total: 2})
	sink(dockerapi.Progress{Stage: dockerapi.StageBuildPlan, Part: "claude", Detail: "latest channel", Refresh: true, Total: 1})
	sink(dockerapi.Progress{Stage: dockerapi.StageBuildStart, Message: "vibe-tools:p1"})

	// Skeleton paints before any step: claude is announced as a refresh.
	if !strings.Contains(buf.String(), "will rebuild (refresh)") {
		t.Fatalf("skeleton missing refresh verdict:\n%q", buf.String())
	}

	sink(dockerapi.Progress{Stage: dockerapi.StageBuild, Message: "ARG X", Part: "base", Step: 1, Steps: 3, StepStart: true})
	sink(dockerapi.Progress{Stage: dockerapi.StageBuild, Part: "base", Step: 1, Steps: 3, Cached: true})
	sink(dockerapi.Progress{Stage: dockerapi.StageBuild, Message: "FROM y", Part: "base", Step: 2, Steps: 3, StepStart: true})
	sink(dockerapi.Progress{Stage: dockerapi.StageBuild, Part: "base", Step: 2, Steps: 3, Cached: true})
	sink(dockerapi.Progress{Stage: dockerapi.StageBuild, Message: "RUN install claude", Part: "claude", Step: 3, Steps: 3, StepStart: true})
	sink(dockerapi.Progress{Stage: dockerapi.StageBuild, Message: "downloading...", Part: "claude", Step: 3, Steps: 3})
	sink(dockerapi.Progress{Stage: dockerapi.StageBuild, Message: "vibe-tools:p1", Done: true})

	frame := lastFrame(buf.String())
	for _, want := range []string{
		glyphCached + " base", "cached",
		glyphBuilt + " claude", "built ",
		"built vibe-tools:p1 in", "1 cached · 1 built",
	} {
		if !strings.Contains(frame, want) {
			t.Errorf("final frame missing %q:\n%q", want, frame)
		}
	}
	if strings.Contains(frame, "downloading...") {
		t.Error("raw layer output leaked into the frame")
	}
}

// A failure marks the row, replays the failing step's buffered tail,
// and prints the daemon error.
func TestRendererFailureTail(t *testing.T) {
	var buf bytes.Buffer
	r := testRenderer(&buf)
	sink := r.Sink()

	sink(dockerapi.Progress{Stage: dockerapi.StageBuildPlan, Part: "tmux", Detail: "3.7b", Total: 1})
	sink(dockerapi.Progress{Stage: dockerapi.StageBuildStart, Message: "vibe-tools:p1"})
	sink(dockerapi.Progress{Stage: dockerapi.StageBuild, Message: "RUN make", Part: "tmux", Step: 1, Steps: 1, StepStart: true})
	sink(dockerapi.Progress{Stage: dockerapi.StageBuild, Message: "cc -O2 tmux.c", Part: "tmux", Step: 1, Steps: 1})
	sink(dockerapi.Progress{Stage: dockerapi.StageBuild, Message: "fatal error: event.h not found", Part: "tmux", Step: 1, Steps: 1})
	sink(dockerapi.Progress{Stage: dockerapi.StageBuild, Message: "returned a non-zero code: 2", Part: "tmux", Step: 1, Steps: 1, Failed: true})

	out := buf.String()
	for _, want := range []string{
		glyphFailed + " tmux", "failed",
		"last output from tmux",
		"fatal error: event.h not found",
		"returned a non-zero code: 2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("failure output missing %q:\n%q", want, out)
		}
	}
}

// Plan-less builds (extension Dockerfiles) grow a row per step from
// the instruction text.
func TestRendererDynamicRows(t *testing.T) {
	var buf bytes.Buffer
	r := testRenderer(&buf)
	sink := r.Sink()

	sink(dockerapi.Progress{Stage: dockerapi.StageBuildStart, Message: "vibe-ext:p1"})
	sink(dockerapi.Progress{Stage: dockerapi.StageBuild, Message: "RUN apt-get install cowsay", Step: 1, Steps: 2, StepStart: true})
	sink(dockerapi.Progress{Stage: dockerapi.StageBuild, Step: 1, Steps: 2, Cached: true})
	sink(dockerapi.Progress{Stage: dockerapi.StageBuild, Message: "USER vscode", Step: 2, Steps: 2, StepStart: true})
	sink(dockerapi.Progress{Stage: dockerapi.StageBuild, Message: "vibe-ext:p1", Done: true})

	frame := lastFrame(buf.String())
	for _, want := range []string{
		glyphCached + " RUN apt-get install cowsay", "cached",
		glyphBuilt + " USER vscode",
	} {
		if !strings.Contains(frame, want) {
			t.Errorf("final frame missing %q:\n%q", want, frame)
		}
	}
}

// Pulls stay a single overwriting ticker line and never open a block.
func TestRendererPullTicker(t *testing.T) {
	var buf bytes.Buffer
	r := testRenderer(&buf)
	sink := r.Sink()
	sink(dockerapi.Progress{Stage: dockerapi.StagePull, Message: "ubuntu:24.04 Downloading", Current: 1, Total: 10})
	sink(dockerapi.Progress{Stage: dockerapi.StagePull, Message: "ubuntu:24.04", Done: true})
	out := buf.String()
	if !strings.Contains(out, "ubuntu:24.04: done\n") {
		t.Fatalf("pull completion missing:\n%q", out)
	}
	if strings.Count(out, "\n") != 1 {
		t.Fatalf("pull ticks must not commit lines:\n%q", out)
	}
}

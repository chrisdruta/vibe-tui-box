package progressui

import (
	"fmt"
	"os"
	"testing"
	"time"

	"vibe/internal/builder"
	"vibe/internal/dockerapi"
	"vibe/internal/schema"
)

// Not a test: a visual smoke replay (go test -run Smoke -v -tags never).
func TestSmokeReplay(t *testing.T) {
	if os.Getenv("SMOKE") == "" {
		t.Skip("set SMOKE=1 for the visual replay")
	}
	r := New(os.Stdout, func() int { return 100 })
	sink := r.Sink()
	plan := builder.GenerateInstallPlan(
		[]schema.AgentSpec{{Kind: schema.AgentClaude}, {Kind: schema.AgentCodex, Version: "0.144.1"}},
		[]schema.Toolchain{schema.ToolchainGo, schema.ToolchainBun},
		true,
	)
	steps := make([]int, len(plan.Parts))
	for _, idx := range plan.StepPart {
		steps[idx]++
	}
	for i, part := range plan.Parts {
		d := part.Detail
		if part.ID == "base" {
			d = "ubuntu:24.04"
		}
		sink(dockerapi.Progress{Stage: dockerapi.StageBuildPlan, Part: part.Title, Detail: d, Refresh: part.Refresh, Total: int64(steps[i])})
	}
	sink(dockerapi.Progress{Stage: dockerapi.StageBuildStart, Message: "vibe-tools:demo"})
	time.Sleep(600 * time.Millisecond)
	total := len(plan.StepPart)
	for s := 1; s <= total; s++ {
		part := plan.Parts[plan.StepPart[s-1]]
		sink(dockerapi.Progress{Stage: dockerapi.StageBuild, Message: "RUN something for " + part.ID, Part: part.Title, Step: s, Steps: total, StepStart: true})
		if part.Refresh {
			for i := 0; i < 5; i++ {
				sink(dockerapi.Progress{Stage: dockerapi.StageBuild, Message: fmt.Sprintf("downloading chunk %d", i), Part: part.Title, Step: s, Steps: total})
				time.Sleep(300 * time.Millisecond)
			}
		} else {
			sink(dockerapi.Progress{Stage: dockerapi.StageBuild, Part: part.Title, Step: s, Steps: total, Cached: true})
			time.Sleep(150 * time.Millisecond)
		}
	}
	sink(dockerapi.Progress{Stage: dockerapi.StageBuild, Message: "vibe-tools:demo", Done: true})
}

package builder

import (
	"regexp"
	"strings"
	"testing"

	"github.com/chrisdruta/vibe-tui-box/internal/schema"
)

// instructionRE mirrors the classic builder's step counting: every
// instruction is one step. Generated text keeps instructions at column
// zero and continuations indented, so this stays a line scan.
var instructionRE = regexp.MustCompile(`(?m)^[A-Z]+ `)

// The step→part map must cover every Dockerfile instruction exactly,
// for every subset of the closed enums — otherwise the progress UI
// would mis-attribute "Step N/M" events.
func TestGenerateInstallPlanCoversEverySteps(t *testing.T) {
	subsetSelections(func(agents []schema.AgentSpec, toolchains []schema.Toolchain, mask int) {
		for _, refresh := range []bool{false, true} {
			plan := GenerateInstallPlan(agents, toolchains, refresh)
			if plan == nil {
				t.Fatalf("mask %b: nil plan for non-empty selection", mask)
			}
			steps := len(instructionRE.FindAll(plan.Dockerfile, -1))
			if len(plan.StepPart) != steps {
				t.Fatalf("mask %b refresh %v: StepPart maps %d steps, dockerfile has %d:\n%s",
					mask, refresh, len(plan.StepPart), steps, plan.Dockerfile)
			}
			for i, p := range plan.StepPart {
				if p < 0 || p >= len(plan.Parts) {
					t.Fatalf("mask %b: step %d maps to part %d of %d", mask, i+1, p, len(plan.Parts))
				}
			}
			// RefreshKinds must agree with the declared per-agent args,
			// one declaration per busted kind and none it doesn't list.
			for _, kind := range plan.RefreshKinds {
				if got := strings.Count(string(plan.Dockerfile), "ARG "+AgentRefreshArgFor(kind)); got != 1 {
					t.Fatalf("mask %b refresh %v: kind %s declared %d times", mask, refresh, kind, got)
				}
			}
			if declared := strings.Count(string(plan.Dockerfile), "ARG "+agentRefreshArgPrefix); declared != len(plan.RefreshKinds) {
				t.Fatalf("mask %b refresh %v: %d refresh args declared, RefreshKinds lists %d",
					mask, refresh, declared, len(plan.RefreshKinds))
			}
		}
	})
}

// GenerateInstall must stay byte-identical to the plan's Dockerfile —
// it is the same generation, differently packaged.
func TestGenerateInstallMatchesPlan(t *testing.T) {
	got := GenerateInstall(allAgents, allToolchains, true)
	plan := GenerateInstallPlan(allAgents, allToolchains, true)
	if string(got) != string(plan.Dockerfile) {
		t.Fatal("GenerateInstall and GenerateInstallPlan diverged")
	}
}

func kindStrings(kinds []schema.AgentKind) []string {
	out := make([]string, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, string(k))
	}
	return out
}

// The full selection's BoM: canonical order, refresh verdicts on the
// channel-tracking agents only, and the base part first (it absorbs the
// header chrome).
func TestGenerateInstallPlanParts(t *testing.T) {
	plan := GenerateInstallPlan(allAgents, allToolchains, true)
	var ids []string
	for _, p := range plan.Parts {
		ids = append(ids, p.ID)
	}
	// Agents run codex, grok, claude — claude last (most volatile; a
	// layer-cache miss rebuilds everything after it).
	want := []string{"base", "tmux", "chafa", "review", "plugins", "parsers", "gh", "go", "node", "rokit", "bun", "codex", "grok", "claude"}
	if strings.Join(ids, " ") != strings.Join(want, " ") {
		t.Fatalf("part order: got %v want %v", ids, want)
	}
	if got := strings.Join(kindStrings(plan.RefreshKinds), " "); got != "codex grok claude" {
		t.Fatalf("RefreshKinds order: %q", got)
	}
	for _, p := range plan.Parts {
		wantRefresh := p.ID == "claude" || p.ID == "codex" || p.ID == "grok"
		if p.Refresh != wantRefresh {
			t.Errorf("part %s: Refresh = %v, want %v", p.ID, p.Refresh, wantRefresh)
		}
	}
	if plan.StepPart[0] != 0 || plan.Parts[plan.StepPart[0]].ID != "base" {
		t.Errorf("step 1 should belong to base, got %s", plan.Parts[plan.StepPart[0]].ID)
	}
	// Pinned agents drop their refresh verdict; the plan then declares
	// no refresh args even when refresh is requested.
	pinned := GenerateInstallPlan([]schema.AgentSpec{{Kind: schema.AgentClaude, Version: "2.1.220"}}, nil, true)
	if len(pinned.RefreshKinds) != 0 {
		t.Errorf("all-pinned plan should declare no refresh args: %v", pinned.RefreshKinds)
	}
	for _, p := range pinned.Parts {
		if p.Refresh {
			t.Errorf("pinned part %s claims a refresh", p.ID)
		}
		if p.ID == "claude" && p.Detail != "2.1.220" {
			t.Errorf("pinned claude detail = %q", p.Detail)
		}
	}
}

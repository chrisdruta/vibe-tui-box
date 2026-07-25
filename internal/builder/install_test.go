package builder

import (
	"bytes"
	"strings"
	"testing"

	"github.com/chrisdruta/vibe-tui-box/internal/schema"
)

var (
	allAgents     = []schema.AgentKind{schema.AgentClaude, schema.AgentCodex, schema.AgentGrok}
	allToolchains = []schema.Toolchain{schema.ToolchainNode, schema.ToolchainBun, schema.ToolchainGo, schema.ToolchainRokit}
)

func TestGenerateInstallEmpty(t *testing.T) {
	if got := GenerateInstall(nil, nil, false); got != nil {
		t.Fatalf("empty selection: want nil, got %q", got)
	}
}

// Every subset of the closed enums must satisfy the extension
// Dockerfile contract the builder enforces.
func TestGenerateInstallAllSubsetsValidate(t *testing.T) {
	for mask := 1; mask < 1<<(len(allAgents)+len(allToolchains)); mask++ {
		var agents []schema.AgentKind
		var toolchains []schema.Toolchain
		for i, a := range allAgents {
			if mask&(1<<i) != 0 {
				agents = append(agents, a)
			}
		}
		for i, tc := range allToolchains {
			if mask&(1<<(len(allAgents)+i)) != 0 {
				toolchains = append(toolchains, tc)
			}
		}
		out := GenerateInstall(agents, toolchains, false)
		if out == nil {
			t.Fatalf("mask %b: nil output for non-empty selection", mask)
		}
		if err := ValidateDockerfile(out); err != nil {
			t.Fatalf("mask %b: generated dockerfile invalid: %v\n%s", mask, err, out)
		}
	}
}

func TestGenerateInstallDeterministic(t *testing.T) {
	a := GenerateInstall(
		[]schema.AgentKind{schema.AgentGrok, schema.AgentClaude},
		[]schema.Toolchain{schema.ToolchainRokit, schema.ToolchainBun},
		false,
	)
	b := GenerateInstall(
		[]schema.AgentKind{schema.AgentClaude, schema.AgentGrok},
		[]schema.Toolchain{schema.ToolchainBun, schema.ToolchainRokit},
		false,
	)
	if !bytes.Equal(a, b) {
		t.Fatalf("selection order changed output:\n--- a\n%s\n--- b\n%s", a, b)
	}
}

// A project that has never refreshed must produce byte-identical bytes
// to the pre-feature generator, so existing tools images never churn.
func TestGenerateInstallRefreshFalseUnchanged(t *testing.T) {
	for mask := 1; mask < 1<<(len(allAgents)+len(allToolchains)); mask++ {
		var agents []schema.AgentKind
		var toolchains []schema.Toolchain
		for i, a := range allAgents {
			if mask&(1<<i) != 0 {
				agents = append(agents, a)
			}
		}
		for i, tc := range allToolchains {
			if mask&(1<<(len(allAgents)+i)) != 0 {
				toolchains = append(toolchains, tc)
			}
		}
		out := string(GenerateInstall(agents, toolchains, false))
		if strings.Contains(out, AgentRefreshArg) {
			t.Fatalf("mask %b: non-refresh output leaked %s:\n%s", mask, AgentRefreshArg, out)
		}
		if strings.Contains(out, "@openai/codex@"+codexChannel) {
			t.Fatalf("mask %b: non-refresh output floated codex to %s", mask, codexChannel)
		}
	}
}

func TestGenerateInstallRefresh(t *testing.T) {
	out := string(GenerateInstall(allAgents, allToolchains, true))
	if err := ValidateDockerfile([]byte(out)); err != nil {
		t.Fatalf("refreshed dockerfile invalid: %v\n%s", err, out)
	}
	// The cache-buster is declared once and referenced by every
	// channel-tracking agent, so each agent layer's cache key tracks it.
	if got := strings.Count(out, "ARG "+AgentRefreshArg); got != 1 {
		t.Errorf("want ARG %s declared once, got %d\n%s", AgentRefreshArg, got, out)
	}
	if got := strings.Count(out, "agents-refresh=${"+AgentRefreshArg+"}"); got != 3 {
		t.Errorf("want the refresh token referenced by all 3 agents, got %d\n%s", got, out)
	}
	// Codex floats to latest on refresh; the hard pin must be gone.
	if !strings.Contains(out, "@openai/codex@"+codexChannel) {
		t.Errorf("refresh did not float codex to %s:\n%s", codexChannel, out)
	}
	if strings.Contains(out, "@openai/codex@"+codexVersion) {
		t.Errorf("refresh left codex pinned at %s:\n%s", codexVersion, out)
	}
	// The pinned system toolchains sit in earlier layers and must not
	// carry the cache-buster.
	for _, layer := range []string{"go.dev/dl/go" + goVersion, "bun-v" + bunVersion, "rokit-" + rokitVersion} {
		if !strings.Contains(out, layer) {
			t.Fatalf("missing pinned layer %q", layer)
		}
	}
}

func TestGenerateInstallContent(t *testing.T) {
	tests := []struct {
		name       string
		agents     []schema.AgentKind
		toolchains []schema.Toolchain
		want       []string
		absent     []string
	}{
		{
			name:   "claude only",
			agents: []schema.AgentKind{schema.AgentClaude},
			want: []string{
				"claude.ai/install.sh",
				"ENV PATH=/home/vscode/.local/bin:${PATH}",
				"chown vscode:vscode /vibe/agent-state",
				"tmux/releases/download/" + tmuxVersion + "/tmux-" + tmuxVersion + ".tar.gz",
				tmuxSHA256,
				"--enable-sixel",
			},
			absent: []string{"nodesource", "bun.sh", "rokit", "go.dev"},
		},
		{
			name:   "codex drags node in",
			agents: []schema.AgentKind{schema.AgentCodex},
			want: []string{
				"USER root",
				"deb.nodesource.com/setup_" + nodeMajor + ".x",
				"@openai/codex@" + codexVersion,
			},
		},
		{
			name:       "go toolchain is a root layer",
			toolchains: []schema.Toolchain{schema.ToolchainGo},
			want: []string{
				"USER root",
				"go.dev/dl/go" + goVersion + ".linux-",
				goSHA256AMD64,
				goSHA256ARM64,
				"rm -rf /usr/local/go",
				"ENV PATH=/usr/local/go/bin:/home/vscode/go/bin:${PATH}",
			},
			absent: []string{"/home/vscode/.local/bin", "tmux"},
		},
		{
			name:       "rokit gets the unzip guard",
			toolchains: []schema.Toolchain{schema.ToolchainRokit},
			want: []string{
				"command -v unzip",
				"rokit-" + rokitVersion + "-linux-",
				"ENV PATH=/home/vscode/.rokit/bin:${PATH}",
			},
		},
		{
			name:   "grok materializes the real binary",
			agents: []schema.AgentKind{schema.AgentGrok},
			want: []string{
				"x.ai/cli/install.sh",
				"ln -s /home/vscode/.agents/grok /home/vscode/.grok",
				"ln -s grok /home/vscode/.local/bin/agent",
			},
		},
		{
			name:       "bun pinned",
			toolchains: []schema.Toolchain{schema.ToolchainBun},
			want:       []string{"bun-v" + bunVersion, "ENV PATH=/home/vscode/.bun/bin:${PATH}"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := string(GenerateInstall(tt.agents, tt.toolchains, false))
			for _, w := range tt.want {
				if !strings.Contains(out, w) {
					t.Errorf("missing %q in:\n%s", w, out)
				}
			}
			for _, a := range tt.absent {
				if strings.Contains(out, a) {
					t.Errorf("unwanted %q in:\n%s", a, out)
				}
			}
			lastUser := ""
			for _, line := range strings.Split(out, "\n") {
				if f := strings.Fields(line); len(f) == 2 && f[0] == "USER" {
					lastUser = f[1]
				}
			}
			if lastUser != "vscode" {
				t.Errorf("final USER is %q, want vscode:\n%s", lastUser, out)
			}
		})
	}
}

package builder

import (
	"bytes"
	"strings"
	"testing"

	"github.com/chrisdruta/vibe-tui-box/internal/schema"
)

var (
	allAgents     = []schema.AgentSpec{{Kind: schema.AgentClaude}, {Kind: schema.AgentCodex}, {Kind: schema.AgentGrok}}
	allToolchains = []schema.Toolchain{schema.ToolchainNode, schema.ToolchainBun, schema.ToolchainGo, schema.ToolchainRokit}
)

func TestGenerateInstallEmpty(t *testing.T) {
	if got := GenerateInstall(nil, nil, false); got != nil {
		t.Fatalf("empty selection: want nil, got %q", got)
	}
}

// subsetSelections enumerates every non-empty subset of the closed
// enums, with refresh in both positions.
func subsetSelections(fn func(agents []schema.AgentSpec, toolchains []schema.Toolchain, mask int)) {
	for mask := 1; mask < 1<<(len(allAgents)+len(allToolchains)); mask++ {
		var agents []schema.AgentSpec
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
		fn(agents, toolchains, mask)
	}
}

// Every subset of the closed enums must satisfy the extension
// Dockerfile contract the builder enforces, refreshed or not, pinned
// or not.
func TestGenerateInstallAllSubsetsValidate(t *testing.T) {
	subsetSelections(func(agents []schema.AgentSpec, toolchains []schema.Toolchain, mask int) {
		for _, refresh := range []bool{false, true} {
			out := GenerateInstall(agents, toolchains, refresh)
			if out == nil {
				t.Fatalf("mask %b: nil output for non-empty selection", mask)
			}
			if err := ValidateDockerfile(out); err != nil {
				t.Fatalf("mask %b refresh %v: generated dockerfile invalid: %v\n%s", mask, refresh, err, out)
			}
		}
		// Pinned variants validate too (grok cannot be pinned).
		var pinned []schema.AgentSpec
		for _, a := range agents {
			if a.Kind != schema.AgentGrok {
				a.Version = "1.2.3"
			}
			pinned = append(pinned, a)
		}
		out := GenerateInstall(pinned, toolchains, true)
		if err := ValidateDockerfile(out); err != nil {
			t.Fatalf("mask %b pinned: generated dockerfile invalid: %v\n%s", mask, err, out)
		}
	})
}

func TestGenerateInstallDeterministic(t *testing.T) {
	a := GenerateInstall(
		[]schema.AgentSpec{{Kind: schema.AgentGrok}, {Kind: schema.AgentClaude}},
		[]schema.Toolchain{schema.ToolchainRokit, schema.ToolchainBun},
		false,
	)
	b := GenerateInstall(
		[]schema.AgentSpec{{Kind: schema.AgentClaude}, {Kind: schema.AgentGrok}},
		[]schema.Toolchain{schema.ToolchainBun, schema.ToolchainRokit},
		false,
	)
	if !bytes.Equal(a, b) {
		t.Fatalf("selection order changed output:\n--- a\n%s\n--- b\n%s", a, b)
	}
}

// A project that has never refreshed carries no cache-buster: the
// unversioned installers track their channels but their layers stay
// plainly cacheable until the first rebuild mints a token.
func TestGenerateInstallRefreshFalseHasNoBuster(t *testing.T) {
	subsetSelections(func(agents []schema.AgentSpec, toolchains []schema.Toolchain, mask int) {
		out := string(GenerateInstall(agents, toolchains, false))
		if strings.Contains(out, AgentRefreshArg) {
			t.Fatalf("mask %b: non-refresh output leaked %s:\n%s", mask, AgentRefreshArg, out)
		}
	})
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
	// Unversioned codex tracks the npm dist-tag.
	if !strings.Contains(out, "@openai/codex@"+codexChannel) {
		t.Errorf("unversioned codex should track %s:\n%s", codexChannel, out)
	}
	// The pinned system toolchains sit in earlier layers and must not
	// carry the cache-buster.
	for _, layer := range []string{"go.dev/dl/go" + goVersion, "bun-v" + bunVersion, "rokit-" + rokitVersion} {
		if !strings.Contains(out, layer) {
			t.Fatalf("missing pinned layer %q", layer)
		}
	}
}

// Manifest-pinned agents install their exact version in plain layers
// the refresh token never reaches; unversioned neighbors still bust.
func TestGenerateInstallManifestPins(t *testing.T) {
	pinned := []schema.AgentSpec{
		{Kind: schema.AgentClaude, Version: "2.1.220"},
		{Kind: schema.AgentCodex, Version: "0.144.1"},
	}
	out := string(GenerateInstall(pinned, nil, true))
	if strings.Contains(out, AgentRefreshArg) {
		t.Fatalf("all-pinned selection must ignore refresh entirely:\n%s", out)
	}
	if !strings.Contains(out, "install.sh | bash -s -- 2.1.220") {
		t.Errorf("claude pin missing:\n%s", out)
	}
	if !strings.Contains(out, "@openai/codex@0.144.1") {
		t.Errorf("codex pin missing:\n%s", out)
	}
	if strings.Contains(out, "bash -s -- "+claudeChannel) || strings.Contains(out, "@openai/codex@"+codexChannel) {
		t.Errorf("pinned agents must not track channels:\n%s", out)
	}

	// Mixed: pinned claude stays plain, unversioned grok busts.
	mixed := []schema.AgentSpec{
		{Kind: schema.AgentClaude, Version: "2.1.220"},
		{Kind: schema.AgentGrok},
	}
	out = string(GenerateInstall(mixed, nil, true))
	if got := strings.Count(out, "agents-refresh=${"+AgentRefreshArg+"}"); got != 1 {
		t.Fatalf("want exactly the unversioned agent busted, got %d references:\n%s", got, out)
	}
	claudeLayer := out[strings.Index(out, "claude.ai/install.sh")-200 : strings.Index(out, "claude.ai/install.sh")]
	if strings.Contains(claudeLayer, "agents-refresh") {
		t.Fatalf("pinned claude layer carries the buster:\n%s", out)
	}
	if err := ValidateDockerfile([]byte(out)); err != nil {
		t.Fatal(err)
	}
}

// A word qualifier names a channel, not a pin: it installs that channel
// and refreshes exactly like an unversioned entry — freezing a channel
// in a cached layer would be the staleness bug all over again.
func TestGenerateInstallChannelWordsRefresh(t *testing.T) {
	channels := []schema.AgentSpec{
		{Kind: schema.AgentClaude, Version: "stable"},
		{Kind: schema.AgentCodex, Version: "next"},
	}
	out := string(GenerateInstall(channels, nil, true))
	if got := strings.Count(out, "agents-refresh=${"+AgentRefreshArg+"}"); got != 2 {
		t.Fatalf("channel-selecting agents must both bust, got %d references:\n%s", got, out)
	}
	if !strings.Contains(out, "bash -s -- stable") {
		t.Errorf("claude should install the selected stable channel:\n%s", out)
	}
	if !strings.Contains(out, "@openai/codex@next") {
		t.Errorf("codex should install the selected dist-tag:\n%s", out)
	}
	if err := ValidateDockerfile([]byte(out)); err != nil {
		t.Fatal(err)
	}
}

// Unversioned claude tracks LATEST — stable lags it by design, which
// is exactly the staleness the per-rebuild refresh exists to kill.
func TestGenerateInstallUnversionedClaudeTracksLatest(t *testing.T) {
	out := string(GenerateInstall([]schema.AgentSpec{{Kind: schema.AgentClaude}}, nil, false))
	if !strings.Contains(out, "bash -s -- latest") {
		t.Fatalf("unversioned claude must install the latest channel:\n%s", out)
	}
}

func TestGenerateInstallContent(t *testing.T) {
	tests := []struct {
		name       string
		agents     []schema.AgentSpec
		toolchains []schema.Toolchain
		want       []string
		absent     []string
	}{
		{
			name:   "claude only",
			agents: []schema.AgentSpec{{Kind: schema.AgentClaude}},
			want: []string{
				"claude.ai/install.sh",
				"bash -s -- " + claudeChannel,
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
			agents: []schema.AgentSpec{{Kind: schema.AgentCodex}},
			want: []string{
				"USER root",
				"deb.nodesource.com/setup_" + nodeMajor + ".x",
				"@openai/codex@" + codexChannel,
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
			agents: []schema.AgentSpec{{Kind: schema.AgentGrok}},
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

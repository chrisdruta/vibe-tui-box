package payload

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// runAgentPlugins sources the payload's agent-plugins.sh under a
// tool-complete PATH that deliberately lacks the agent CLIs — the
// script's main body no-ops and the named function runs against the
// given environment. The payload is shell, so these fixtures are the
// unit tests the sandbox seeding contract demanded (BACKLOG, Codex
// adversarial review 2026-07-24).
func runAgentPlugins(t *testing.T, env []string, body string) {
	t.Helper()
	script, err := filepath.Abs(filepath.Join("..", "..", "payload", "container", "agent-plugins.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("agent-plugins.sh not found: %v", err)
	}
	bin := t.TempDir()
	for _, tool := range []string{"date", "mkdir", "chmod", "grep", "mktemp", "cat", "mv", "rm", "find", "sed"} {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("test needs %s on PATH", tool)
		}
		if err := os.Symlink(p, filepath.Join(bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("bash", "-c", ". \""+script+"\" && "+body)
	cmd.Env = append([]string{"PATH=" + bin, "HOME=" + t.TempDir()}, env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s failed: %v\n%s", body, err, out)
	}
}

func seedKeyCount(t *testing.T, cfg string) int {
	t.Helper()
	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return len(regexp.MustCompile(`(?m)^[ \t]*sandbox_mode[ \t]*=`).FindAll(data, -1))
}

func TestCodexSeedConfig(t *testing.T) {
	cases := []struct {
		name     string
		existing string // "" = no file
		want     string // exact content afterwards; "" = don't compare
		keys     int
	}{
		{name: "fresh file is seeded", keys: 1,
			want: "sandbox_mode = \"danger-full-access\"\n"},
		{name: "absent key is prepended above tables", keys: 1,
			existing: "model = \"o3\"\n[tui]\ntheme = \"dark\"\n",
			want:     "sandbox_mode = \"danger-full-access\"\nmodel = \"o3\"\n[tui]\ntheme = \"dark\"\n"},
		{name: "column-zero user key wins untouched", keys: 1,
			existing: "sandbox_mode = \"read-only\"\n",
			want:     "sandbox_mode = \"read-only\"\n"},
		// The regression that could brick codex: an indented user key
		// must be seen, or the prepend creates a duplicate top-level
		// key TOML parsers reject.
		{name: "indented user key wins untouched", keys: 1,
			existing: "  sandbox_mode = \"read-only\"\n",
			want:     "  sandbox_mode = \"read-only\"\n"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			cfg := filepath.Join(home, "config.toml")
			if tt.existing != "" {
				if err := os.WriteFile(cfg, []byte(tt.existing), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			// Twice: the second run must be a no-op, never a second key.
			runAgentPlugins(t, []string{"CODEX_HOME=" + home}, "codex_seed_config")
			runAgentPlugins(t, []string{"CODEX_HOME=" + home}, "codex_seed_config")
			if got := seedKeyCount(t, cfg); got != tt.keys {
				data, _ := os.ReadFile(cfg)
				t.Fatalf("sandbox_mode count %d, want %d:\n%s", got, tt.keys, data)
			}
			if tt.want != "" {
				data, _ := os.ReadFile(cfg)
				if string(data) != tt.want {
					t.Fatalf("config content:\n%q\nwant:\n%q", data, tt.want)
				}
			}
		})
	}
}

func TestCodexPatchPlugin(t *testing.T) {
	cfgDir := t.TempDir()
	write := func(rel, content string) string {
		t.Helper()
		p := filepath.Join(cfgDir, "plugins", filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cache := write("cache/openai-codex/codex/1.0.6/scripts/lib/codex.mjs",
		`const opts = { sandbox: "read-only", other: 1 };`)
	market := write("marketplaces/openai-codex/plugins/codex/scripts/codex-companion.mjs",
		`const mode = options.sandbox ?? "read-only";`)
	// Path and filename mention codex, but it is not the openai-codex
	// tree: the patch must never rewrite an unrelated plugin.
	unrelated := write("cache/acme-codex-tools/codex/2.0.0/codex.mjs",
		`const opts = { sandbox: "read-only" };`)

	runAgentPlugins(t, []string{"CLAUDE_CONFIG_DIR=" + cfgDir}, "codex_patch_plugin")

	for _, p := range []string{cache, market} {
		data, _ := os.ReadFile(p)
		if !strings.Contains(string(data), "danger-full-access") || strings.Contains(string(data), `"read-only"`) {
			t.Fatalf("openai-codex file not patched: %s\n%s", p, data)
		}
	}
	if data, _ := os.ReadFile(unrelated); !strings.Contains(string(data), `"read-only"`) {
		t.Fatalf("unrelated plugin was rewritten:\n%s", data)
	}
}

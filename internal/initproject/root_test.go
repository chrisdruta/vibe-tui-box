package initproject

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSeedRootBriefing covers the root pointer pair: created fresh with
// the import chain intact, and never touching files that already exist.
func TestSeedRootBriefing(t *testing.T) {
	t.Run("fresh root gets both files", func(t *testing.T) {
		dir := t.TempDir()
		created, skipped, err := SeedRootBriefing(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(skipped) != 0 || len(created) != 2 {
			t.Fatalf("created=%v skipped=%v", created, skipped)
		}
		agents, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(agents), "@.vibe/AGENTS.md") ||
			!strings.Contains(string(agents), ".vibe/AGENTS.md") {
			t.Fatalf("AGENTS.md missing the briefing pointer:\n%s", agents)
		}
		claude, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
		if err != nil {
			t.Fatal(err)
		}
		if string(claude) != "@AGENTS.md\n" {
			t.Fatalf("CLAUDE.md shim wrong: %q", claude)
		}
	})

	t.Run("existing files are kept, not overwritten", func(t *testing.T) {
		dir := t.TempDir()
		own := []byte("my own agents guide\n")
		if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), own, 0o644); err != nil {
			t.Fatal(err)
		}
		created, skipped, err := SeedRootBriefing(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(skipped) != 1 || skipped[0] != "AGENTS.md" {
			t.Fatalf("skipped=%v", skipped)
		}
		if len(created) != 1 || created[0] != "CLAUDE.md" {
			t.Fatalf("created=%v", created)
		}
		after, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(own) {
			t.Fatalf("existing AGENTS.md was modified:\n%s", after)
		}
	})
}

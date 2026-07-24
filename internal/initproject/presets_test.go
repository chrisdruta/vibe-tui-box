package initproject

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chrisdruta/vibe-tui-box/internal/builder"
	"github.com/chrisdruta/vibe-tui-box/internal/payload"
	"github.com/chrisdruta/vibe-tui-box/internal/schema"
)

// TestTrackedPresetsRenderValidProjects renders every preset shipped in
// the real payload and pushes the results through the same validators
// `vibe up` would apply: the manifest must load and validate, and a
// seeded extension Dockerfile must pass the closed build contract.
func TestTrackedPresetsRenderValidProjects(t *testing.T) {
	b, err := payload.New(os.DirFS(filepath.Join("..", "..", "payload")))
	if err != nil {
		t.Fatal(err)
	}
	names := b.Names()
	if len(names) < 5 {
		t.Fatalf("expected the shipped preset set, got %v", names)
	}
	for _, name := range names {
		if name == payload.CommonPreset {
			t.Fatalf("the common overlay must not list as a preset: %v", names)
		}
	}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			preset, err := b.Preset(name)
			if err != nil {
				t.Fatal(err)
			}
			// Both init choices must render a valid manifest carrying
			// the chosen agent.memory value.
			for _, memory := range []schema.MemoryMode{schema.MemoryOff, schema.MemoryAuto} {
				files, err := Render(preset, TemplateData{
					ProjectName: "proj", HarnessVersion: "v1.0.0", AutoMemory: string(memory),
				})
				if err != nil {
					t.Fatal(err)
				}
				doc, err := schema.Load(strings.NewReader(string(files["vibe.yaml"])), schema.Limits{})
				if err != nil {
					t.Fatalf("preset %s vibe.yaml: %v", name, err)
				}
				if errs := doc.Validate(); len(errs) > 0 {
					t.Fatalf("preset %s vibe.yaml invalid: %v", name, errs)
				}
				if doc.Manifest.Agent.Memory != memory {
					t.Fatalf("preset %s agent.memory = %q, want %q", name, doc.Manifest.Agent.Memory, memory)
				}
				if doc.Manifest.Image.Extension {
					dockerfile, ok := files["Dockerfile"]
					if !ok {
						t.Fatalf("preset %s enables the extension but seeds no Dockerfile", name)
					}
					if err := builder.ValidateDockerfile(dockerfile); err != nil {
						t.Fatalf("preset %s Dockerfile: %v", name, err)
					}
				}
			}
		})
	}

	// The shared overlay merges under any preset (init's exact rule:
	// preset files win) and must seed instructions plus inert hook
	// samples — never a live hook or a manifest.
	common, err := b.Preset(payload.CommonPreset)
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"vibe.yaml", "vibe.yaml.tmpl", "hooks/post-create.sh", "hooks/post-start.sh"} {
		if _, ok := common.Files[banned]; ok {
			t.Fatalf("common overlay seeds live file %q", banned)
		}
	}
	minimal, err := b.Preset("minimal")
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range common.Files {
		if _, ok := minimal.Files[name]; !ok {
			minimal.Files[name] = content
		}
	}
	files, err := Render(minimal, TemplateData{ProjectName: "proj", HarnessVersion: "v1.0.0", AutoMemory: "off"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"vibe.yaml", "AGENTS.md", "hooks/post-create.sh.sample", "hooks/post-start.sh.sample"} {
		if _, ok := files[want]; !ok {
			t.Fatalf("merged render missing %q (has %v)", want, keysOf(files))
		}
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

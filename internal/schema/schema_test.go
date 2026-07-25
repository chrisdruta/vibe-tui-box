package schema

import (
	"errors"
	"strings"
	"testing"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
)

const minimalManifest = `schema: 1
harness: v2.0.0
image:
  base: "mcr.microsoft.com/devcontainers/base:debian"
  agents: [claude]
agent:
  cmd: claude
  tmux: true
`

const fullManifest = `schema: 1
harness: v2.0.0
image:
  base: "mcr.microsoft.com/devcontainers/base@sha256:` + "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" + `"
  agents: [claude, codex]
  toolchains: [go, node]
  extension: true
runtime:
  ports: ["127.0.0.1:34872:34872"]
  imports:
    - {source: models, target: /models, readonly: true}
  env: {MY_FLAG: "1", A_FLAG: "2"}
services:
  db:
    image: "postgres:16"
    ports: ["127.0.0.1:5432:5432"]
    env: {POSTGRES_PASSWORD: "x"}
    volumes:
      - {name: data, target: /var/lib/postgresql/data}
agent:
  cmd: claude
  tmux: true
env_file: .env
bootstrap:
  required: [git, jq]
`

func load(t *testing.T, src string) (*Document, error) {
	t.Helper()
	return Load(strings.NewReader(src), Limits{})
}

func mustLoad(t *testing.T, src string) *Document {
	t.Helper()
	doc, err := load(t, src)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return doc
}

func TestLoadMinimal(t *testing.T) {
	doc := mustLoad(t, minimalManifest)
	if errs := doc.Validate(); len(errs) > 0 {
		t.Fatalf("unexpected diagnostics: %v", errs)
	}
	m := doc.Manifest
	if m.Schema != 1 || m.Harness != "v2.0.0" || m.Image.Base == "" || m.Agent.Cmd != AgentClaude {
		t.Fatalf("decoded manifest wrong: %+v", m)
	}
}

func TestLoadFull(t *testing.T) {
	doc := mustLoad(t, fullManifest)
	if errs := doc.Validate(); len(errs) > 0 {
		t.Fatalf("unexpected diagnostics: %v", errs)
	}
	m := doc.Manifest
	if len(m.Runtime.Env) != 2 || m.Runtime.Env[0].Key != "A_FLAG" {
		t.Fatalf("env not sorted by key: %+v", m.Runtime.Env)
	}
	if len(m.Services) != 1 || m.Services["db"].Image != "postgres:16" {
		t.Fatalf("services wrong: %+v", m.Services)
	}
	if !m.Image.Extension || len(m.Image.Toolchains) != 2 {
		t.Fatalf("image wrong: %+v", m.Image)
	}
}

func TestLoadStructuralRejections(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"empty", ""},
		{"two documents", "schema: 1\n---\nschema: 1\n"},
		{"anchor and alias", "a: &x 1\nb: *x\n"},
		{"merge key", "a: &x {k: 1}\nb: {<<: *x}\n"},
		{"duplicate key", "schema: 1\nschema: 2\n"},
		{"non-string key", "1: x\n"},
		{"custom tag", "a: !!binary aGk=\n"},
		{"unknown field", "schema: 1\nbogus: true\n"},
		{"not a mapping", "- a\n- b\n"},
		{"wrong type", "schema: [1]\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := load(t, tc.src)
			if err == nil {
				t.Fatal("expected load failure")
			}
			if !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("error %v is not ErrInvalid", err)
			}
		})
	}
}

func TestLoadLimits(t *testing.T) {
	big := "schema: 1\nharness: v2.0.0\n" + strings.Repeat("# padding\n", 100)
	if _, err := Load(strings.NewReader(big), Limits{MaxBytes: 64}); err == nil {
		t.Fatal("byte limit not enforced")
	}
	deep := "a:\n"
	pad := " "
	for i := 0; i < 40; i++ {
		deep += pad + "a:\n"
		pad += " "
	}
	if _, err := load(t, deep); err == nil {
		t.Fatal("depth limit not enforced")
	}
	var sb strings.Builder
	sb.WriteString("a:\n")
	for i := 0; i < 1500; i++ {
		sb.WriteString("  - x\n")
	}
	if _, err := load(t, sb.String()); err == nil {
		t.Fatal("collection limit not enforced")
	}
}

func TestValidateDiagnostics(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		wantPath string
	}{
		{"unsupported schema", "schema: 9\nharness: v2.0.0\nimage: {base: x, agents: [claude]}\nagent: {cmd: claude}\n", "schema"},
		{"bad harness", "schema: 1\nharness: nope\nimage: {base: x, agents: [claude]}\nagent: {cmd: claude}\n", "harness"},
		{"missing base", "schema: 1\nharness: v2.0.0\nimage: {agents: [claude]}\nagent: {cmd: claude}\n", "image.base"},
		{"non-loopback port", "schema: 1\nharness: v2.0.0\nimage: {base: x, agents: [claude]}\nruntime: {ports: [\"0.0.0.0:80:80\"]}\nagent: {cmd: claude}\n", "runtime.ports[0]"},
		{"absolute import source", "schema: 1\nharness: v2.0.0\nimage: {base: x, agents: [claude]}\nruntime: {imports: [{source: /etc, target: /d}]}\nagent: {cmd: claude}\n", "runtime.imports[0].source"},
		{"escaping import source", "schema: 1\nharness: v2.0.0\nimage: {base: x, agents: [claude]}\nruntime: {imports: [{source: ../up, target: /d}]}\nagent: {cmd: claude}\n", "runtime.imports[0].source"},
		{"relative import target", "schema: 1\nharness: v2.0.0\nimage: {base: x, agents: [claude]}\nruntime: {imports: [{source: m, target: d}]}\nagent: {cmd: claude}\n", "runtime.imports[0].target"},
		{"reserved service", "schema: 1\nharness: v2.0.0\nimage: {base: x, agents: [claude]}\nservices: {dev: {image: y}}\nagent: {cmd: claude}\n", "services.dev"},
		{"agent not installed", "schema: 1\nharness: v2.0.0\nimage: {base: x, agents: [claude]}\nagent: {cmd: codex}\n", "agent.cmd"},
		{"bad env key", "schema: 1\nharness: v2.0.0\nimage: {base: x, agents: [claude]}\nruntime: {env: {\"BAD-KEY\": \"1\"}}\nagent: {cmd: claude}\n", "runtime.env.BAD-KEY"},
		{"absolute env_file", "schema: 1\nharness: v2.0.0\nimage: {base: x, agents: [claude]}\nagent: {cmd: claude}\nenv_file: /etc/passwd\n", "env_file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := mustLoad(t, tc.src)
			errs := doc.Validate()
			for _, e := range errs {
				if e.Path == tc.wantPath {
					return
				}
			}
			t.Fatalf("no diagnostic at %q; got %v", tc.wantPath, errs)
		})
	}
}

func TestValidatePositions(t *testing.T) {
	doc := mustLoad(t, "schema: 9\nharness: v2.0.0\nimage: {base: x, agents: [claude]}\nagent: {cmd: claude}\n")
	errs := doc.Validate()
	if len(errs) == 0 {
		t.Fatal("expected diagnostics")
	}
	if errs[0].Path != "schema" || errs[0].Line != 1 {
		t.Fatalf("first diagnostic should locate schema on line 1: %+v", errs[0])
	}
}

func TestUnknownEnum(t *testing.T) {
	if _, err := load(t, "schema: 1\nharness: v2.0.0\nimage: {base: x, agents: [copilot]}\nagent: {cmd: claude}\n"); err == nil {
		t.Fatal("unknown agent enum accepted")
	}
	if _, err := load(t, "schema: 1\nharness: v2.0.0\nimage: {base: x, agents: [claude], toolchains: [zig]}\nagent: {cmd: claude}\n"); err == nil {
		t.Fatal("unknown toolchain enum accepted")
	}
	if _, err := load(t, "schema: 1\nharness: v2.0.0\nimage: {base: x, agents: [claude]}\nagent: {cmd: claude, memory: always}\n"); err == nil {
		t.Fatal("unknown memory enum accepted")
	}
}

func TestAgentMemoryModes(t *testing.T) {
	doc := mustLoad(t, "schema: 1\nharness: v2.0.0\nimage: {base: x, agents: [claude]}\nagent: {cmd: claude, memory: auto}\n")
	if doc.Manifest.Agent.Memory != MemoryAuto {
		t.Fatalf("memory = %q, want auto", doc.Manifest.Agent.Memory)
	}
	if errs := doc.Validate(); len(errs) != 0 {
		t.Fatalf("memory: auto should validate: %v", errs)
	}
	// Absent means the zero value — the engine treats it as off.
	doc = mustLoad(t, "schema: 1\nharness: v2.0.0\nimage: {base: x, agents: [claude]}\nagent: {cmd: claude}\n")
	if doc.Manifest.Agent.Memory != "" {
		t.Fatalf("absent memory = %q, want zero", doc.Manifest.Agent.Memory)
	}
}

func TestParsePortTable(t *testing.T) {
	good := []string{"127.0.0.1:80:80", "127.1.2.3:1:65535"}
	for _, g := range good {
		if _, err := ParsePort(g); err != nil {
			t.Fatalf("ParsePort(%q): %v", g, err)
		}
	}
	bad := []string{"", "80:80", "0.0.0.0:80:80", "10.0.0.1:80:80", "localhost:80:80", "127.0.0.1:0:80", "127.0.0.1:80:70000", "127.0.0.1:x:80"}
	for _, b := range bad {
		if _, err := ParsePort(b); err == nil {
			t.Fatalf("ParsePort(%q) unexpectedly succeeded", b)
		}
	}
}

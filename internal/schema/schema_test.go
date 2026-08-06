package schema

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"vibe/internal/domain"
)

const minimalManifest = `schema: 1
image:
  base: "mcr.microsoft.com/devcontainers/base:debian"
  agents: [claude]
agent:
  cmd: claude
  tmux: true
`

const fullManifest = `schema: 1
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
	if m.Schema != 1 || m.Image.Base == "" || m.Agent.Cmd != AgentClaude {
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
		{"retired harness key", "schema: 1\nharness: v2.0.0\nimage: {base: x, agents: [claude]}\nagent: {cmd: claude}\n"},
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
	big := "schema: 1\n" + strings.Repeat("# padding\n", 100)
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

func TestAgentSpecParsing(t *testing.T) {
	base := "schema: 1\nimage: {base: x, agents: [%s]}\nagent: {cmd: claude}\n"

	doc := mustLoad(t, fmt.Sprintf(base, "claude@2.1.220, codex"))
	if errs := doc.Validate(); len(errs) > 0 {
		t.Fatalf("pinned manifest should validate: %v", errs)
	}
	agents := doc.Manifest.Image.Agents
	if len(agents) != 2 ||
		agents[0].Kind != AgentClaude || agents[0].Version != "2.1.220" ||
		agents[1].Kind != AgentCodex || agents[1].Version != "" {
		t.Fatalf("spec parse wrong: %+v", agents)
	}
	if agents[0].String() != "claude@2.1.220" || agents[1].String() != "codex" {
		t.Fatalf("spec String() wrong: %q, %q", agents[0], agents[1])
	}

	for name, src := range map[string]string{
		"grok cannot pin":       fmt.Sprintf(base, "claude, \"grok@1.0\""),
		"empty version":         fmt.Sprintf(base, "\"claude@\""),
		"shell-meaning version": fmt.Sprintf(base, "\"claude@$(id)\""),
		"quote in version":      fmt.Sprintf(base, "\"claude@1'2\""),
		"leading punctuation":   fmt.Sprintf(base, "\"claude@.1\""),
		"unknown agent pinned":  fmt.Sprintf(base, "\"gemini@1.0\""),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := load(t, src); err == nil {
				t.Fatalf("should fail to load:\n%s", src)
			}
		})
	}

	// A pin and its unversioned twin are one CLI declared twice.
	dup := mustLoad(t, fmt.Sprintf(base, "claude, claude@2.1.220"))
	errs := dup.Validate()
	found := false
	for _, e := range errs {
		if e.Path == "image.agents[1]" {
			found = true
		}
	}
	if !found {
		t.Fatalf("pinned duplicate not diagnosed: %v", errs)
	}
}

func TestValidateDiagnostics(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		wantPath string
	}{
		{"unsupported schema", "schema: 9\nimage: {base: x, agents: [claude]}\nagent: {cmd: claude}\n", "schema"},
		{"missing base", "schema: 1\nimage: {agents: [claude]}\nagent: {cmd: claude}\n", "image.base"},
		{"non-loopback port", "schema: 1\nimage: {base: x, agents: [claude]}\nruntime: {ports: [\"0.0.0.0:80:80\"]}\nagent: {cmd: claude}\n", "runtime.ports[0]"},
		{"absolute import source", "schema: 1\nimage: {base: x, agents: [claude]}\nruntime: {imports: [{source: /etc, target: /d}]}\nagent: {cmd: claude}\n", "runtime.imports[0].source"},
		{"escaping import source", "schema: 1\nimage: {base: x, agents: [claude]}\nruntime: {imports: [{source: ../up, target: /d}]}\nagent: {cmd: claude}\n", "runtime.imports[0].source"},
		{"relative import target", "schema: 1\nimage: {base: x, agents: [claude]}\nruntime: {imports: [{source: m, target: d}]}\nagent: {cmd: claude}\n", "runtime.imports[0].target"},
		{"writable import", "schema: 1\nimage: {base: x, agents: [claude]}\nruntime: {imports: [{source: m, target: /d, readonly: false}]}\nagent: {cmd: claude}\n", "runtime.imports[0].readonly"},
		{"default-writable import", "schema: 1\nimage: {base: x, agents: [claude]}\nruntime: {imports: [{source: m, target: /d}]}\nagent: {cmd: claude}\n", "runtime.imports[0].readonly"},
		{"reserved service", "schema: 1\nimage: {base: x, agents: [claude]}\nservices: {dev: {image: y}}\nagent: {cmd: claude}\n", "services.dev"},
		{"reserved dns service", "schema: 1\nimage: {base: x, agents: [claude]}\nservices: {dns: {image: y}}\nagent: {cmd: claude}\n", "services.dns"},
		{"agent not installed", "schema: 1\nimage: {base: x, agents: [claude]}\nagent: {cmd: codex}\n", "agent.cmd"},
		{"bad env key", "schema: 1\nimage: {base: x, agents: [claude]}\nruntime: {env: {\"BAD-KEY\": \"1\"}}\nagent: {cmd: claude}\n", "runtime.env.BAD-KEY"},
		{"absolute env_file", "schema: 1\nimage: {base: x, agents: [claude]}\nagent: {cmd: claude}\nenv_file: /etc/passwd\n", "env_file"},
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

// Image references with a registry host port are the canonical
// private-registry workflow; uppercase hostnames are legal too
// (review 2026-08-05 §2.5).
func TestImageRefShapes(t *testing.T) {
	valid := []string{
		"postgres:16", "ghcr.io/owner/app:v1",
		"localhost:5000/app:1.0", "registry.example.com:8443/team/app",
		"Registry.Example.com/team/app",
	}
	for _, ref := range valid {
		doc := mustLoad(t, "schema: 1\nimage: {base: \""+ref+"\", agents: [claude]}\nagent: {cmd: claude}\n")
		if errs := doc.Validate(); len(errs) != 0 {
			t.Errorf("%q should be a valid image reference: %v", ref, errs)
		}
	}
	invalid := []string{"UPPER/case-path", "spaces bad", "-leading/app"}
	for _, ref := range invalid {
		doc := mustLoad(t, "schema: 1\nimage: {base: \""+ref+"\", agents: [claude]}\nagent: {cmd: claude}\n")
		if errs := doc.Validate(); len(errs) == 0 {
			t.Errorf("%q should be rejected", ref)
		}
	}
}

func TestValidatePositions(t *testing.T) {
	doc := mustLoad(t, "schema: 9\nimage: {base: x, agents: [claude]}\nagent: {cmd: claude}\n")
	errs := doc.Validate()
	if len(errs) == 0 {
		t.Fatal("expected diagnostics")
	}
	if errs[0].Path != "schema" || errs[0].Line != 1 {
		t.Fatalf("first diagnostic should locate schema on line 1: %+v", errs[0])
	}
}

func TestUnknownEnum(t *testing.T) {
	if _, err := load(t, "schema: 1\nimage: {base: x, agents: [copilot]}\nagent: {cmd: claude}\n"); err == nil {
		t.Fatal("unknown agent enum accepted")
	}
	if _, err := load(t, "schema: 1\nimage: {base: x, agents: [claude], toolchains: [zig]}\nagent: {cmd: claude}\n"); err == nil {
		t.Fatal("unknown toolchain enum accepted")
	}
	if _, err := load(t, "schema: 1\nimage: {base: x, agents: [claude]}\nagent: {cmd: claude, memory: always}\n"); err == nil {
		t.Fatal("unknown memory enum accepted")
	}
	if _, err := load(t, "schema: 1\nimage: {base: x, agents: [claude]}\nruntime: {egress: none}\nagent: {cmd: claude}\n"); err == nil {
		t.Fatal("unknown egress enum accepted")
	}
}

func TestRuntimeEgressModes(t *testing.T) {
	doc := mustLoad(t, "schema: 1\nimage: {base: x, agents: [claude]}\nruntime: {egress: off}\nagent: {cmd: claude}\n")
	if doc.Manifest.Runtime.Egress != EgressOff {
		t.Fatalf("egress = %q, want off", doc.Manifest.Runtime.Egress)
	}
	if errs := doc.Validate(); len(errs) != 0 {
		t.Fatalf("egress: off should validate: %v", errs)
	}
	doc = mustLoad(t, "schema: 1\nimage: {base: x, agents: [claude]}\nruntime: {egress: on}\nagent: {cmd: claude}\n")
	if doc.Manifest.Runtime.Egress != EgressOn {
		t.Fatalf("egress = %q, want on", doc.Manifest.Runtime.Egress)
	}
	// Absent means the zero value — the engine treats it as on: the
	// ledger default is inverted relative to agent.memory.
	doc = mustLoad(t, "schema: 1\nimage: {base: x, agents: [claude]}\nagent: {cmd: claude}\n")
	if doc.Manifest.Runtime.Egress != "" {
		t.Fatalf("absent egress = %q, want zero", doc.Manifest.Runtime.Egress)
	}
}

func TestAgentMemoryModes(t *testing.T) {
	doc := mustLoad(t, "schema: 1\nimage: {base: x, agents: [claude]}\nagent: {cmd: claude, memory: auto}\n")
	if doc.Manifest.Agent.Memory != MemoryAuto {
		t.Fatalf("memory = %q, want auto", doc.Manifest.Agent.Memory)
	}
	if errs := doc.Validate(); len(errs) != 0 {
		t.Fatalf("memory: auto should validate: %v", errs)
	}
	// Absent means the zero value — the engine treats it as off.
	doc = mustLoad(t, "schema: 1\nimage: {base: x, agents: [claude]}\nagent: {cmd: claude}\n")
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

// TestLoadNodeAndScalarLimits closes the last two bounds of the
// bounded-parse trust surface: total node count and single-scalar
// size, both injected small so the tests stay tiny.
func TestLoadNodeAndScalarLimits(t *testing.T) {
	var many strings.Builder
	many.WriteString("schema: 1\nruntime:\n  env:\n")
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&many, "    K%d: \"v\"\n", i)
	}
	if _, err := Load(strings.NewReader(many.String()), Limits{MaxNodes: 50}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("node limit not enforced: %v", err)
	}
	if _, err := Load(strings.NewReader(many.String()), Limits{}); err != nil {
		t.Fatalf("default node limit rejected a small manifest: %v", err)
	}

	fat := "schema: 1\nruntime:\n  env:\n    FAT: \"" + strings.Repeat("x", 100) + "\"\n"
	if _, err := Load(strings.NewReader(fat), Limits{MaxScalar: 64}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("scalar limit not enforced: %v", err)
	}
	if _, err := Load(strings.NewReader(fat), Limits{}); err != nil {
		t.Fatalf("default scalar limit rejected a small scalar: %v", err)
	}
}

// TestOrderedEnvStringOnly pins env's !!str-only rule: unquoted YAML
// scalars (numbers, booleans, null, timestamps) silently change
// meaning across YAML implementations, so env values must be quoted
// strings — and keys must be scalars at all.
func TestOrderedEnvStringOnly(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
	}{
		{"integer value", "env:\n  PORT: 8080\n"},
		{"boolean value", "env:\n  DEBUG: true\n"},
		{"null value", "env:\n  EMPTY: null\n"},
		{"mapping value", "env:\n  NESTED:\n    a: \"b\"\n"},
		{"sequence key", "env:\n  ? [a, b]\n  : \"v\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var env struct {
				Env OrderedEnv `yaml:"env"`
			}
			if err := yaml.Unmarshal([]byte(tc.src), &env); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("want ErrInvalid, got %v", err)
			}
		})
	}

	var ok struct {
		Env OrderedEnv `yaml:"env"`
	}
	if err := yaml.Unmarshal([]byte("env:\n  B: \"2\"\n  A: \"1\"\n"), &ok); err != nil {
		t.Fatal(err)
	}
	if len(ok.Env) != 2 || ok.Env[0].Key != "A" || ok.Env[1].Key != "B" {
		t.Fatalf("env must sort by key: %+v", ok.Env)
	}
}

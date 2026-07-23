package model

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/registry"
	"github.com/chrisdruta/vibe-tui-box/internal/schema"
	"github.com/chrisdruta/vibe-tui-box/internal/snapshot"
)

var update = flag.Bool("update", false, "rewrite golden files")

const testProjectID = domain.ProjectID("abcdefghijklmnopqrstuvwxyz")

func loadManifest(t *testing.T, src string) schema.Manifest {
	t.Helper()
	doc, err := schema.Load(strings.NewReader(src), schema.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if errs := doc.Validate(); len(errs) > 0 {
		t.Fatalf("manifest diagnostics: %v", errs)
	}
	return doc.Manifest
}

func testInput(t *testing.T, manifest string) CompileInput {
	t.Helper()
	return CompileInput{
		Project: registry.Record{
			ID:          testProjectID,
			Root:        "/home/user/project",
			DisplayName: "project",
		},
		Manifest: loadManifest(t, manifest),
		Snapshot: snapshot.Result{
			Digest: domain.SHA256([]byte("snapshot")),
			Path:   "/home/user/.vibe/state/snapshots/deadbeef",
		},
	}
}

const minimalManifest = `schema: 1
harness: v2.0.0
image:
  base: "mcr.microsoft.com/devcontainers/base:debian"
  agents: [claude]
agent:
  cmd: claude
  tmux: true
`

const sidecarManifest = `schema: 1
harness: v2.0.0
image:
  base: "mcr.microsoft.com/devcontainers/base:debian"
  agents: [claude]
runtime:
  ports: ["127.0.0.1:34872:34872"]
  imports:
    - {source: models, target: /models, readonly: true}
  env: {B_FLAG: "2", A_FLAG: "1"}
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
`

func TestCompileGolden(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
	}{
		{"minimal", minimalManifest},
		{"sidecar", sidecarManifest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, errs := Compile(testInput(t, tc.manifest))
			if len(errs) > 0 {
				t.Fatalf("compile diagnostics: %v", errs)
			}
			got, err := CanonicalJSON(plan)
			if err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join("testdata", tc.name+".golden.json")
			if *update {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden (run with -update to create): %v", err)
			}
			if string(got) != string(want) {
				t.Fatalf("canonical plan drifted from %s:\n%s", golden, got)
			}
		})
	}
}

func TestCompileDeterministic(t *testing.T) {
	p1, errs := Compile(testInput(t, sidecarManifest))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	p2, errs := Compile(testInput(t, sidecarManifest))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if p1.CanonicalHash != p2.CanonicalHash {
		t.Fatalf("hashes differ: %s vs %s", p1.CanonicalHash, p2.CanonicalHash)
	}
	if p1.CanonicalHash.IsZero() {
		t.Fatal("canonical hash not set")
	}
}

func TestCompileShape(t *testing.T) {
	plan, errs := Compile(testInput(t, sidecarManifest))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if plan.Dev.Name != "vibe-abcdefghijkl-dev" {
		t.Fatalf("dev container name %q", plan.Dev.Name)
	}
	if len(plan.Services) != 1 || plan.Services[0].Name != "vibe-abcdefghijkl-svc-db" {
		t.Fatalf("sidecar wrong: %+v", plan.Services)
	}
	if plan.Dev.User != "vscode" {
		t.Fatalf("dev user %q", plan.Dev.User)
	}
	if !plan.Dev.Policy.DropAllCapabilities || !plan.Dev.Policy.NoNewPrivileges {
		t.Fatal("closed policy not applied")
	}
	// Env sorted by key regardless of manifest order.
	if plan.Dev.Environment[0].Key != "A_FLAG" {
		t.Fatalf("env not sorted: %+v", plan.Dev.Environment)
	}
	// Import mount frozen from the snapshot, not the workspace.
	var importMount *Mount
	for i := range plan.Dev.Mounts {
		if plan.Dev.Mounts[i].Target == "/models" {
			importMount = &plan.Dev.Mounts[i]
		}
	}
	if importMount == nil || !strings.Contains(importMount.Source, "/snapshots/") || !importMount.ReadOnly {
		t.Fatalf("import mount wrong: %+v", importMount)
	}
}

func TestCompileRejectsReservedTarget(t *testing.T) {
	manifest := strings.Replace(sidecarManifest, "target: /models", "target: "+WorkspaceTarget, 1)
	_, errs := Compile(testInput(t, manifest))
	if len(errs) == 0 {
		t.Fatal("import targeting the workspace mount should fail")
	}
}

func TestValidateMountOverlap(t *testing.T) {
	plan, errs := Compile(testInput(t, minimalManifest))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	plan.Dev.Mounts = append(plan.Dev.Mounts, Mount{
		Kind: BindMount, Source: "/x", Target: WorkspaceTarget + "/nested",
	})
	if verrs := Validate(plan); len(verrs) == 0 {
		t.Fatal("nested mount target should be rejected")
	}
}

func TestValidatePortPolicy(t *testing.T) {
	plan, errs := Compile(testInput(t, minimalManifest))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	plan.Dev.Ports = []PortBinding{{HostIP: "0.0.0.0", HostPort: 80, ContainerPort: 80}}
	if verrs := Validate(plan); len(verrs) == 0 {
		t.Fatal("non-loopback host ip should be rejected")
	}
}

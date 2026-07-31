package model

import (
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/registry"
	"github.com/chrisdruta/vibe-tui-box/internal/schema"
	"github.com/chrisdruta/vibe-tui-box/internal/snapshot"
	"github.com/chrisdruta/vibe-tui-box/internal/store"
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

// testInputWithArtifact pins a deterministic artifact so compiles gain
// the artifact-gated pieces (payload mounts, the dns ledger sidecar).
func testInputWithArtifact(t *testing.T, manifest string) CompileInput {
	t.Helper()
	in := testInput(t, manifest)
	in.Artifact = store.Artifact{
		Record: store.ArtifactRecord{
			Digest:        domain.SHA256([]byte("artifact")),
			Version:       "0.0.0-test",
			PayloadDigest: domain.SHA256([]byte("payload")),
		},
		Path: "/home/user/.vibe/store/artifacts/cafe",
	}
	return in
}

const minimalManifest = `schema: 1
image:
  base: "mcr.microsoft.com/devcontainers/base:debian"
  agents: [claude]
agent:
  cmd: claude
  tmux: true
`

const sidecarManifest = `schema: 1
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
		input    func(*testing.T, string) CompileInput
	}{
		{"minimal", minimalManifest, testInput},
		{"sidecar", sidecarManifest, testInput},
		// The artifact-bearing compile gains the payload mounts and the
		// synthesized dns ledger sidecar (first among services).
		{"dns", sidecarManifest, testInputWithArtifact},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, errs := Compile(tc.input(t, tc.manifest))
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

// TestCompileAgentStateEnv pins the per-agent login relocation: every
// installed agent whose CLI honors a state-home variable gets it
// pointed at the agent-state volume so logins survive rebuilds.
func TestCompileAgentStateEnv(t *testing.T) {
	manifest := strings.Replace(minimalManifest, "agents: [claude]", "agents: [claude, codex]", 1)
	plan, errs := Compile(testInput(t, manifest))
	if len(errs) > 0 {
		t.Fatalf("compile diagnostics: %v", errs)
	}
	var got []string
	for _, e := range plan.Dev.Environment {
		got = append(got, e.Key+"="+e.Value)
	}
	for _, want := range []string{
		"CLAUDE_CONFIG_DIR=/vibe/agent-state/claude",
		"DISABLE_AUTOUPDATER=1",
		"CODEX_HOME=/vibe/agent-state/codex",
		"GH_CONFIG_DIR=/vibe/agent-state/gh",
	} {
		if !slices.Contains(got, want) {
			t.Fatalf("dev env missing %q: %v", want, got)
		}
	}
}

// TestCompileRuntimeDir pins the runtime-dir contract: the dev
// container mounts a real tmpfs at the XDG runtime dir (so
// state-dir.sh records clear on every container boot) and the engine
// env var points spec-following tools at it.
func TestCompileRuntimeDir(t *testing.T) {
	plan, errs := Compile(testInput(t, minimalManifest))
	if len(errs) > 0 {
		t.Fatalf("compile diagnostics: %v", errs)
	}
	var tmpfs *Mount
	for i := range plan.Dev.Mounts {
		if plan.Dev.Mounts[i].Target == RuntimeDirTarget {
			tmpfs = &plan.Dev.Mounts[i]
		}
	}
	if tmpfs == nil || tmpfs.Kind != TmpfsMount || tmpfs.Source != "" {
		t.Fatalf("runtime dir mount wrong: %+v", tmpfs)
	}
	var envs []string
	for _, e := range plan.Dev.Environment {
		envs = append(envs, e.Key+"="+e.Value)
	}
	if !slices.Contains(envs, "XDG_RUNTIME_DIR="+RuntimeDirTarget) {
		t.Fatalf("dev env missing XDG_RUNTIME_DIR: %v", envs)
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

// TestCompileDNSSidecar pins the synthesized ledger sidecar: present
// (and first) exactly when an artifact is pinned and egress is not
// refused, with the dev container carrying the semantic resolver link.
func TestCompileDNSSidecar(t *testing.T) {
	plan, errs := Compile(testInputWithArtifact(t, sidecarManifest))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if len(plan.Services) != 2 || plan.Services[0].Name != "vibe-abcdefghijkl-svc-dns" ||
		plan.Services[1].Name != "vibe-abcdefghijkl-svc-db" {
		t.Fatalf("dns sidecar should lead the services: %+v", plan.Services)
	}
	dns := plan.Services[0]
	if dns.Image.Ref != CoreDNSImageRef {
		t.Fatalf("dns image %q", dns.Image.Ref)
	}
	if !slices.Equal(dns.Command, []string{"-conf", DNSCorefile}) {
		t.Fatalf("dns command %v", dns.Command)
	}
	if len(dns.Mounts) != 1 || dns.Mounts[0].Target != DNSConfTarget || !dns.Mounts[0].ReadOnly ||
		dns.Mounts[0].Source != "/home/user/.vibe/store/artifacts/cafe/payload/dns" {
		t.Fatalf("dns mount wrong: %+v", dns.Mounts)
	}
	if dns.Log == nil || dns.Log.MaxSize != "10m" || dns.Log.MaxFiles != 3 {
		t.Fatalf("dns log bounds wrong: %+v", dns.Log)
	}
	if dns.User != "0" {
		t.Fatalf("dns sidecar must run as root (file-caps exec rule), got user %q", dns.User)
	}
	if !dns.Policy.NetBindService || !dns.Policy.DropAllCapabilities || !dns.Policy.NoNewPrivileges {
		t.Fatalf("dns policy wrong: %+v", dns.Policy)
	}
	if len(dns.Environment) != 0 || len(dns.Ports) != 0 {
		t.Fatalf("dns sidecar must carry no env or ports: %+v", dns)
	}
	if plan.Dev.DNSFromService != DNSServiceName {
		t.Fatalf("dev dns_from_service %q", plan.Dev.DNSFromService)
	}
	var inventoried bool
	for _, img := range plan.Images {
		if img.Ref == CoreDNSImageRef {
			inventoried = true
		}
	}
	if !inventoried {
		t.Fatalf("CoreDNS ref missing from image inventory: %+v", plan.Images)
	}

	// Opt-out compiles the pre-feature topology.
	off := strings.Replace(sidecarManifest, "runtime:\n", "runtime:\n  egress: off\n", 1)
	plan, errs = Compile(testInputWithArtifact(t, off))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if len(plan.Services) != 1 || plan.Services[0].Name != "vibe-abcdefghijkl-svc-db" {
		t.Fatalf("egress off should drop the dns sidecar: %+v", plan.Services)
	}
	if plan.Dev.DNSFromService != "" {
		t.Fatalf("egress off should clear dns_from_service, got %q", plan.Dev.DNSFromService)
	}

	// No artifact means no store-owned Corefile: same pre-feature shape.
	plan, errs = Compile(testInput(t, sidecarManifest))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if len(plan.Services) != 1 || plan.Dev.DNSFromService != "" {
		t.Fatalf("artifact-less compile should have no dns sidecar: %+v", plan.Services)
	}
}

func TestValidateLogPolicy(t *testing.T) {
	for _, bad := range []LogPolicy{
		{MaxSize: "", MaxFiles: 3},
		{MaxSize: "10", MaxFiles: 3},
		{MaxSize: "0m", MaxFiles: 3},
		{MaxSize: "10m", MaxFiles: 0},
	} {
		plan, errs := Compile(testInput(t, minimalManifest))
		if len(errs) > 0 {
			t.Fatal(errs)
		}
		bad := bad
		plan.Dev.Log = &bad
		if verrs := Validate(plan); len(verrs) == 0 {
			t.Fatalf("log policy %+v should be rejected", bad)
		}
	}
}

func TestValidateDNSFromService(t *testing.T) {
	plan, errs := Compile(testInput(t, minimalManifest))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	plan.Dev.DNSFromService = "dns"
	if verrs := Validate(plan); len(verrs) == 0 {
		t.Fatal("dns_from_service naming a missing service should be rejected")
	}
}

func TestValidateNetBindServiceScope(t *testing.T) {
	// The one capability re-grant is scoped to the engine dns forwarder;
	// any other container claiming it is rejected.
	plan, errs := Compile(testInputWithArtifact(t, sidecarManifest))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if verrs := Validate(plan); len(verrs) != 0 {
		t.Fatalf("compiled plan should validate: %v", verrs)
	}
	plan.Dev.Policy.NetBindService = true
	if verrs := Validate(plan); len(verrs) == 0 {
		t.Fatal("dev claiming NET_BIND_SERVICE should be rejected")
	}
}

func TestCompileRejectsReservedTarget(t *testing.T) {
	// The engine-owned targets stay reserved even when their mounts are
	// absent from this compile: testInput carries no artifact and no
	// broker results dir, so /vibe/payload and /vibe/results exist only
	// as policy, not as mounts.
	for _, target := range []string{
		WorkspaceTarget,
		PayloadTarget,
		ResultsTarget,
		AgentStateTarget + "/nested",
		RuntimeDirTarget + "/nested",
		"/vibe", // contains engine-owned targets
	} {
		manifest := strings.Replace(sidecarManifest, "target: /models", "target: "+target, 1)
		_, errs := Compile(testInput(t, manifest))
		if len(errs) == 0 {
			t.Fatalf("import targeting %s should fail", target)
		}
		if !strings.Contains(errs[0].Path, "runtime.imports[0].target") {
			t.Fatalf("error for %s should name the manifest field, got %+v", target, errs[0])
		}
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

func TestValidateTmpfsSource(t *testing.T) {
	plan, errs := Compile(testInput(t, minimalManifest))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	for i := range plan.Dev.Mounts {
		if plan.Dev.Mounts[i].Kind == TmpfsMount {
			plan.Dev.Mounts[i].Source = "/x"
		}
	}
	if verrs := Validate(plan); len(verrs) == 0 {
		t.Fatal("tmpfs mount with a source should be rejected")
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

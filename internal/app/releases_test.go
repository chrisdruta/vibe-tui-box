package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"vibe/internal/dockerapi"
	dockerfake "vibe/internal/dockerapi/fake"
	"vibe/internal/domain"
	"vibe/internal/model"
	"vibe/internal/paths"
	"vibe/internal/payload"
	"vibe/internal/registry"
)

// withPayload equips a test app with an embedded payload bundle and a
// fake executable so provision works.
func withPayload(t *testing.T, a *App) {
	t.Helper()
	script := "#!/bin/sh\nexec sleep infinity\n"
	// The Corefile rides like the real embedded payload's: a provision
	// from this bundle yields a dns-capable artifact (DNSConfPresent).
	conf := ".:53 {}\n"
	files := []payload.File{{
		Path:   "container/entrypoint.sh",
		Mode:   "0755",
		Size:   int64(len(script)),
		Digest: domain.SHA256([]byte(script)),
	}, {
		Path:   "dns/Corefile",
		Mode:   "0644",
		Size:   int64(len(conf)),
		Digest: domain.SHA256([]byte(conf)),
	}}
	manifest, err := payload.EncodeManifest(files)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := payload.New(fstest.MapFS{
		"container/entrypoint.sh": &fstest.MapFile{Data: []byte(script)},
		"dns/Corefile":            &fstest.MapFile{Data: []byte(conf)},
		payload.ManifestPath:      &fstest.MapFile{Data: manifest},
	})
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "vibe")
	if err := os.WriteFile(exe, []byte("FAKE-ENGINE"), 0o755); err != nil {
		t.Fatal(err)
	}
	a.deps.Payload = bundle
	a.deps.Executable = exe
}

func TestProvisionPinsAndUpUsesPayload(t *testing.T) {
	a, docker := newTestApp(t)
	withPayload(t, a)
	ctx := context.Background()
	dir := newProject(t)

	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	res, err := a.Provision(ctx, ProvisionRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if res.Artifact.Digest.IsZero() || res.Artifact.PayloadDigest.IsZero() || res.Artifact.BinaryDigest.IsZero() {
		t.Fatalf("artifact record incomplete: %+v", res.Artifact)
	}
	if res.Pinned == nil || res.Pinned.Artifact != res.Artifact.Digest {
		t.Fatalf("project not pinned: %+v", res.Pinned)
	}

	// Provision is idempotent for the same binary+payload — including
	// under a moving clock (2026-08-04: a fresh InstalledAt alone used
	// to conflict the once-only record write; the frozen test clock hid
	// it). The record keeps its original story.
	a.deps.Clock = &advancingClock{}
	res2, err := a.Provision(ctx, ProvisionRequest{Dir: dir})
	if err != nil || res2.Artifact.Digest != res.Artifact.Digest {
		t.Fatalf("re-provision: %+v, %v", res2.Artifact, err)
	}
	if !res2.Artifact.InstalledAt.Equal(res.Artifact.InstalledAt) {
		t.Fatalf("re-provision must keep the original record: %s vs %s",
			res2.Artifact.InstalledAt, res.Artifact.InstalledAt)
	}

	// Up now mounts the payload and runs its entrypoint.
	up, err := a.Up(ctx, UpRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !up.State.Running() {
		t.Fatalf("up state: %+v", up.State)
	}
	creates := docker.CallsTo("CreateContainer")
	dev := creates[len(creates)-1].Request.(dockerapi.CreateRequest)
	if len(dev.Command) != 1 || dev.Command[0] != model.PayloadEntrypoint {
		t.Fatalf("dev command %v, want payload entrypoint", dev.Command)
	}
	var payloadMount *dockerapi.Mount
	for i := range dev.Mounts {
		if dev.Mounts[i].Target == model.PayloadTarget {
			payloadMount = &dev.Mounts[i]
		}
	}
	if payloadMount == nil || !payloadMount.ReadOnly {
		t.Fatalf("payload mount missing or writable: %+v", dev.Mounts)
	}
	if !strings.HasSuffix(payloadMount.Source, "/payload") {
		t.Fatalf("payload mount source %q", payloadMount.Source)
	}
	if dev.Labels["dev.vibe.artifact"] != res.Artifact.Digest.String() {
		t.Fatalf("artifact label missing: %v", dev.Labels)
	}
	var digestEnv string
	for _, e := range dev.Env {
		if after, ok := strings.CutPrefix(e, "VIBE_PAYLOAD_DIGEST="); ok {
			digestEnv = after
		}
	}
	if digestEnv != res.Artifact.PayloadDigest.String() {
		t.Fatalf("payload digest env %q", digestEnv)
	}
}

// TestUpSynthesizesDNSSidecar pins the app-level egress wiring: a
// provisioned project resolves the pinned CoreDNS ref and creates the
// dns sidecar ahead of dev, and `runtime.egress: off` restores the
// pre-feature topology with no CoreDNS resolution.
func TestUpSynthesizesDNSSidecar(t *testing.T) {
	run := func(t *testing.T, manifest string) (*dockerfake.Client, []dockerfake.Call) {
		t.Helper()
		a, docker := newTestApp(t)
		withPayload(t, a)
		ctx := context.Background()
		dir := newProject(t)
		if err := os.WriteFile(filepath.Join(dir, paths.ManifestRelPath), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
			t.Fatal(err)
		}
		if _, err := a.Provision(ctx, ProvisionRequest{Dir: dir}); err != nil {
			t.Fatal(err)
		}
		if _, err := a.Up(ctx, UpRequest{Dir: dir}); err != nil {
			t.Fatal(err)
		}
		return docker, docker.CallsTo("CreateContainer")
	}

	docker, creates := run(t, testManifest)
	var resolvedCoreDNS bool
	for _, call := range docker.CallsTo("ResolveImage") {
		if call.Request.(dockerapi.ImageRef) == dockerapi.ImageRef(model.CoreDNSImageRef) {
			resolvedCoreDNS = true
		}
	}
	if !resolvedCoreDNS {
		t.Fatal("CoreDNS ref never resolved")
	}
	if len(creates) != 2 {
		t.Fatalf("create count %d, want dns+dev", len(creates))
	}
	dns := creates[0].Request.(dockerapi.CreateRequest)
	if !strings.HasSuffix(string(dns.Name), "-svc-dns") {
		t.Fatalf("first create %q, want the dns sidecar", dns.Name)
	}
	if !strings.HasPrefix(dns.Image, "coredns/coredns@sha256:") {
		t.Fatalf("dns sidecar image %q, want the pinned form", dns.Image)
	}

	off := strings.Replace(testManifest, "runtime:\n", "runtime:\n  egress: off\n", 1)
	docker, creates = run(t, off)
	for _, call := range docker.CallsTo("ResolveImage") {
		if call.Request.(dockerapi.ImageRef) == dockerapi.ImageRef(model.CoreDNSImageRef) {
			t.Fatal("egress off must not resolve the CoreDNS ref")
		}
	}
	if len(creates) != 1 || strings.HasSuffix(string(creates[0].Request.(dockerapi.CreateRequest).Name), "-svc-dns") {
		t.Fatalf("egress off should create only dev: %+v", creates)
	}
}

// TestUpSkipsDNSSidecarWithoutCorefile pins the 2026-08-04 dogfood
// fix: a pinned artifact whose payload lacks dns/Corefile (a pre-egress
// release, or the observed stale self-provision with an empty
// payload/dns dir) must come up WITHOUT the ledger sidecar — before
// the capability probe, up synthesized a sidecar whose CoreDNS exited
// on the missing conf and every reconcile failed.
func TestUpSkipsDNSSidecarWithoutCorefile(t *testing.T) {
	a, docker := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	reg, err := a.Register(ctx, RegisterRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	// A release artifact with a payload but no dns/Corefile — the
	// pre-egress shape (seedReleaseArtifact carries only the host conf).
	rel := seedReleaseArtifact(t, a, "OLD-RELEASE")
	if _, err := a.deps.Registry.Update(ctx, reg.Record.ID, reg.Record.Revision, func(r *registry.Record) error {
		r.Artifact = rel.Digest
		r.ReleaseVersion = rel.Version
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	up, err := a.Up(ctx, UpRequest{Dir: dir})
	if err != nil {
		t.Fatalf("up with a Corefile-less artifact must degrade, not fail: %v", err)
	}
	if !up.State.Running() {
		t.Fatalf("project not running: %+v", up.State)
	}
	for _, call := range docker.CallsTo("CreateContainer") {
		if strings.HasSuffix(string(call.Request.(dockerapi.CreateRequest).Name), "-svc-dns") {
			t.Fatal("a Corefile-less artifact must not synthesize the dns sidecar")
		}
	}
	for _, call := range docker.CallsTo("ResolveImage") {
		if call.Request.(dockerapi.ImageRef) == dockerapi.ImageRef(model.CoreDNSImageRef) {
			t.Fatal("a Corefile-less artifact must not resolve the CoreDNS ref")
		}
	}
}

// TestProvisionRefusesDevBuiltBinary pins the other half of the
// 2026-08-04 provision fix: on a dev-mode host the running shim's
// content IS the dev artifact `dev sync` minted, and provisioning it
// must fail with the named conflict — a dev artifact can never satisfy
// a release pin — instead of the raw record-content error.
func TestProvisionRefusesDevBuiltBinary(t *testing.T) {
	a, _ := newTestApp(t)
	withPayload(t, a)
	ctx := context.Background()

	// Learn the content digest, then rewrite its record as the
	// dev-build `dev sync` would have minted first.
	res, err := a.Provision(ctx, ProvisionRequest{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	recPath := filepath.Join(a.deps.Layout.Artifacts, res.Artifact.Digest.Hex()+".record.json")
	if err := os.Remove(recPath); err != nil {
		t.Fatal(err)
	}
	dev := res.Artifact
	dev.Release = domain.ReleaseProvenance{Source: "dev-build", Version: dev.Version}
	if err := a.deps.Store.WriteArtifactRecord(dev); err != nil {
		t.Fatal(err)
	}

	_, err = a.Provision(ctx, ProvisionRequest{Dir: t.TempDir()})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("provision over a dev-build record must conflict, got %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "dev artifact") {
		t.Fatalf("the conflict must name the dev-build cause: %v", err)
	}
}

func TestProvisionWithoutPayload(t *testing.T) {
	a, _ := newTestApp(t)
	ctx := context.Background()
	if _, err := a.Provision(ctx, ProvisionRequest{Dir: t.TempDir()}); err == nil {
		t.Fatal("provision without an embedded payload should fail")
	}
}

func TestUpdateRequiresConfiguration(t *testing.T) {
	a, _ := newTestApp(t)
	ctx := context.Background()
	if _, err := a.Update(ctx, UpdateRequest{Dir: t.TempDir(), Version: "v1.0.0"}); err == nil {
		t.Fatal("update without a release source should fail")
	}
}

package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/chrisdruta/vibe-tui-box/internal/dockerapi"
	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/model"
	"github.com/chrisdruta/vibe-tui-box/internal/payload"
)

// withPayload equips a test app with an embedded payload bundle and a
// fake executable so provision works.
func withPayload(t *testing.T, a *App) {
	t.Helper()
	script := "#!/bin/sh\nexec sleep infinity\n"
	files := []payload.File{{
		Path:   "container/entrypoint.sh",
		Mode:   "0755",
		Size:   int64(len(script)),
		Digest: domain.SHA256([]byte(script)),
	}}
	manifest, err := payload.EncodeManifest(files)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := payload.New(fstest.MapFS{
		"container/entrypoint.sh": &fstest.MapFile{Data: []byte(script)},
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

	// Provision is idempotent for the same binary+payload.
	res2, err := a.Provision(ctx, ProvisionRequest{Dir: dir})
	if err != nil || res2.Artifact.Digest != res.Artifact.Digest {
		t.Fatalf("re-provision: %+v, %v", res2.Artifact, err)
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

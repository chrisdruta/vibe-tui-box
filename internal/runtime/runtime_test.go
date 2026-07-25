package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chrisdruta/vibe-tui-box/internal/dockerapi"
	dockerfake "github.com/chrisdruta/vibe-tui-box/internal/dockerapi/fake"
	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/lock"
	"github.com/chrisdruta/vibe-tui-box/internal/model"
	"github.com/chrisdruta/vibe-tui-box/internal/paths"
	"github.com/chrisdruta/vibe-tui-box/internal/registry"
	"github.com/chrisdruta/vibe-tui-box/internal/schema"
	"github.com/chrisdruta/vibe-tui-box/internal/snapshot"
	"github.com/chrisdruta/vibe-tui-box/internal/store"
)

const testProjectID = domain.ProjectID("abcdefghijklmnopqrstuvwxyz")

var testToolsDigest = domain.SHA256([]byte("built-tools"))

const testManifest = `schema: 1
harness: v2.0.0
image:
  base: "base:1@sha256:1111111111111111111111111111111111111111111111111111111111111111"
  agents: [claude]
runtime:
  env: {FLAG: "1"}
services:
  db:
    image: "db:1@sha256:2222222222222222222222222222222222222222222222222222222222222222"
agent:
  cmd: claude
  tmux: true
env_file: .env
`

type fixture struct {
	svc    *Service
	docker *dockerfake.Client
	cand   Candidate
	rec    registry.Record
}

func newFixture(t *testing.T, manifest string, opts ...func(*store.Store, *model.CompileInput)) *fixture {
	t.Helper()
	layout, err := paths.NewLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	locks := lock.NewFlock(layout.Locks)
	st, err := store.New(layout, locks)
	if err != nil {
		t.Fatal(err)
	}
	docker := dockerfake.New()
	svc, err := NewService(docker, st, locks)
	if err != nil {
		t.Fatal(err)
	}

	// Publish an input snapshot carrying the env file.
	staging, err := st.NewStaging("snapshot")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, model.SnapshotEnvFilePath), []byte("SECRET=s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, model.SnapshotManifestPath), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	snapDigest, _, err := store.DigestTree(staging)
	if err != nil {
		t.Fatal(err)
	}
	snapObj, err := st.Publish(context.Background(), store.SnapshotObject, staging, snapDigest)
	if err != nil {
		t.Fatal(err)
	}

	doc, err := schema.Load(strings.NewReader(manifest), schema.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if errs := doc.Validate(); len(errs) > 0 {
		t.Fatalf("manifest: %v", errs)
	}
	rec := registry.Record{ID: testProjectID, Root: "/home/user/project", DisplayName: "project", Revision: 1}
	input := model.CompileInput{
		Project:  rec,
		Manifest: doc.Manifest,
		Snapshot: snapshot.Result{Digest: snapDigest, Path: snapObj.Path},
		// The candidate pipeline records the built tools image digest
		// before compiling; mirror it for the manifest's agents.
		ImageDigests: map[string]domain.Digest{
			model.ToolsImageRef(testProjectID): testToolsDigest,
		},
	}
	for _, opt := range opts {
		opt(st, &input)
	}
	plan, ferrs := model.Compile(input)
	if len(ferrs) > 0 {
		t.Fatalf("compile: %v", ferrs)
	}
	planBytes, err := model.CanonicalJSON(plan)
	if err != nil {
		t.Fatal(err)
	}

	candStaging, err := st.NewStaging("candidate")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candStaging, PlanFileName), planBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	candDigest, _, err := store.DigestTree(candStaging)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Publish(context.Background(), store.CandidateObject, candStaging, candDigest); err != nil {
		t.Fatal(err)
	}
	candRecord := store.CandidateRecord{
		Digest:    candDigest,
		ProjectID: testProjectID,
		Kind:      store.RuntimeCandidate,
		Snapshot:  snapDigest,
		Plan:      domain.SHA256(planBytes),
		CreatedAt: time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC),
	}
	if err := st.WriteCandidateRecord(candRecord); err != nil {
		t.Fatal(err)
	}

	return &fixture{
		svc:    svc,
		docker: docker,
		cand:   Candidate{Record: candRecord, Plan: plan},
		rec:    rec,
	}
}

func TestUpCreatesFullTopology(t *testing.T) {
	f := newFixture(t, testManifest)
	ctx := context.Background()

	state, err := f.svc.Up(ctx, f.cand, UpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !state.Running() {
		t.Fatalf("state not running: %+v", state)
	}

	// Network and volume ensured with engine labels.
	netName := model.NetworkName(testProjectID)
	if _, ok := f.docker.Networks[netName]; !ok {
		t.Fatalf("network %s not created", netName)
	}
	if _, ok := f.docker.Volumes[model.AgentStateVolumeName(testProjectID)]; !ok {
		t.Fatal("agent-state volume not created")
	}

	// Sidecar created before the dev container.
	creates := f.docker.CallsTo("CreateContainer")
	if len(creates) != 2 {
		t.Fatalf("want 2 creates, got %d", len(creates))
	}
	first := creates[0].Request.(dockerapi.CreateRequest)
	if !strings.Contains(string(first.Name), "-svc-db") {
		t.Fatalf("sidecar should be created first, got %s", first.Name)
	}

	// Full request equality for the dev container.
	dev := creates[1].Request.(dockerapi.CreateRequest)
	want := dockerapi.CreateRequest{
		Name:    dockerapi.ContainerName(model.DevContainerName(testProjectID)),
		Image:   model.ToolsImageRef(testProjectID) + "@" + testToolsDigest.String(),
		User:    "vscode",
		Command: []string{"sleep", "infinity"},
		Env:     []string{"SECRET=s3cret", "FLAG=1", "CLAUDE_CONFIG_DIR=/vibe/agent-state/claude", "DISABLE_AUTOUPDATER=1"},
		Labels: map[string]string{
			ManagedLabel:   "true",
			ProjectLabel:   string(testProjectID),
			RoleLabel:      "dev",
			CandidateLabel: f.cand.Record.Digest.String(),
		},
		Mounts: []dockerapi.Mount{
			{Kind: dockerapi.BindMount, Source: "/home/user/project", Target: model.WorkspaceTarget},
			{Kind: dockerapi.VolumeMount, Source: model.AgentStateVolumeName(testProjectID), Target: model.AgentStateTarget},
		},
		Ports:   []dockerapi.PortBinding{},
		Network: netName,
		Policy:  dockerapi.Policy{DropAllCapabilities: true, NoNewPrivileges: true},
	}
	assertCreateEqual(t, dev, want)
}

func assertCreateEqual(t *testing.T, got, want dockerapi.CreateRequest) {
	t.Helper()
	if got.Name != want.Name || got.Image != want.Image || got.User != want.User || got.Network != want.Network || got.Policy != want.Policy {
		t.Fatalf("create request mismatch:\ngot  %+v\nwant %+v", got, want)
	}
	if !equalSlices(got.Command, want.Command) || !equalSlices(got.Env, want.Env) {
		t.Fatalf("command/env mismatch:\ngot  %+v %+v\nwant %+v %+v", got.Command, got.Env, want.Command, want.Env)
	}
	if len(got.Mounts) != len(want.Mounts) {
		t.Fatalf("mounts mismatch:\ngot  %+v\nwant %+v", got.Mounts, want.Mounts)
	}
	for i := range got.Mounts {
		if got.Mounts[i] != want.Mounts[i] {
			t.Fatalf("mount %d mismatch: got %+v want %+v", i, got.Mounts[i], want.Mounts[i])
		}
	}
	if len(got.Ports) != len(want.Ports) {
		t.Fatalf("ports mismatch: %+v vs %+v", got.Ports, want.Ports)
	}
	if len(got.Labels) != len(want.Labels) {
		t.Fatalf("labels mismatch:\ngot  %+v\nwant %+v", got.Labels, want.Labels)
	}
	for k, v := range want.Labels {
		if got.Labels[k] != v {
			t.Fatalf("label %s: got %q want %q", k, got.Labels[k], v)
		}
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestUpIdempotent(t *testing.T) {
	f := newFixture(t, testManifest)
	ctx := context.Background()
	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	before := len(f.docker.CallsTo("CreateContainer"))
	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	if after := len(f.docker.CallsTo("CreateContainer")); after != before {
		t.Fatalf("idempotent up created containers: %d -> %d", before, after)
	}
	if len(f.docker.CallsTo("RemoveContainer")) != 0 {
		t.Fatal("idempotent up removed containers")
	}
}

func TestUpRestartsStopped(t *testing.T) {
	f := newFixture(t, testManifest)
	ctx := context.Background()
	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	devName := dockerapi.ContainerName(model.DevContainerName(testProjectID))
	f.docker.Containers[devName].Running = false

	state, err := f.svc.Up(ctx, f.cand, UpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !state.Running() {
		t.Fatalf("stopped container not restarted: %+v", state)
	}
	if len(f.docker.CallsTo("RemoveContainer")) != 0 {
		t.Fatal("restart should not replace the container")
	}
}

func TestUpForceReplaces(t *testing.T) {
	f := newFixture(t, testManifest)
	ctx := context.Background()
	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Up(ctx, f.cand, UpOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if removed := len(f.docker.CallsTo("RemoveContainer")); removed != 2 {
		t.Fatalf("force should replace both containers, removed %d", removed)
	}
	if creates := len(f.docker.CallsTo("CreateContainer")); creates != 4 {
		t.Fatalf("force should recreate both containers, created %d total", creates)
	}
}

func TestUpRefusesUnmanagedName(t *testing.T) {
	f := newFixture(t, testManifest)
	ctx := context.Background()
	devName := dockerapi.ContainerName(model.DevContainerName(testProjectID))
	f.docker.Containers[devName] = &dockerapi.ContainerState{
		ID: "intruder", Name: devName, Running: true,
		Labels: map[string]string{},
	}
	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("unmanaged name collision should conflict, got %v", err)
	}
}

func TestDownRemovesEverything(t *testing.T) {
	f := newFixture(t, testManifest)
	ctx := context.Background()
	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	approved := f.cand.Record.Digest
	f.rec.Approved = &approved

	if err := f.svc.Down(ctx, f.rec, DownOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(f.docker.Containers) != 0 {
		t.Fatalf("containers left after down: %v", f.docker.Containers)
	}
	if _, ok := f.docker.Networks[model.NetworkName(testProjectID)]; ok {
		t.Fatal("network left after down")
	}
	// Volumes survive a plain down.
	if _, ok := f.docker.Volumes[model.AgentStateVolumeName(testProjectID)]; !ok {
		t.Fatal("volumes must survive down without --volumes")
	}

	if err := f.svc.Down(ctx, f.rec, DownOptions{RemoveVolumes: true}); err != nil {
		t.Fatal(err)
	}
	if len(f.docker.Volumes) != 0 {
		t.Fatalf("volumes left after down --volumes: %v", f.docker.Volumes)
	}
}

func TestStatusReportsSync(t *testing.T) {
	f := newFixture(t, testManifest)
	ctx := context.Background()
	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	approved := f.cand.Record.Digest
	f.rec.Approved = &approved

	state, err := f.svc.Status(ctx, f.rec)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Running() || len(state.Containers) != 2 {
		t.Fatalf("status wrong: %+v", state)
	}

	other := domain.SHA256([]byte("other"))
	f.rec.Approved = &other
	state, err = f.svc.Status(ctx, f.rec)
	if err != nil {
		t.Fatal(err)
	}
	if state.Running() {
		t.Fatal("stale candidate must not report in-sync running state")
	}
}

func TestLoadCandidateVerifiesPlan(t *testing.T) {
	f := newFixture(t, testManifest)
	ctx := context.Background()
	cand, lease, err := f.svc.LoadCandidate(ctx, f.cand.Record.Digest)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if cand.Plan.CanonicalHash != f.cand.Plan.CanonicalHash {
		t.Fatal("loaded plan differs")
	}
	if _, _, err := f.svc.LoadCandidate(ctx, domain.SHA256([]byte("missing"))); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing candidate should be not-found, got %v", err)
	}
}

// TestDownFailsWhenStopFails pins that a StopContainer failure aborts the
// teardown instead of being swallowed.
func TestDownFailsWhenStopFails(t *testing.T) {
	f := newFixture(t, testManifest)
	ctx := context.Background()
	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	f.docker.StopErr = errors.New("stop boom")

	err := f.svc.Down(ctx, f.rec, DownOptions{})
	if err == nil || !strings.Contains(err.Error(), "stop boom") {
		t.Fatalf("down must surface the stop failure, got %v", err)
	}
	// Aborted before removal, so the containers are still present.
	if len(f.docker.CallsTo("RemoveContainer")) != 0 {
		t.Fatal("down should not remove containers after a stop failure")
	}
}

// TestDownFailsWhenRemoveFails pins that a RemoveContainer failure that is
// not ErrNotFound aborts the teardown.
func TestDownFailsWhenRemoveFails(t *testing.T) {
	f := newFixture(t, testManifest)
	ctx := context.Background()
	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	f.docker.RemoveErr = errors.New("remove boom")

	err := f.svc.Down(ctx, f.rec, DownOptions{})
	if err == nil || !strings.Contains(err.Error(), "remove boom") {
		t.Fatalf("down must surface the remove failure, got %v", err)
	}
}

// TestReplaceFailsWhenStopFails pins that the container-replacement path
// (forced up) propagates a StopContainer failure.
func TestReplaceFailsWhenStopFails(t *testing.T) {
	f := newFixture(t, testManifest)
	ctx := context.Background()
	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	f.docker.StopErr = errors.New("stop boom")

	_, err := f.svc.Up(ctx, f.cand, UpOptions{Force: true})
	if err == nil || !strings.Contains(err.Error(), "stop boom") {
		t.Fatalf("forced replace must surface the stop failure, got %v", err)
	}
	if len(f.docker.CallsTo("RemoveContainer")) != 0 {
		t.Fatal("replace should not remove after a stop failure")
	}
}

// TestReplaceFailsWhenRemoveFails pins that the container-replacement path
// propagates a RemoveContainer failure that is not ErrNotFound.
func TestReplaceFailsWhenRemoveFails(t *testing.T) {
	f := newFixture(t, testManifest)
	ctx := context.Background()
	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	f.docker.RemoveErr = errors.New("remove boom")

	_, err := f.svc.Up(ctx, f.cand, UpOptions{Force: true})
	if err == nil || !strings.Contains(err.Error(), "remove boom") {
		t.Fatalf("forced replace must surface the remove failure, got %v", err)
	}
}

// TestCreateRetriesPullOnMissingImage pins the create → ErrNotFound →
// pull → re-create retry: the first create for the sidecar fails
// not-found, the image is pulled by the right ref, and the retried create
// succeeds so the topology comes up.
func TestCreateRetriesPullOnMissingImage(t *testing.T) {
	f := newFixture(t, testManifest)
	ctx := context.Background()
	f.docker.CreateErrOnce = fmt.Errorf("%w: no such image", domain.ErrNotFound)

	state, err := f.svc.Up(ctx, f.cand, UpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !state.Running() {
		t.Fatalf("topology not running after pull-retry: %+v", state)
	}

	pulls := f.docker.CallsTo("PullImage")
	if len(pulls) != 1 {
		t.Fatalf("want exactly one pull, got %d", len(pulls))
	}
	// The failed create is recorded, then the retry, then the dev create.
	creates := f.docker.CallsTo("CreateContainer")
	if len(creates) != 3 {
		t.Fatalf("want 3 create attempts (fail, retry, dev), got %d", len(creates))
	}
	failed := creates[0].Request.(dockerapi.CreateRequest)
	pulled := pulls[0].Request.(dockerapi.ResolvedImage)
	if pulled.Ref != dockerapi.ImageRef(failed.Image) {
		t.Fatalf("pull ref mismatch: got %q want %q", pulled.Ref, failed.Image)
	}
	retry := creates[1].Request.(dockerapi.CreateRequest)
	if retry.Name != failed.Name {
		t.Fatalf("retry targeted a different container: %q vs %q", retry.Name, failed.Name)
	}
}

// TestDownVolumesFailsOnMissingCandidate pins that Down --volumes fails
// loudly when the approved candidate cannot be loaded, rather than
// silently keeping the plan-declared sidecar volumes.
func TestDownVolumesFailsOnMissingCandidate(t *testing.T) {
	f := newFixture(t, testManifest)
	ctx := context.Background()
	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	bogus := domain.SHA256([]byte("gc'd candidate"))
	f.rec.Approved = &bogus

	err := f.svc.Down(ctx, f.rec, DownOptions{RemoveVolumes: true})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("down --volumes with a missing approved candidate should be not-found, got %v", err)
	}
	// Failing before the removal loop must not partially delete volumes,
	// including the always-derivable agent-state volume.
	if _, ok := f.docker.Volumes[model.AgentStateVolumeName(testProjectID)]; !ok {
		t.Fatal("down must not delete any volume when it aborts on candidate load")
	}
	if len(f.docker.CallsTo("RemoveVolume")) != 0 {
		t.Fatal("no volume removal should be attempted after the load failure")
	}
}

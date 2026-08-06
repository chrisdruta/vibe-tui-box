package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vibe/internal/dev"
	"vibe/internal/dockerapi"
	"vibe/internal/domain"
	"vibe/internal/registry"
	"vibe/internal/store"
)

// publishTestArtifact fabricates one installed artifact with a chosen
// provenance source and install time.
func publishTestArtifact(t *testing.T, a *App, version, source string, at time.Time) domain.Digest {
	t.Helper()
	staging, err := a.deps.Store.NewStaging("art")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(staging, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "bin", "vibe"), []byte("binary-"+version), 0o755); err != nil {
		t.Fatal(err)
	}
	digest, _, err := store.DigestTree(staging)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.deps.Store.Publish(context.Background(), store.ArtifactObject, staging, digest); err != nil {
		t.Fatal(err)
	}
	if err := a.deps.Store.WriteArtifactRecord(store.ArtifactRecord{
		Digest:      digest,
		Version:     version,
		Release:     domain.ReleaseProvenance{Source: source, Version: version},
		InstalledAt: at,
	}); err != nil {
		t.Fatal(err)
	}
	return digest
}

func removedIDs(entries []GCEntry) map[string]bool {
	out := map[string]bool{}
	for _, e := range entries {
		out[e.Kind+" "+e.ID] = true
	}
	return out
}

// realClock: GC ages objects by file mtime against Clock.Now, so the
// fixed test clock (which sits in the files' past) would shield
// everything even at MinAge 0.
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func TestGCFlow(t *testing.T) {
	a, docker := newTestApp(t)
	a.deps.Clock = realClock{}
	ctx := context.Background()
	dir := newProject(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}

	// Three candidates: superseded (c1), approved (c2), pending (c3).
	up1, err := a.Up(ctx, UpRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	c1 := up1.Candidate
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=v2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	up2, err := a.Up(ctx, UpRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	c2 := up2.Candidate
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=v3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeRequestFile(t, dir, "req-gc",
		`{"format":1,"id":"req-gc","kind":"rebuild","reason":"r","summary":"s"}`)
	list, err := a.RequestList(ctx, RequestListRequest{Dir: dir})
	if err != nil || len(list.Pending) != 1 {
		t.Fatalf("pending: %+v, %v", list.Pending, err)
	}
	c3 := list.Pending[0].Candidate

	s1 := mustCandidateSnapshot(t, a, c1)
	s2 := mustCandidateSnapshot(t, a, c2)

	// Artifacts: an old release, the newest release, a stale dev build.
	base := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	relOld := publishTestArtifact(t, a, "v0.9.0", "release", base)
	relNew := publishTestArtifact(t, a, "v1.0.0", "release", base.Add(time.Hour))
	devArt := publishTestArtifact(t, a, "dev-src-abc", "dev-build", base.Add(2*time.Hour))

	// Binaries: the current handoff target plus one stale copy.
	bin := a.deps.Layout.Bin
	current := "vibe-" + relNew.Hex()[:12]
	if err := os.WriteFile(filepath.Join(bin, current), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(bin, current), filepath.Join(bin, "vibe")); err != nil {
		t.Fatal(err)
	}
	stale := "vibe-" + relOld.Hex()[:12]
	if err := os.WriteFile(filepath.Join(bin, stale), []byte("y"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Dry run: plans the removals, touches nothing.
	dry, err := a.GC(ctx, GCRequest{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	ids := removedIDs(dry.Removed)
	for _, want := range []string{
		"candidate " + c1.String(),
		"snapshot " + s1.String(),
		"artifact " + relOld.String(),
		"artifact " + devArt.String(),
		"binary " + stale,
	} {
		if !ids[want] {
			t.Fatalf("dry run missing %q in %v", want, dry.Removed)
		}
	}
	for _, keep := range []string{
		"candidate " + c2.String(),
		"candidate " + c3.String(),
		"snapshot " + s2.String(),
		"artifact " + relNew.String(),
		"binary " + current,
	} {
		if ids[keep] {
			t.Fatalf("dry run would remove a root: %q", keep)
		}
	}
	if ok, _ := a.deps.Store.Exists(ctx, store.CandidateObject, c1); !ok {
		t.Fatal("dry run removed an object")
	}

	// A live lease shields an otherwise-unreferenced object.
	lease, err := a.deps.Store.Open(ctx, store.CandidateObject, c1)
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.GC(ctx, GCRequest{})
	if err != nil {
		t.Fatal(err)
	}
	lease.Close()
	skipped := removedIDs(got.Skipped)
	if !skipped["candidate "+c1.String()] {
		t.Fatalf("leased candidate not skipped: %+v", got.Skipped)
	}
	if ok, _ := a.deps.Store.Exists(ctx, store.ArtifactObject, relOld); ok {
		t.Fatal("old release artifact survived")
	}
	if _, err := os.Lstat(filepath.Join(bin, stale)); !os.IsNotExist(err) {
		t.Fatal("stale binary survived")
	}
	if _, err := os.Lstat(filepath.Join(bin, current)); err != nil {
		t.Fatal("current binary was removed")
	}

	// Lease released: the object goes on the next run.
	if _, err := a.GC(ctx, GCRequest{}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := a.deps.Store.Exists(ctx, store.CandidateObject, c1); ok {
		t.Fatal("unreferenced candidate survived")
	}
	if ok, _ := a.deps.Store.Exists(ctx, store.SnapshotObject, s1); ok {
		t.Fatal("unreferenced snapshot survived")
	}
	for _, keep := range []domain.Digest{c2, c3} {
		if ok, _ := a.deps.Store.Exists(ctx, store.CandidateObject, keep); !ok {
			t.Fatalf("kept candidate %s vanished", keep)
		}
	}

	// MinAge shields young objects: everything here has a fresh mtime
	// while the fixed test clock sits in their past.
	if res, err := a.GC(ctx, GCRequest{MinAge: time.Hour}); err != nil || len(res.Removed) != 0 {
		t.Fatalf("min-age run removed %+v, %v", res.Removed, err)
	}

	// Forgetting the project releases its registry state — but its
	// containers still exist, and live containers root their candidate:
	// only the pending candidate (no containers) goes now.
	rec := mustResolve(t, a, dir)
	if _, err := a.Forget(ctx, ForgetRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	after, err := a.GC(ctx, GCRequest{})
	if err != nil {
		t.Fatal(err)
	}
	ids = removedIDs(after.Removed)
	if !ids["broker "+string(rec.ID)] {
		t.Fatalf("stale broker dir not removed: %+v", after.Removed)
	}
	if ok, _ := a.deps.Store.Exists(ctx, store.CandidateObject, c2); !ok {
		t.Fatal("live containers must keep rooting their candidate after forget")
	}
	if ok, _ := a.deps.Store.Exists(ctx, store.CandidateObject, c3); ok {
		t.Fatal("forgotten project's pending candidate survived")
	}

	// Once the containers are gone too, the candidate follows.
	docker.Containers = map[dockerapi.ContainerName]*dockerapi.ContainerState{}
	if _, err := a.GC(ctx, GCRequest{}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := a.deps.Store.Exists(ctx, store.CandidateObject, c2); ok {
		t.Fatal("forgotten project's candidate survived with no containers")
	}
	if ok, _ := a.deps.Store.Exists(ctx, store.ArtifactObject, relNew); !ok {
		t.Fatal("newest release artifact must survive with no projects")
	}
}

func mustCandidateSnapshot(t *testing.T, a *App, digest domain.Digest) domain.Digest {
	t.Helper()
	rec, err := a.deps.Store.ReadCandidateRecord(digest)
	if err != nil {
		t.Fatal(err)
	}
	return rec.Snapshot
}

// TestGCInterruptedApproval pins the live-container root: containers
// running a candidate the registry no longer points at (a reconcile
// that never reached its registry update, or one rolled back on paper
// only) must keep that candidate and its snapshot alive.
func TestGCInterruptedApproval(t *testing.T) {
	a, _ := newTestApp(t)
	a.deps.Clock = realClock{}
	ctx := context.Background()
	dir := newProject(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	up1, err := a.Up(ctx, UpRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	c1 := up1.Candidate
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=v2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	up2, err := a.Up(ctx, UpRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	c2 := up2.Candidate
	s2 := mustCandidateSnapshot(t, a, c2)

	// Simulate the interrupted approval: containers stay on c2 while
	// the registry pointer moves back to c1.
	rec := mustResolve(t, a, dir)
	if _, err := a.deps.Registry.Update(ctx, rec.ID, rec.Revision, func(r *registry.Record) error {
		r.Approved = &c1
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := a.GC(ctx, GCRequest{}); err != nil {
		t.Fatal(err)
	}
	for what, d := range map[string]domain.Digest{"candidate c2": c2, "snapshot s2": s2, "candidate c1": c1} {
		kind := store.CandidateObject
		if what == "snapshot s2" {
			kind = store.SnapshotObject
		}
		if ok, _ := a.deps.Store.Exists(ctx, kind, d); !ok {
			t.Fatalf("%s must survive: containers or the registry still reference it", what)
		}
	}
}

// TestGCFailsClosedOnBadContainerMetadata pins that GC aborts rather
// than collects when live-container metadata cannot be trusted.
func TestGCFailsClosedOnBadContainerMetadata(t *testing.T) {
	a, docker := newTestApp(t)
	ctx := context.Background()
	docker.Containers["rogue"] = &dockerapi.ContainerState{
		ID:   "ctr-rogue",
		Name: "rogue",
		Labels: map[string]string{
			"dev.vibe.managed":   "true",
			"dev.vibe.project":   "abcdefghijklmnopqrstuvwxyz",
			"dev.vibe.candidate": "not-a-digest",
		},
	}
	if _, err := a.GC(ctx, GCRequest{}); err == nil || !strings.Contains(err.Error(), "malformed candidate label") {
		t.Fatalf("malformed candidate label must abort gc, got %v", err)
	}

	docker.Containers["rogue"].Labels["dev.vibe.candidate"] = ""
	if _, err := a.GC(ctx, GCRequest{}); err == nil {
		t.Fatal("missing candidate label must abort gc")
	}
}

// TestGCFailsClosedWithoutDocker pins that an unreachable daemon
// aborts GC: without the live-container roots the root set is
// incomplete.
func TestGCFailsClosedWithoutDocker(t *testing.T) {
	a, docker := newTestApp(t)
	docker.PingErr = errors.New("daemon down")
	if _, err := a.GC(context.Background(), GCRequest{}); err == nil || !strings.Contains(err.Error(), "daemon") {
		t.Fatalf("gc without docker must fail closed, got %v", err)
	}
}

// TestGCFailsClosedOnBadArtifactLabelAndMissingRecord pins the two
// remaining fail-closed metadata paths: a present-but-malformed
// artifact label, and a live-container candidate whose record cannot
// be read.
func TestGCFailsClosedOnBadArtifactLabelAndMissingRecord(t *testing.T) {
	a, docker := newTestApp(t)
	ctx := context.Background()
	good := domain.SHA256([]byte("candidate")).String()
	docker.Containers["rogue"] = &dockerapi.ContainerState{
		ID:   "ctr-rogue",
		Name: "rogue",
		Labels: map[string]string{
			"dev.vibe.managed":   "true",
			"dev.vibe.project":   "abcdefghijklmnopqrstuvwxyz",
			"dev.vibe.candidate": good,
			"dev.vibe.artifact":  "not-a-digest",
		},
	}
	if _, err := a.GC(ctx, GCRequest{}); err == nil || !strings.Contains(err.Error(), "malformed artifact label") {
		t.Fatalf("malformed artifact label must abort gc, got %v", err)
	}

	// Valid labels, but no candidate record exists for the digest.
	delete(docker.Containers["rogue"].Labels, "dev.vibe.artifact")
	if _, err := a.GC(ctx, GCRequest{}); err == nil || !strings.Contains(err.Error(), "record is unreadable") {
		t.Fatalf("unreadable candidate record for a live root must abort gc, got %v", err)
	}
}

// TestGCSkipsDevBuilder pins the dev↔gc agreement: the throwaway
// build container is managed but project- and candidate-less by
// design, and a stale one (hard kill mid-build) must not wedge GC
// (review 2026-08-05 §2.7). The label set mirrors dev.runBuild's.
func TestGCSkipsDevBuilder(t *testing.T) {
	a, docker := newTestApp(t)
	docker.Containers["vibe-devbuild-abc"] = &dockerapi.ContainerState{
		ID:   "ctr-devbuild",
		Name: "vibe-devbuild-abc",
		Labels: map[string]string{
			"dev.vibe.managed": "true",
			"dev.vibe.role":    dev.BuilderRole,
		},
	}
	if _, err := a.GC(context.Background(), GCRequest{}); err != nil {
		t.Fatalf("a live dev-builder must not wedge gc: %v", err)
	}
}

// TestGCFailsClosedOnMissingProjectLabel pins that a managed container
// that cannot name its project aborts GC — the project label gates
// journal cleanup.
func TestGCFailsClosedOnMissingProjectLabel(t *testing.T) {
	a, docker := newTestApp(t)
	docker.Containers["rogue"] = &dockerapi.ContainerState{
		ID:   "ctr-rogue",
		Name: "rogue",
		Labels: map[string]string{
			"dev.vibe.managed":   "true",
			"dev.vibe.candidate": domain.SHA256([]byte("c")).String(),
		},
	}
	if _, err := a.GC(context.Background(), GCRequest{}); err == nil || !strings.Contains(err.Error(), "project label") {
		t.Fatalf("missing project label must abort gc, got %v", err)
	}

	// Nonempty but malformed is equally fail-closed: a misfiled project
	// key would let the real project's journal look container-free.
	docker.Containers["rogue"].Labels["dev.vibe.project"] = "Not-A-Valid-Project-Id!"
	if _, err := a.GC(context.Background(), GCRequest{}); err == nil || !strings.Contains(err.Error(), "project label") {
		t.Fatalf("malformed project label must abort gc, got %v", err)
	}
}

// TestGCJournalCleanupRespectsLiveContainers pins that an unregistered
// project's replacement journal is only collected once the project has
// no managed containers left.
func TestGCJournalCleanupRespectsLiveContainers(t *testing.T) {
	a, docker := newTestApp(t)
	a.deps.Clock = realClock{}
	ctx := context.Background()
	dir := newProject(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Up(ctx, UpRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	rec := mustResolve(t, a, dir)
	replaceDir := filepath.Join(a.deps.Layout.State, "replace")
	if err := os.MkdirAll(replaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	journalFile := filepath.Join(replaceDir, string(rec.ID)+".json")
	if err := os.WriteFile(journalFile, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Forget(ctx, ForgetRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}

	// Containers still exist: the journal must be skipped, not removed.
	res, err := a.GC(ctx, GCRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(journalFile); err != nil {
		t.Fatalf("journal of a live unregistered project must survive: %v", err)
	}
	found := false
	for _, e := range res.Skipped {
		if strings.Contains(e.ID, "replace-journal") {
			found = true
		}
	}
	if !found {
		t.Fatalf("skipped journal must be reported: %+v", res.Skipped)
	}

	// Containers gone: the journal follows.
	docker.Containers = map[dockerapi.ContainerName]*dockerapi.ContainerState{}
	if _, err := a.GC(ctx, GCRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(journalFile); !os.IsNotExist(err) {
		t.Fatalf("journal of a containerless unregistered project must be collected, stat err=%v", err)
	}
}

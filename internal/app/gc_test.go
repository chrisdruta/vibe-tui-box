package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/store"
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
	a, _ := newTestApp(t)
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

	// Forgetting the project releases its state: broker dir, approved and
	// pending candidates; the newest release artifact stays.
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
	for _, c := range []domain.Digest{c2, c3} {
		if ok, _ := a.deps.Store.Exists(ctx, store.CandidateObject, c); ok {
			t.Fatalf("forgotten project's candidate %s survived", c)
		}
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

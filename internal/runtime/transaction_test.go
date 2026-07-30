package runtime

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chrisdruta/vibe-tui-box/internal/dockerapi"
	"github.com/chrisdruta/vibe-tui-box/internal/domain"
)

// preexistingIDs snapshots which container IDs exist right now, so
// hooks can target only the containers a later transaction creates.
func preexistingIDs(f *fixture) map[dockerapi.ContainerID]bool {
	out := map[dockerapi.ContainerID]bool{}
	for _, c := range f.docker.Containers {
		out[c.ID] = true
	}
	return out
}

// TestReplaceStagingPullFailureLeavesOldUntouched pins that a pull
// failure during staging — before anything was stopped or renamed —
// leaves the old generation completely untouched and running.
func TestReplaceStagingPullFailureLeavesOldUntouched(t *testing.T) {
	f := newFixture(t, testManifest)
	ctx := context.Background()
	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	before := runningNames(f.docker)
	f.docker.CreateErrOnce = fmt.Errorf("%w: image gone", domain.ErrNotFound)
	// Fail the pull of whichever image the staged create asks for.
	for _, c := range f.docker.Containers {
		f.docker.PullErrs[dockerapi.ImageRef(c.Image)] = errors.New("pull boom")
	}

	_, err := f.svc.Up(ctx, f.cand, UpOptions{Force: true})
	if err == nil || !strings.Contains(err.Error(), "pull boom") {
		t.Fatalf("staging pull failure must surface, got %v", err)
	}
	if len(f.docker.CallsTo("StopContainer")) != 0 || len(f.docker.CallsTo("RenameContainer")) != 0 {
		t.Fatal("staging failure must not have touched the old generation")
	}
	assertRolledBack(t, f, before)
}

// TestReplaceLivenessFailureRollsBack pins the pre-commit gate: a new
// container whose start "succeeds" but which exits immediately must
// fail the transaction and restore the old generation.
func TestReplaceLivenessFailureRollsBack(t *testing.T) {
	f := newFixture(t, testManifest)
	ctx := context.Background()
	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	before := runningNames(f.docker)
	pre := preexistingIDs(f)
	f.docker.StartExits = map[dockerapi.ContainerID]bool{}
	f.docker.CreateHook = func(req dockerapi.CreateRequest) {
		if c, ok := f.docker.Containers[req.Name]; ok && !pre[c.ID] {
			f.docker.StartExits[c.ID] = true
		}
	}

	_, err := f.svc.Up(ctx, f.cand, UpOptions{Force: true})
	if err == nil || !strings.Contains(err.Error(), "exited after start") {
		t.Fatalf("immediate-exit replacement must fail the pre-commit liveness gate, got %v", err)
	}
	assertRolledBack(t, f, before)
}

// TestMidwayFailureRollsBackEarlierSwaps pins topology atomicity: when
// the dev container's replacement fails AFTER a sidecar was already
// swapped to the new generation, the sidecar must come back on its old
// generation — no mixed topology survives.
func TestMidwayFailureRollsBackEarlierSwaps(t *testing.T) {
	f := newFixture(t, testManifest)
	ctx := context.Background()
	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	before := runningNames(f.docker)
	beforeIDs := map[dockerapi.ContainerName]dockerapi.ContainerID{}
	for name, c := range f.docker.Containers {
		beforeIDs[name] = c.ID
	}
	pre := preexistingIDs(f)
	f.docker.StartHookErr = func(c dockerapi.ContainerState) error {
		if !pre[c.ID] && strings.Contains(string(c.Name), "-dev") {
			return errors.New("dev start boom")
		}
		return nil
	}

	_, err := f.svc.Up(ctx, f.cand, UpOptions{Force: true})
	if err == nil || !strings.Contains(err.Error(), "dev start boom") {
		t.Fatalf("dev replacement failure must surface, got %v", err)
	}
	assertRolledBack(t, f, before)
	for name, id := range beforeIDs {
		if c, ok := f.docker.Containers[name]; !ok || c.ID != id {
			t.Fatalf("container %s is not the original generation after rollback", name)
		}
	}
}

// TestSweepCompletesFailedRollback pins the retry chain: a rollback
// that itself fails retains the journal, and the next up's sweep
// finishes the recovery from the journal before reconciling.
func TestSweepCompletesFailedRollback(t *testing.T) {
	f := newFixture(t, testManifest)
	ctx := context.Background()
	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	before := runningNames(f.docker)
	beforeIDs := map[dockerapi.ContainerName]dockerapi.ContainerID{}
	for name, c := range f.docker.Containers {
		beforeIDs[name] = c.ID
	}
	pre := preexistingIDs(f)
	// Forward failure: the staged container cannot be swapped in.
	f.docker.RenameHookErr = func(c dockerapi.ContainerState, to dockerapi.ContainerName) error {
		if !pre[c.ID] {
			return errors.New("swap rename boom")
		}
		return nil
	}
	// Rollback failure: the retired old container cannot be restarted.
	f.docker.StartHookErr = func(c dockerapi.ContainerState) error {
		if pre[c.ID] {
			return errors.New("restore start boom")
		}
		return nil
	}

	_, err := f.svc.Up(ctx, f.cand, UpOptions{Force: true})
	if err == nil || !strings.Contains(err.Error(), "swap rename boom") || !strings.Contains(err.Error(), "restore start boom") {
		t.Fatalf("failed rollback must join both errors, got %v", err)
	}
	if _, err := os.Stat(f.svc.journalPath(testProjectID)); err != nil {
		t.Fatalf("journal must be retained after a failed rollback: %v", err)
	}

	// The failure cleared: the next up recovers from the journal, then
	// reconciles normally.
	f.docker.RenameHookErr = nil
	f.docker.StartHookErr = nil
	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	assertRolledBack(t, f, before)
	for name, id := range beforeIDs {
		if c, ok := f.docker.Containers[name]; !ok || c.ID != id {
			t.Fatalf("container %s is not the original generation after recovery", name)
		}
	}
}

// TestCorruptJournalFailsClosed pins that an unreadable journal aborts
// up before any container is touched.
func TestCorruptJournalFailsClosed(t *testing.T) {
	f := newFixture(t, testManifest)
	ctx := context.Background()
	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(f.svc.replaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.svc.journalPath(testProjectID), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := len(f.docker.Calls())

	_, err := f.svc.Up(ctx, f.cand, UpOptions{})
	if err == nil || !strings.Contains(err.Error(), "unreadable") {
		t.Fatalf("corrupt journal must fail closed, got %v", err)
	}
	if len(f.docker.Calls()) != calls {
		t.Fatal("corrupt journal must abort before any Docker call")
	}
}

// TestUndoKeepsUnprovenNewWhenOldGone pins the fail-closed rule: when
// the old container vanished externally, recovery keeps the unproven
// replacement (an unproven container running beats nothing) and
// retains the journal.
func TestUndoKeepsUnprovenNewWhenOldGone(t *testing.T) {
	f := newFixture(t, testManifest)
	ctx := context.Background()
	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	// Craft a journal claiming the running db container is an unproven
	// replacement of an old container that no longer exists.
	var db *dockerapi.ContainerState
	for _, c := range f.docker.Containers {
		if strings.Contains(string(c.Name), "-svc-db") {
			db = c
		}
	}
	if db == nil {
		t.Fatal("no db container")
	}
	j := &journal{
		Version:   journalVersion,
		Candidate: f.cand.Record.Digest.String(),
		Nonce:     strings.Repeat("ab", 16),
		Entries: []journalEntry{{
			Kind: "replace", Name: string(db.Name), OldID: "ctr-gone", WasRunning: true, NewID: string(db.ID),
		}},
	}
	if err := f.svc.writeJournal(testProjectID, j); err != nil {
		t.Fatal(err)
	}

	_, err := f.svc.Up(ctx, f.cand, UpOptions{})
	if err == nil || !strings.Contains(err.Error(), "keeping unproven replacement") {
		t.Fatalf("missing old container must fail closed, got %v", err)
	}
	if _, ok := f.docker.Containers[db.Name]; !ok {
		t.Fatal("the unproven replacement must not be deleted")
	}
	if _, err := os.Stat(f.svc.journalPath(testProjectID)); err != nil {
		t.Fatalf("journal must be retained on fail-closed recovery: %v", err)
	}
}

// TestSweepRestoresRetiredNotSwapped pins recovery of a crash between
// retire and swap: old parked at .prev, new staged at .next — the old
// container must come back under its canonical name and the staged one
// must go.
func TestSweepRestoresRetiredNotSwapped(t *testing.T) {
	f := newFixture(t, testManifest)
	ctx := context.Background()
	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	var db *dockerapi.ContainerState
	for _, c := range f.docker.Containers {
		if strings.Contains(string(c.Name), "-svc-db") {
			db = c
		}
	}
	canonical := db.Name
	nonce := strings.Repeat("cd", 16)

	// Crash shape: old renamed to .prev (stopped), staged .next exists.
	db.Running = false
	delete(f.docker.Containers, canonical)
	db.Name = canonical + retiredSuffix
	f.docker.Containers[db.Name] = db
	stagedLabels := map[string]string{}
	maps.Copy(stagedLabels, db.Labels)
	stagedLabels[TxnLabel] = nonce
	staged := &dockerapi.ContainerState{ID: "ctr-staged", Name: canonical + stagedSuffix, Labels: stagedLabels}
	f.docker.Containers[staged.Name] = staged
	j := &journal{
		Version:   journalVersion,
		Candidate: f.cand.Record.Digest.String(),
		Nonce:     nonce,
		Entries: []journalEntry{{
			Kind: "replace", Name: string(canonical), OldID: string(db.ID), WasRunning: true, NewID: "ctr-staged",
		}},
	}
	if err := f.svc.writeJournal(testProjectID, j); err != nil {
		t.Fatal(err)
	}

	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	restored, ok := f.docker.Containers[canonical]
	if !ok || restored.ID != db.ID || !restored.Running {
		t.Fatalf("old container not restored running at %s: %+v", canonical, restored)
	}
	for name := range f.docker.Containers {
		if strings.HasSuffix(string(name), ".prev") || strings.HasSuffix(string(name), ".next") {
			t.Fatalf("leftover %s survived recovery", name)
		}
	}
	if _, err := os.Stat(f.svc.journalPath(testProjectID)); !os.IsNotExist(err) {
		t.Fatalf("journal should be gone after recovery, stat err=%v", err)
	}
}

// TestSweepClearsTempDebris pins that only this project's atomic-write
// temp files are collected.
func TestSweepClearsTempDebris(t *testing.T) {
	f := newFixture(t, testManifest)
	ctx := context.Background()
	if err := os.MkdirAll(f.svc.replaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(f.svc.replaceDir, "."+string(testProjectID)+".json.tmp-123")
	other := filepath.Join(f.svc.replaceDir, ".otherproject.json.tmp-456")
	for _, p := range []string{mine, other} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mine); !os.IsNotExist(err) {
		t.Fatal("own temp debris survived the sweep")
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal("another project's temp file must not be touched")
	}
}

// TestUndoFailsClosedWhenBothGenerationsGone pins that recovery never
// deletes its journal when neither the old container nor its
// replacement exists — there is no usable outcome to converge on, so
// the operator decides.
func TestUndoFailsClosedWhenBothGenerationsGone(t *testing.T) {
	f := newFixture(t, testManifest)
	ctx := context.Background()
	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	j := &journal{
		Version:   journalVersion,
		Candidate: f.cand.Record.Digest.String(),
		Nonce:     strings.Repeat("ef", 16),
		Entries: []journalEntry{{
			Kind: "replace", Name: "vibe-gone-svc-x", OldID: "ctr-gone-old", WasRunning: true, NewID: "ctr-gone-new",
		}},
	}
	if err := f.svc.writeJournal(testProjectID, j); err != nil {
		t.Fatal(err)
	}

	_, err := f.svc.Up(ctx, f.cand, UpOptions{})
	if err == nil || !strings.Contains(err.Error(), "neither old container") {
		t.Fatalf("both generations missing must fail closed, got %v", err)
	}
	if _, err := os.Stat(f.svc.journalPath(testProjectID)); err != nil {
		t.Fatalf("journal must be retained when recovery has no usable outcome: %v", err)
	}
}

// TestUndoFailsClosedOnForeignCanonical pins that recovery refuses to
// touch a container occupying a transaction-owned name whose identity
// matches neither the old nor the new container.
func TestUndoFailsClosedOnForeignCanonical(t *testing.T) {
	f := newFixture(t, testManifest)
	ctx := context.Background()
	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	var db *dockerapi.ContainerState
	for _, c := range f.docker.Containers {
		if strings.Contains(string(c.Name), "-svc-db") {
			db = c
		}
	}
	canonical := db.Name
	// Old exists parked at .prev; a foreign container (not managed by
	// this transaction, wrong ID) squats on the canonical name.
	delete(f.docker.Containers, canonical)
	db.Name = canonical + retiredSuffix
	db.Running = false
	f.docker.Containers[db.Name] = db
	f.docker.Containers[canonical] = &dockerapi.ContainerState{
		ID:   "ctr-foreign",
		Name: canonical,
		Labels: map[string]string{
			ManagedLabel: "true", ProjectLabel: string(testProjectID),
		},
	}
	j := &journal{
		Version:   journalVersion,
		Candidate: f.cand.Record.Digest.String(),
		Nonce:     strings.Repeat("aa", 16),
		Entries: []journalEntry{{
			Kind: "replace", Name: string(canonical), OldID: string(db.ID), WasRunning: true,
		}},
	}
	if err := f.svc.writeJournal(testProjectID, j); err != nil {
		t.Fatal(err)
	}

	_, err := f.svc.Up(ctx, f.cand, UpOptions{})
	if err == nil || !strings.Contains(err.Error(), "not touching it") {
		t.Fatalf("foreign container at canonical must fail closed, got %v", err)
	}
	if c, ok := f.docker.Containers[canonical]; !ok || c.ID != "ctr-foreign" {
		t.Fatal("foreign container must be untouched")
	}
	if _, err := os.Stat(f.svc.journalPath(testProjectID)); err != nil {
		t.Fatalf("journal must be retained on fail-closed recovery: %v", err)
	}
}

// TestJournalKindInvariants pins the strict decode contract: entries
// missing the fields their undo keys on are corruption, and trailing
// data after the document is rejected.
func TestJournalKindInvariants(t *testing.T) {
	f := newFixture(t, testManifest)
	ctx := context.Background()
	if err := os.MkdirAll(f.svc.replaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	valid := `{"version":1,"candidate":"` + f.cand.Record.Digest.String() + `","nonce":"` + strings.Repeat("ab", 16) + `"`
	for name, body := range map[string]string{
		"replace without old_id": valid + `,"entries":[{"kind":"replace","name":"x"}]}`,
		"start without id":       valid + `,"entries":[{"kind":"start","name":"x"}]}`,
		"trailing data":          valid + `,"entries":[]}{"extra":1}`,
	} {
		if err := os.WriteFile(f.svc.journalPath(testProjectID), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err == nil || !strings.Contains(err.Error(), "unreadable") {
			t.Fatalf("%s must fail closed, got %v", name, err)
		}
	}
}

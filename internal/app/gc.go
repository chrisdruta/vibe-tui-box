package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"vibe/internal/dev"
	"vibe/internal/domain"
	"vibe/internal/lock"
	"vibe/internal/runtime"
	"vibe/internal/store"
)

// GCRequest controls explicit store garbage collection. Nothing is ever
// collected implicitly: this is the one entry point, and it only
// removes what no registry pin, approved-candidate pointer, pending
// broker binding, or live lease is holding. MinAge additionally shields
// in-flight work — an object younger than it is never a candidate for
// removal (the CLI default is one hour).
type GCRequest struct {
	DryRun bool
	MinAge time.Duration
}

// GCEntry is one removed or skipped item.
type GCEntry struct {
	Kind   string `json:"kind"` // artifact | candidate | snapshot | record | binary | broker | state
	ID     string `json:"id"`
	Bytes  int64  `json:"bytes,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type GCResult struct {
	DryRun         bool
	Removed        []GCEntry
	Skipped        []GCEntry // held by a live lease
	StagingEntries int
	StagingBytes   int64
	TotalBytes     int64
}

func (a *App) GC(ctx context.Context, req GCRequest) (GCResult, error) {
	fail := opFail[GCResult]("gc", "")
	held, err := a.deps.Locks.Acquire(ctx, lock.StoreGlobal())
	if err != nil {
		return fail(err)
	}
	defer held.Release()

	// Live containers are GC roots, so an unreachable daemon means the
	// root set cannot be computed — fail closed rather than collect
	// objects a running deployment may reference.
	if err := a.deps.Docker.Ping(ctx); err != nil {
		return fail(fmt.Errorf("gc needs the Docker daemon to enumerate live containers (start Docker and rerun): %w", err))
	}

	cutoff := a.deps.Clock.Now().Add(-req.MinAge)
	roots, err := a.gcRoots(ctx)
	if err != nil {
		return fail(err)
	}

	result := GCResult{DryRun: req.DryRun}
	remove := func(kind store.ObjectKind, keep map[domain.Digest]bool) error {
		digests, err := a.deps.Store.ListObjects(kind)
		if err != nil {
			return err
		}
		for _, d := range digests {
			if keep[d] {
				continue
			}
			stat, err := a.deps.Store.ObjectStat(kind, d)
			if err != nil {
				continue // raced away already
			}
			if stat.ModTime.After(cutoff) {
				continue // young: in-flight work is not garbage yet
			}
			entry := GCEntry{Kind: string(kind), ID: d.String(), Bytes: stat.Bytes}
			if !req.DryRun {
				switch err := a.deps.Store.RemoveObject(ctx, kind, d); {
				case errors.Is(err, domain.ErrConflict):
					entry.Reason = "in use"
					result.Skipped = append(result.Skipped, entry)
					continue
				case err != nil:
					return err
				}
			}
			result.Removed = append(result.Removed, entry)
			result.TotalBytes += entry.Bytes
		}
		return nil
	}
	if err := remove(store.ArtifactObject, roots.artifacts); err != nil {
		return fail(err)
	}
	if err := remove(store.CandidateObject, roots.candidates); err != nil {
		return fail(err)
	}
	if err := remove(store.SnapshotObject, roots.snapshots); err != nil {
		return fail(err)
	}
	if err := a.gcOrphanRecords(ctx, req, roots, &result); err != nil {
		return fail(err)
	}

	entries, bytes, err := a.deps.Store.CleanStaging(cutoff, req.DryRun)
	if err != nil {
		return fail(err)
	}
	result.StagingEntries, result.StagingBytes = entries, bytes
	result.TotalBytes += bytes

	if err := a.gcBinaries(req, roots, &result); err != nil {
		return fail(err)
	}
	if err := a.gcProjectState(req, roots, &result); err != nil {
		return fail(err)
	}
	return result, nil
}

// gcRoots gathers everything that must survive: per-project artifact
// pins, the newest release artifact (`dev off` hands back to it),
// approved candidates, candidates bound to pending broker requests,
// candidates and artifacts referenced by live managed containers (a
// reconcile that never reached its registry update still has a running
// deployment), and the snapshots those candidates reference.
type gcRoots struct {
	registered map[domain.ProjectID]bool
	// liveProjects holds every project with at least one managed
	// container, whether or not it is still registered.
	liveProjects map[domain.ProjectID]bool
	artifacts    map[domain.Digest]bool
	candidates   map[domain.Digest]bool
	snapshots    map[domain.Digest]bool
}

func (a *App) gcRoots(ctx context.Context) (gcRoots, error) {
	roots := gcRoots{
		registered:   map[domain.ProjectID]bool{},
		liveProjects: map[domain.ProjectID]bool{},
		artifacts:    map[domain.Digest]bool{},
		candidates:   map[domain.Digest]bool{},
		snapshots:    map[domain.Digest]bool{},
	}

	// Live containers root their candidate and artifact. Metadata that
	// cannot be trusted fails the whole GC: a malformed label must
	// never demote a live reference to garbage. Stopped containers
	// count (they are restartable deployments), as do .prev/.next
	// leftovers of replacement transactions.
	live, err := a.deps.Docker.ListManagedContainers(ctx)
	if err != nil {
		return roots, err
	}
	liveCandidates := map[domain.Digest]bool{}
	for _, c := range live {
		// The dev-build container is managed but deliberately project-
		// and candidate-less: it references no store objects (its
		// mounts are throwaway host temp dirs), so it roots nothing
		// and must not trip the fail-closed label checks below.
		if c.Labels[runtime.RoleLabel] == dev.BuilderRole {
			continue
		}
		// The project label gates journal cleanup (gcProjectState), so
		// a container that cannot name its project validly fails the
		// GC the same way an unparsable candidate does — a malformed
		// ID would misfile the container and let its real project's
		// state look container-free.
		project, err := domain.ParseProjectID(c.Labels[runtime.ProjectLabel])
		if err != nil {
			return roots, fmt.Errorf("%w: managed container %s has a missing or malformed project label; refusing to collect through it",
				domain.ErrConflict, c.Name)
		}
		roots.liveProjects[project] = true
		cd, err := domain.ParseDigest(c.Labels[runtime.CandidateLabel])
		if err != nil {
			return roots, fmt.Errorf("%w: managed container %s has a missing or malformed candidate label; refusing to collect through it",
				domain.ErrConflict, c.Name)
		}
		roots.candidates[cd] = true
		liveCandidates[cd] = true
		if raw, ok := c.Labels[runtime.ArtifactLabel]; ok {
			ad, err := domain.ParseDigest(raw)
			if err != nil {
				return roots, fmt.Errorf("%w: managed container %s has a malformed artifact label; refusing to collect through it",
					domain.ErrConflict, c.Name)
			}
			roots.artifacts[ad] = true
		}
	}

	records, err := a.deps.Registry.List(ctx)
	if err != nil {
		return roots, err
	}
	for _, rec := range records {
		roots.registered[rec.ID] = true
		if !rec.Artifact.IsZero() {
			roots.artifacts[rec.Artifact] = true
		}
		if rec.Approved != nil {
			roots.candidates[*rec.Approved] = true
		}
		bs, err := a.brokerStore(rec.ID)
		if err != nil {
			return roots, err
		}
		pending, err := bs.ListPending()
		if err != nil {
			return roots, err
		}
		for _, p := range pending {
			roots.candidates[p.Candidate] = true
		}
	}
	artifacts, err := a.deps.Store.ListArtifactRecords()
	if err != nil {
		return roots, err
	}
	for _, rec := range artifacts { // newest-first
		if rec.Release.Source != "dev-build" {
			roots.artifacts[rec.Digest] = true
			break
		}
	}
	candRecords, err := a.deps.Store.ListCandidateRecords()
	if err != nil {
		return roots, err
	}
	for _, rec := range candRecords {
		if roots.candidates[rec.Digest] && !rec.Snapshot.IsZero() {
			roots.snapshots[rec.Snapshot] = true
		}
		delete(liveCandidates, rec.Digest)
	}
	// A candidate rooted by a live container whose record the listing
	// missed: its snapshot cannot be determined without the record, so
	// read it directly and fail closed when that is impossible —
	// registry/broker-rooted candidates stay lenient (nothing live
	// depends on their loadability), live-container roots do not.
	for d := range liveCandidates {
		rec, err := a.deps.Store.ReadCandidateRecord(d)
		if err != nil {
			return roots, fmt.Errorf("%w: candidate %s is referenced by a live container but its record is unreadable (%v); refusing to collect through it",
				domain.ErrConflict, d, err)
		}
		if !rec.Snapshot.IsZero() {
			roots.snapshots[rec.Snapshot] = true
		}
	}
	return roots, nil
}

// gcOrphanRecords drops record files whose object is gone (an
// interrupted removal); kept digests are never touched.
func (a *App) gcOrphanRecords(ctx context.Context, req GCRequest, roots gcRoots, result *GCResult) error {
	artifacts, err := a.deps.Store.ListArtifactRecords()
	if err != nil {
		return err
	}
	candidates, err := a.deps.Store.ListCandidateRecords()
	if err != nil {
		return err
	}
	type rec struct {
		kind   store.ObjectKind
		digest domain.Digest
		keep   bool
	}
	var all []rec
	for _, r := range artifacts {
		all = append(all, rec{store.ArtifactObject, r.Digest, roots.artifacts[r.Digest]})
	}
	for _, r := range candidates {
		all = append(all, rec{store.CandidateObject, r.Digest, roots.candidates[r.Digest]})
	}
	for _, r := range all {
		if r.keep {
			continue
		}
		exists, err := a.deps.Store.Exists(ctx, r.kind, r.digest)
		if err != nil || exists {
			continue // live objects were handled (or kept) above
		}
		entry := GCEntry{Kind: "record", ID: string(r.kind) + " " + r.digest.String(), Reason: "orphan record"}
		if !req.DryRun {
			if err := a.deps.Store.RemoveObject(ctx, r.kind, r.digest); err != nil {
				return err
			}
		}
		result.Removed = append(result.Removed, entry)
	}
	return nil
}

// gcBinaries removes stale bin/vibe-<hex12> copies left behind by the
// update/dev handoff. The current symlink target and every kept
// artifact's copy stay.
func (a *App) gcBinaries(req GCRequest, roots gcRoots, result *GCResult) error {
	keep := map[string]bool{}
	for d := range roots.artifacts {
		keep["vibe-"+d.Short()] = true
	}
	if target, err := os.Readlink(filepath.Join(a.deps.Layout.Bin, "vibe")); err == nil {
		keep[filepath.Base(target)] = true
	}
	entries, err := os.ReadDir(a.deps.Layout.Bin)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "vibe-") || keep[name] {
			continue
		}
		p := filepath.Join(a.deps.Layout.Bin, name)
		info, err := os.Lstat(p)
		if err != nil {
			continue
		}
		entry := GCEntry{Kind: "binary", ID: name, Bytes: info.Size()}
		if !req.DryRun {
			if err := os.Remove(p); err != nil {
				return err
			}
		}
		result.Removed = append(result.Removed, entry)
		result.TotalBytes += entry.Bytes
	}
	return nil
}

// gcProjectState removes per-project host state — broker directories,
// extension approvals, dev records — whose project is no longer
// registered.
func (a *App) gcProjectState(req GCRequest, roots gcRoots, result *GCResult) error {
	// Broker directories are named by project ID.
	brokerEntries, err := os.ReadDir(a.deps.Layout.Broker)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, e := range brokerEntries {
		if !e.IsDir() || roots.registered[domain.ProjectID(e.Name())] {
			continue
		}
		p := filepath.Join(a.deps.Layout.Broker, e.Name())
		entry := GCEntry{Kind: "broker", ID: e.Name(), Bytes: treeSize(p)}
		if !req.DryRun {
			if err := os.RemoveAll(p); err != nil {
				return err
			}
		}
		result.Removed = append(result.Removed, entry)
		result.TotalBytes += entry.Bytes
	}

	// Approval markers are "<project-id>-<64-hex-digest>".
	approvals := filepath.Join(a.deps.Layout.State, "approvals")
	appEntries, err := os.ReadDir(approvals)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, e := range appEntries {
		name := e.Name()
		if len(name) < 66 || name[len(name)-65] != '-' {
			continue
		}
		project := domain.ProjectID(name[:len(name)-65])
		if roots.registered[project] {
			continue
		}
		entry := GCEntry{Kind: "state", ID: "approval " + name}
		if !req.DryRun {
			if err := os.Remove(filepath.Join(approvals, name)); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		result.Removed = append(result.Removed, entry)
	}

	// Dev provenance records are "<project-id>.json".
	devDir := filepath.Join(a.deps.Layout.State, "dev")
	devEntries, err := os.ReadDir(devDir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, e := range devEntries {
		name, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || roots.registered[domain.ProjectID(name)] {
			continue
		}
		entry := GCEntry{Kind: "state", ID: "dev-record " + name}
		if !req.DryRun {
			if err := os.Remove(filepath.Join(devDir, e.Name())); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		result.Removed = append(result.Removed, entry)
	}

	// Replacement journals are "<project-id>.json" (temp debris is
	// ".<project-id>.json.tmp-*"). An unregistered project never runs
	// `up` again, so its files would otherwise persist forever — but a
	// journal is a transaction phase marker, so it is only removed when
	// the container enumeration (already done under this exclusive
	// lock) shows the project has no managed containers left; otherwise
	// it is reported, never collected through.
	replaceDir := filepath.Join(a.deps.Layout.State, "replace")
	repEntries, err := os.ReadDir(replaceDir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, e := range repEntries {
		project, ok := replaceFileProject(e.Name())
		if !ok || roots.registered[project] {
			continue
		}
		entry := GCEntry{Kind: "state", ID: "replace-journal " + e.Name()}
		if roots.liveProjects[project] {
			entry.Reason = "unregistered project still has managed containers"
			result.Skipped = append(result.Skipped, entry)
			continue
		}
		if !req.DryRun {
			if err := os.Remove(filepath.Join(replaceDir, e.Name())); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		result.Removed = append(result.Removed, entry)
	}
	return nil
}

// replaceFileProject extracts the project ID from a replacement-journal
// filename or its atomic-write temp debris.
func replaceFileProject(name string) (domain.ProjectID, bool) {
	if id, ok := strings.CutSuffix(name, ".json"); ok && !strings.HasPrefix(name, ".") {
		return domain.ProjectID(id), true
	}
	if rest, ok := strings.CutPrefix(name, "."); ok {
		if i := strings.Index(rest, ".json.tmp-"); i > 0 {
			return domain.ProjectID(rest[:i]), true
		}
	}
	return "", false
}

// treeSize sums regular-file sizes best-effort for reporting.
func treeSize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // best-effort size reporting
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

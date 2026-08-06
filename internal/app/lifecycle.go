package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"vibe/internal/builder"
	"vibe/internal/domain"
	"vibe/internal/envfile"
	"vibe/internal/lock"
	"vibe/internal/model"
	"vibe/internal/paths"
	"vibe/internal/registry"
	"vibe/internal/runtime"
	"vibe/internal/schema"
	"vibe/internal/snapshot"
	"vibe/internal/tmux"
)

// ConfigRequest compiles and prints the canonical plan for the project
// containing Dir. It is diagnostic: inputs are frozen exactly as for a
// candidate, but nothing is registered as approved.
type ConfigRequest struct {
	Dir string
}

type ConfigResult struct {
	Plan      model.Plan
	Canonical []byte
	Snapshot  snapshot.Result
	// BoM is the tools image bill of materials this manifest selects —
	// the same parts a rebuild reports progress on. Derived, so it
	// rides outside the canonical plan; nil without a tools image.
	BoM []builder.InstallPart
}

func (a *App) Config(ctx context.Context, req ConfigRequest) (ConfigResult, error) {
	fail := opFail[ConfigResult]("config", "")
	root, rec, err := a.resolveProject(ctx, req.Dir)
	if err != nil {
		return fail(err)
	}
	fail = opFail[ConfigResult]("config", rec.ID)
	frozen, err := a.freezeInputs(ctx, root, rec)
	if err != nil {
		return fail(err)
	}
	artifact, releaseArtifact, err := a.loadArtifact(ctx, rec)
	if err != nil {
		return fail(err)
	}
	defer releaseArtifact()
	brokerStore, err := a.brokerStore(rec.ID)
	if err != nil {
		return fail(err)
	}
	plan, ferrs := model.Compile(model.CompileInput{
		Project:          rec,
		Artifact:         artifact,
		Manifest:         frozen.Manifest,
		Snapshot:         frozen.Snapshot,
		BrokerResultsDir: brokerStore.ResultsDir(),
		DNSConf:          model.DNSConfPresent(artifact),
		// Image references stay unresolved in the diagnostic plan; up
		// resolves them to digests when it builds the real candidate.
	})
	if len(ferrs) > 0 {
		return fail(fieldErrs(ferrs))
	}
	canonical, err := model.CanonicalJSON(plan)
	if err != nil {
		return fail(err)
	}
	res := ConfigResult{Plan: plan, Canonical: canonical, Snapshot: frozen.Snapshot}
	if hasTools(frozen.Manifest) {
		if ip := builder.GenerateInstallPlan(frozen.Manifest.Image.Agents, frozen.Manifest.Image.Toolchains, rec.AgentRefresh != ""); ip != nil {
			res.BoM = ip.Parts
			for i := range res.BoM {
				if res.BoM[i].ID == "base" {
					res.BoM[i].Detail = frozen.Manifest.Image.Base
				}
			}
		}
	}
	return res, nil
}

// frozenInputs is the outcome of freezing a project's configuration
// inputs into an immutable snapshot: the published snapshot plus the
// manifest as loaded back from that snapshot. No later stage rereads
// workspace paths.
type frozenInputs struct {
	Manifest schema.Manifest
	Snapshot snapshot.Result
}

// Snapshot layout constants live in the model package
// (model.SnapshotManifestPath and friends) so runtime and app agree.

func (a *App) freezeInputs(ctx context.Context, root paths.Root, rec registry.Record) (frozenInputs, error) {
	// First pass over the workspace manifest only learns which inputs to
	// freeze; every validated read below comes from the snapshot.
	preview, err := loadManifestFile(filepath.Join(root.Path, paths.ManifestRelPath))
	if err != nil {
		return frozenInputs{}, err
	}

	spec := snapshot.Spec{
		ProjectRoot: root,
		Entries: []snapshot.Entry{
			{Source: paths.ManifestRelPath, Dest: model.SnapshotManifestPath},
		},
	}
	if ef := preview.Manifest.EnvFile; ef != "" {
		spec.Entries = append(spec.Entries, snapshot.Entry{Source: ef, Dest: model.SnapshotEnvFilePath})
	}
	if preview.Manifest.Image.Extension {
		spec.Entries = append(spec.Entries,
			snapshot.Entry{Source: ".vibe/Dockerfile", Dest: model.SnapshotDockerfilePath},
			snapshot.Entry{Source: ".vibe/build", Dest: model.SnapshotBuildDir, Optional: true})
	}
	for i, imp := range preview.Manifest.Runtime.Imports {
		spec.Entries = append(spec.Entries, snapshot.Entry{
			Source: imp.Source,
			Dest:   model.SnapshotImportDir + "/" + strconv.Itoa(i),
		})
	}
	snap, err := a.deps.Snapshots.Create(ctx, spec)
	if err != nil {
		return frozenInputs{}, err
	}

	doc, err := loadManifestFile(filepath.Join(snap.Path, model.SnapshotManifestPath))
	if err != nil {
		return frozenInputs{}, err
	}
	if ferrs := doc.Validate(); len(ferrs) > 0 {
		return frozenInputs{}, fieldErrs(ferrs)
	}

	// Parse the frozen env file now so a malformed one fails the freeze;
	// its entries are only ever loaded per exec, never carried further.
	if doc.Manifest.EnvFile != "" {
		f, err := os.Open(filepath.Join(snap.Path, model.SnapshotEnvFilePath))
		if err != nil {
			return frozenInputs{}, fmt.Errorf("open snapshotted env file: %w", err)
		}
		defer f.Close()
		if _, err := envfile.Parse(f, envfile.Limits{}); err != nil {
			return frozenInputs{}, fmt.Errorf("env file %s: %w", doc.Manifest.EnvFile, err)
		}
	}
	return frozenInputs{Manifest: doc.Manifest, Snapshot: snap}, nil
}

func loadManifestFile(path string) (*schema.Document, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", domain.ErrNotFound, path)
		}
		return nil, err
	}
	defer f.Close()
	doc, err := schema.Load(f, schema.Limits{})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return doc, nil
}

// UpRequest reconciles the project to a fresh candidate. Force (the
// rebuild verb) is also the agent-refresh boundary: every rebuild mints
// the token that re-pulls the unversioned agents, and nothing else does
// — a refreshed tools image changes the candidate and replaces the
// containers anyway, so a separate refresh knob on up would just be
// rebuild spelled twice.
type UpRequest struct {
	Dir   string
	Force bool // replace containers even when already in sync (rebuild)
	// Project, when set, resolves the directory from the registry
	// record instead of Dir — the tui's cold-project click dispatches
	// by ID (`vibe _up`), with no workspace cwd to offer. The resolved
	// root still passes the ordinary resolveProject checks.
	Project domain.ProjectID
}

type UpResult struct {
	Record    registry.Record
	Candidate domain.Digest
	State     runtime.State
}

func (a *App) Up(ctx context.Context, req UpRequest) (UpResult, error) {
	op := "up"
	if req.Force {
		op = "rebuild"
	}
	fail := opFail[UpResult](op, "")
	dir := req.Dir
	if req.Project != "" {
		byID, err := a.deps.Registry.Get(ctx, req.Project)
		if err != nil {
			return fail(err)
		}
		dir = byID.Root
	}
	root, rec, err := a.resolveProject(ctx, dir)
	if err != nil {
		return fail(err)
	}
	fail = opFail[UpResult](op, rec.ID)
	if err := a.deps.Docker.Ping(ctx); err != nil {
		return fail(err)
	}
	// Shared store-global hold across the whole approval flow: every
	// reference it mints — the snapshot freezeInputs publishes, the
	// candidate, the containers, the approved pointer — stays shielded
	// from an exclusive GC until Registry.Update roots it durably.
	// Republication preserves object mtimes, so GC's MinAge alone
	// cannot shield a re-referenced old object.
	sharedStore, err := a.deps.Locks.AcquireShared(ctx, lock.StoreGlobal())
	if err != nil {
		return fail(err)
	}
	defer sharedStore.Release()
	// A rebuild mints a new token before compiling so the tools image
	// tracks the channel-tracking (unversioned) agents; it is persisted
	// below (with Approved) only once the containers are actually up.
	// Every rebuild refreshes — "no version given" means "latest per
	// rebuild" — while a plain up keeps the record's existing token, so
	// idempotent ups stay warm-cached and off the network. The token is
	// the per-agent resolved-version fingerprint (mintAgentRefresh):
	// upstream unmoved since the last rebuild → identical token →
	// Docker's layer cache keeps the previous pull warm.
	refreshAgents := req.Force
	if refreshAgents {
		rec.AgentRefresh = a.mintAgentRefresh(ctx, root)
	}
	cand, err := a.prepareCandidate(ctx, root, rec)
	if err != nil {
		return fail(err)
	}
	state, err := a.runtime.Up(ctx, cand, runtime.UpOptions{
		Force:        req.Force,
		Progress:     a.deps.Progress,
		LifecycleOut: a.deps.LifecycleOut,
	})
	if err != nil {
		return fail(err)
	}
	// The durable candidate exists and its containers run; only now move
	// the approved-candidate pointer.
	updated, err := a.deps.Registry.Update(ctx, rec.ID, rec.Revision, func(r *registry.Record) error {
		r.Approved = &cand.Record.Digest
		if refreshAgents {
			r.AgentRefresh = rec.AgentRefresh
		}
		return nil
	})
	if err != nil {
		return fail(err)
	}
	state.Approved = cand.Record.Digest
	for i := range state.Containers {
		state.Containers[i].InSync = state.Containers[i].Candidate == cand.Record.Digest
	}
	a.bumpTuiSerial(ctx)
	return UpResult{Record: updated, Candidate: cand.Record.Digest, State: state}, nil
}

// agentProbeTimeout bounds ONE channel-version probe: a rebuild is
// interactive, and a black-holed endpoint must degrade to the
// timestamp fallback, never hang the build.
const agentProbeTimeout = 10 * time.Second

// mintAgentRefresh mints a rebuild's agent-refresh token: one
// kind=version pair per channel-tracking agent, each resolved by
// probing the channel's own release endpoint. An unchanged upstream
// yields the identical token, so the agent layers stay warm-cached
// instead of re-downloading identical builds; a real bump changes the
// pair and re-pulls exactly that agent's layer. Every failure mode —
// no prober wired, unreadable manifest, probe error or timeout, a
// value that would not survive the fingerprint grammar — degrades to a
// timestamp value, the pre-probe behavior that busts on every rebuild:
// staleness never hides behind a dead endpoint.
func (a *App) mintAgentRefresh(ctx context.Context, root paths.Root) string {
	stamp := a.deps.Clock.Now().UTC().Format(time.RFC3339Nano)
	doc, err := loadManifestFile(filepath.Join(root.Path, paths.ManifestRelPath))
	if err != nil {
		return stamp
	}
	var pairs []string
	for _, ag := range doc.Manifest.Image.Agents {
		channel := builder.AgentChannel(ag)
		if channel == "" {
			continue // exact pin: a plain cached layer, nothing to probe
		}
		value := stamp
		if a.deps.AgentVersions != nil {
			pctx, cancel := context.WithTimeout(ctx, agentProbeTimeout)
			v, err := a.deps.AgentVersions.Resolve(pctx, ag.Kind, channel)
			cancel()
			// The pair grammar splits on spaces and cuts on the first
			// '='; a prober is trusted code but the guard keeps a bad
			// impl a cache-bust, never a corrupt token.
			if err == nil && v != "" && !strings.ContainsAny(v, " =") {
				value = v
			}
		}
		pairs = append(pairs, string(ag.Kind)+"="+value)
	}
	if len(pairs) == 0 {
		return stamp
	}
	return strings.Join(pairs, " ")
}

// DownRequest tears down the project's containers and network. Volumes
// survive unless RemoveVolumes is set. DeferUIKill hands the
// UI-session teardown back to the caller as DownResult.KillUI instead
// of killing inline: the CLI sets it so the result renders BEFORE the
// kill — `vibe down` inside the project's own UI session used to HUP
// itself mid-output (branch-review remainder item 9).
type DownRequest struct {
	Dir           string
	RemoveVolumes bool
	DeferUIKill   bool
}

type DownResult struct {
	Record registry.Record
	// KillUI closes the project's UI session; non-nil only when the
	// request deferred it. Best-effort, exactly like the inline kill.
	KillUI func(context.Context)
}

func (a *App) Down(ctx context.Context, req DownRequest) (DownResult, error) {
	fail := opFail[DownResult]("down", "")
	_, rec, err := a.resolveProject(ctx, req.Dir)
	if err != nil {
		return fail(err)
	}
	fail = opFail[DownResult]("down", rec.ID)
	if err := a.deps.Docker.Ping(ctx); err != nil {
		return fail(err)
	}
	if err := a.runtime.Down(ctx, rec, runtime.DownOptions{RemoveVolumes: req.RemoveVolumes}); err != nil {
		return fail(err)
	}
	// The project's UI session fronts containers that no longer exist;
	// closing it beats leaving a hollow shell (and ends the palette's
	// park flow cleanly). Best-effort like the serial bump: a nil tmux,
	// a dead server, or no session never fails the down that did the
	// real work. Deferred, the same kill runs from the result after
	// the caller has flushed its output.
	killUI := func(ctx context.Context) {
		if a.deps.Tmux == nil {
			return
		}
		killCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		_ = a.deps.Tmux.KillSession(killCtx, tmux.SessionFor(rec.ID))
		cancel()
	}
	res := DownResult{Record: rec}
	if req.DeferUIKill {
		res.KillUI = killUI
	} else {
		killUI(ctx)
	}
	a.bumpTuiSerial(ctx)
	return res, nil
}

type StatusRequest struct {
	Dir string
}

type StatusResult struct {
	Record registry.Record
	State  runtime.State
}

func (a *App) Status(ctx context.Context, req StatusRequest) (StatusResult, error) {
	fail := opFail[StatusResult]("status", "")
	_, rec, err := a.resolveProject(ctx, req.Dir)
	if err != nil {
		return fail(err)
	}
	fail = opFail[StatusResult]("status", rec.ID)
	if err := a.deps.Docker.Ping(ctx); err != nil {
		return fail(err)
	}
	state, err := a.runtime.Status(ctx, rec)
	if err != nil {
		return fail(err)
	}
	return StatusResult{Record: rec, State: state}, nil
}

// resolveProject is the shared discover-then-resolve step.
func (a *App) resolveProject(ctx context.Context, dir string) (paths.Root, registry.Record, error) {
	root, err := paths.Discover(dir)
	if err != nil {
		return paths.Root{}, registry.Record{}, err
	}
	rec, err := a.deps.Registry.Resolve(ctx, root)
	if err != nil {
		return paths.Root{}, registry.Record{}, err
	}
	return root, rec, nil
}

// fieldErrs folds diagnostics into one error that keeps every line and
// still classifies as ErrInvalid.
func fieldErrs(errs []domain.FieldError) error {
	var buf bytes.Buffer
	for i, e := range errs {
		if i > 0 {
			buf.WriteString("\n")
		}
		buf.WriteString(e.Error())
	}
	return fmt.Errorf("%w: %s", domain.ErrInvalid, buf.String())
}

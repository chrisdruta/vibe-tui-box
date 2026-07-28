package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chrisdruta/vibe-tui-box/internal/broker"
	"github.com/chrisdruta/vibe-tui-box/internal/dockerapi"
	dockerfake "github.com/chrisdruta/vibe-tui-box/internal/dockerapi/fake"
	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/model"
	"github.com/chrisdruta/vibe-tui-box/internal/payload"
	"github.com/chrisdruta/vibe-tui-box/internal/registry"
	"github.com/chrisdruta/vibe-tui-box/internal/store"
	"github.com/chrisdruta/vibe-tui-box/internal/terminal"
)

// seedReleaseArtifact publishes a minimal non-dev-build artifact (object
// tree plus record) so dev off has a release to revert and hand back to.
func seedReleaseArtifact(t *testing.T, a *App, binary string) store.ArtifactRecord {
	t.Helper()
	staging, err := a.deps.Store.NewStaging("artifact")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(staging)
	binPath := filepath.Join(staging, store.ArtifactBinaryRelPath)
	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binPath, []byte(binary), 0o755); err != nil {
		t.Fatal(err)
	}
	digest, _, err := store.DigestTree(staging)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.deps.Store.Publish(context.Background(), store.ArtifactObject, staging, digest); err != nil {
		t.Fatal(err)
	}
	rec := store.ArtifactRecord{
		Digest:      digest,
		Version:     "v9.9.9",
		Release:     domain.ReleaseProvenance{Source: "release", Version: "v9.9.9"},
		InstalledAt: a.deps.Clock.Now().UTC(),
	}
	if err := a.deps.Store.WriteArtifactRecord(rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

func writeRequestFile(t *testing.T, dir, id, content string) {
	t.Helper()
	reqDir := filepath.Join(dir, broker.RequestsRelDir)
	if err := os.MkdirAll(reqDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reqDir, id+".json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBrokerRequestFlow(t *testing.T) {
	a, docker := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Up(ctx, UpRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}

	// The dev container carries the read-only results mount.
	creates := docker.CallsTo("CreateContainer")
	dev := creates[len(creates)-1].Request.(dockerapi.CreateRequest)
	found := false
	for _, m := range dev.Mounts {
		if m.Target == model.ResultsTarget && m.ReadOnly {
			found = true
		}
	}
	if !found {
		t.Fatalf("results mount missing: %+v", dev.Mounts)
	}

	// Agent drops a request; polling binds it to a candidate.
	writeRequestFile(t, dir, "req-1",
		`{"format":1,"id":"req-1","kind":"rebuild","reason":"need port","summary":"open 8080"}`)
	list, err := a.RequestList(ctx, RequestListRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Pending) != 1 || list.Pending[0].RequestID != "req-1" || list.Pending[0].Candidate.IsZero() {
		t.Fatalf("pending: %+v", list.Pending)
	}
	candidate := list.Pending[0].Candidate

	// Polling again does not rebind unchanged content.
	list2, err := a.RequestList(ctx, RequestListRequest{Dir: dir})
	if err != nil || len(list2.Pending) != 1 || list2.Pending[0].Candidate != candidate {
		t.Fatalf("re-poll changed binding: %+v, %v", list2.Pending, err)
	}

	// Show renders sanitized text. Unchanged inputs bind to the already
	// approved candidate, and the diff surface says so.
	show, err := a.RequestShow(ctx, RequestShowRequest{Dir: dir, ID: "req-1"})
	if err != nil || len(show.Summary.Lines) == 0 {
		t.Fatalf("show: %+v, %v", show, err)
	}
	if show.DiffLabel == "" || len(show.Diff.Lines) == 0 ||
		!strings.Contains(show.Diff.Lines[0], "already the approved") {
		t.Fatalf("show diff for unchanged candidate: label=%q diff=%q", show.DiffLabel, show.Diff.Lines)
	}

	// Reject writes a result the container can read and clears pending.
	if _, err := a.RequestDecide(ctx, RequestDecideRequest{
		Dir: dir, Candidate: candidate, Approve: false, Message: "not now",
	}); err != nil {
		t.Fatal(err)
	}
	bs, _ := a.brokerStore(mustResolve(t, a, dir).ID)
	results, _ := bs.ListResults()
	if len(results) != 1 || results[0].Status != broker.StatusRejected {
		t.Fatalf("results after reject: %+v", results)
	}
	// A decided request is not re-polled.
	list3, err := a.RequestList(ctx, RequestListRequest{Dir: dir})
	if err != nil || len(list3.Pending) != 0 {
		t.Fatalf("rejected request reappeared: %+v", list3.Pending)
	}

	// A new request with changed workspace content approves end-to-end.
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=v2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeRequestFile(t, dir, "req-2",
		`{"format":1,"id":"req-2","kind":"rebuild","reason":"env change","summary":"rotate secret"}`)
	list4, err := a.RequestList(ctx, RequestListRequest{Dir: dir})
	if err != nil || len(list4.Pending) != 1 {
		t.Fatalf("second request: %+v, %v", list4.Pending, err)
	}
	// A changed candidate shows a real plan diff (the snapshot digest
	// line moves), both in show and in the approval confirmation.
	show2, err := a.RequestShow(ctx, RequestShowRequest{Dir: dir, ID: "req-2"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(show2.DiffLabel, "plan diff (approved sha256:") {
		t.Fatalf("show2 diff label: %q", show2.DiffLabel)
	}
	var plus, minus bool
	for _, line := range show2.Diff.Lines {
		plus = plus || strings.HasPrefix(line, "+ ")
		minus = minus || strings.HasPrefix(line, "- ")
	}
	if !plus || !minus {
		t.Fatalf("show2 diff has no +/- lines: %q", show2.Diff.Lines)
	}
	capture := &capturingPrompt{approve: true}
	a.deps.Prompt = capture
	decide, err := a.RequestDecide(ctx, RequestDecideRequest{
		Dir: dir, Candidate: list4.Pending[0].Candidate, Approve: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if capture.last.DiffLabel == "" || len(capture.last.Diff.Lines) == 0 {
		t.Fatalf("approval confirmation carried no plan diff: %+v", capture.last)
	}
	if decide.Result.Status != broker.StatusApproved || decide.State == nil || !decide.State.Running() {
		t.Fatalf("approve result: %+v", decide)
	}
	status, err := a.Status(ctx, StatusRequest{Dir: dir})
	if err != nil || status.Record.Approved == nil || *status.Record.Approved != list4.Pending[0].Candidate {
		t.Fatalf("approved pointer not moved: %+v, %v", status.Record, err)
	}
}

// capturingPrompt records the last confirmation for assertion.
type capturingPrompt struct {
	approve  bool
	confirms int
	last     terminal.Confirmation
}

func (p *capturingPrompt) Confirm(_ context.Context, c terminal.Confirmation) (bool, error) {
	p.confirms++
	p.last = c
	return p.approve, nil
}

func mustResolve(t *testing.T, a *App, dir string) registry.Record {
	t.Helper()
	_, rec, err := a.resolveProject(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

const extensionManifest = `schema: 1
image:
  base: "mcr.microsoft.com/devcontainers/base:debian"
  agents: [claude]
  extension: true
agent:
  cmd: claude
  tmux: true
`

const extensionDockerfile = `ARG VIBE_BASE_IMAGE
FROM ${VIBE_BASE_IMAGE}
RUN apt-get update
`

func TestExtensionBuildFlow(t *testing.T) {
	a, docker := newTestApp(t)
	a.deps.Prompt = terminal.AutoApprove{Approve: true}
	ctx := context.Background()
	dir := newProject(t)
	writeManifest(t, dir, extensionManifest)
	if err := os.WriteFile(filepath.Join(dir, ".vibe", "Dockerfile"), []byte(extensionDockerfile), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}

	rec := mustResolve(t, a, dir)
	extRef := model.ExtensionImageRef(rec.ID)
	docker.BuildResult = dockerapi.BuiltImage{
		Ref:    dockerapi.ImageRef(extRef),
		Digest: domain.SHA256([]byte("built-extension")),
	}

	up, err := a.Up(ctx, UpRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !up.State.Running() {
		t.Fatalf("up state: %+v", up.State)
	}

	builds := docker.CallsTo("Build")
	if len(builds) != 2 {
		t.Fatalf("want 2 builds (tools, extension), got %d", len(builds))
	}
	toolsReq := builds[0].Request.(dockerapi.BuildRequest)
	toolsRef := model.ToolsImageRef(rec.ID)
	if toolsReq.Tag != toolsRef || toolsReq.Dockerfile != "Dockerfile" {
		t.Fatalf("tools build request: %+v", toolsReq)
	}
	if !strings.Contains(toolsReq.BuildArgs["VIBE_BASE_IMAGE"], "@sha256:") {
		t.Fatalf("tools base image not digest-pinned: %q", toolsReq.BuildArgs["VIBE_BASE_IMAGE"])
	}
	req := builds[1].Request.(dockerapi.BuildRequest)
	if req.Tag != extRef || req.Dockerfile != model.SnapshotDockerfilePath {
		t.Fatalf("build request: %+v", req)
	}
	// The extension layers on top of the built tools image, pinned to
	// its digest.
	base := req.BuildArgs["VIBE_BASE_IMAGE"]
	if !strings.HasPrefix(base, toolsRef+"@sha256:") || !strings.Contains(base, docker.BuildResult.Digest.Hex()) {
		t.Fatalf("extension base %q not the digest-pinned tools image", base)
	}
	// The restricted context contains the Dockerfile but never env-file
	// or the manifest.
	if _, err := os.Stat(filepath.Join(req.ContextDir, "env-file")); !os.IsNotExist(err) {
		t.Fatal("env file leaked into the build context")
	}

	// The dev container runs the built extension image.
	creates := docker.CallsTo("CreateContainer")
	dev := creates[len(creates)-1].Request.(dockerapi.CreateRequest)
	if !strings.Contains(dev.Image, docker.BuildResult.Digest.Hex()) {
		t.Fatalf("dev image %q not pinned to built digest", dev.Image)
	}

	// Second up with unchanged inputs: approval sticks, no new build
	// prompt needed (AutoApprove would hide it, but the marker path is
	// exercised).
	if _, err := a.Up(ctx, UpRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
}

// TestToolsBuildFlow: a manifest with agents but no extension builds
// exactly one image — the engine-generated tools image — with no
// approval prompt (the app has no Prompt wired: tools Dockerfiles are
// engine-authored, not project input).
func TestToolsBuildFlow(t *testing.T) {
	a, docker := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}

	rec := mustResolve(t, a, dir)
	toolsRef := model.ToolsImageRef(rec.ID)
	docker.BuildResult = dockerapi.BuiltImage{
		Ref:    dockerapi.ImageRef(toolsRef),
		Digest: domain.SHA256([]byte("built-tools")),
	}

	up, err := a.Up(ctx, UpRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !up.State.Running() {
		t.Fatalf("up state: %+v", up.State)
	}

	builds := docker.CallsTo("Build")
	if len(builds) != 1 {
		t.Fatalf("want 1 build, got %d", len(builds))
	}
	req := builds[0].Request.(dockerapi.BuildRequest)
	if req.Tag != toolsRef || req.Dockerfile != "Dockerfile" {
		t.Fatalf("build request: %+v", req)
	}
	if !strings.Contains(req.BuildArgs["VIBE_BASE_IMAGE"], "@sha256:") {
		t.Fatalf("base image not digest-pinned: %q", req.BuildArgs["VIBE_BASE_IMAGE"])
	}

	// The dev container runs the tools image pinned to the built digest.
	creates := docker.CallsTo("CreateContainer")
	dev := creates[len(creates)-1].Request.(dockerapi.CreateRequest)
	if !strings.HasPrefix(dev.Image, toolsRef+"@sha256:") || !strings.Contains(dev.Image, docker.BuildResult.Digest.Hex()) {
		t.Fatalf("dev image %q not the digest-pinned tools image", dev.Image)
	}
}

func TestExtensionRejectedWithoutApproval(t *testing.T) {
	a, _ := newTestApp(t)
	a.deps.Prompt = terminal.AutoApprove{Approve: false}
	ctx := context.Background()
	dir := newProject(t)
	writeManifest(t, dir, extensionManifest)
	if err := os.WriteFile(filepath.Join(dir, ".vibe", "Dockerfile"), []byte(extensionDockerfile), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Up(ctx, UpRequest{Dir: dir}); !errors.Is(err, domain.ErrCanceled) {
		t.Fatalf("unapproved extension should cancel, got %v", err)
	}
}

// newEngineRepo fabricates a minimal engine checkout: module file,
// payload tree with manifest, and a project manifest for discovery.
func newEngineRepo(t *testing.T) string {
	t.Helper()
	dir := newProject(t)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module "+EngineModule+"\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Reuse the preset-payload fixture layout on disk.
	script := "#!/bin/sh\nexec sleep infinity\n"
	files := map[string]string{"payload/container/entrypoint.sh": script}
	for p, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	manifest, err := payloadManifestFor(map[string]string{"container/entrypoint.sh": script})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "payload", "manifest.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(dir, "cmd", "vibe"), 0o755)
	os.WriteFile(filepath.Join(dir, "cmd", "vibe", "main.go"), []byte("package main\n"), 0o644)
	return dir
}

func payloadManifestFor(files map[string]string) ([]byte, error) {
	var entries []payload.File
	for p, content := range files {
		mode := "0644"
		if strings.HasSuffix(p, ".sh") {
			mode = "0755"
		}
		entries = append(entries, payload.File{
			Path: p, Mode: mode, Size: int64(len(content)), Digest: domain.SHA256([]byte(content)),
		})
	}
	return payload.EncodeManifest(entries)
}

func TestDevModeFlow(t *testing.T) {
	a, docker := newTestApp(t)
	ctx := context.Background()
	dir := newEngineRepo(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}

	// The fake "builder container" writes the compiled binary into the
	// /out bind mount when created.
	docker.CreateHook = func(req dockerapi.CreateRequest) {
		if req.Labels["dev.vibe.role"] != "dev-builder" {
			return
		}
		for _, m := range req.Mounts {
			if m.Target == "/out" {
				os.WriteFile(filepath.Join(m.Source, "vibe"), []byte("DEV-BINARY"), 0o755)
			}
			if m.Target == "/src" && m.ReadOnly == false {
				panic("source mount must be read-only")
			}
		}
	}

	// Entering dev mode is the source-trust ceremony: refused without a
	// prompt, canceled on decline, confirmed exactly once — a later sync
	// of an already-dev project repeats the decision quietly.
	if _, err := a.DevOn(ctx, DevOnRequest{Dir: dir}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("dev on without a prompt should conflict, got %v", err)
	}
	a.deps.Prompt = terminal.AutoApprove{Approve: false}
	if _, err := a.DevOn(ctx, DevOnRequest{Dir: dir}); !errors.Is(err, domain.ErrCanceled) {
		t.Fatalf("declined dev on should cancel, got %v", err)
	}
	entry := &capturingPrompt{approve: true}
	a.deps.Prompt = entry

	on, err := a.DevOn(ctx, DevOnRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if entry.confirms != 1 || !strings.Contains(entry.last.Title, "dev mode") {
		t.Fatalf("entering dev mode should confirm once: %d %+v", entry.confirms, entry.last)
	}
	if on.Project.Mode != registry.ModeDev || on.Project.Artifact != on.Artifact.Digest {
		t.Fatalf("dev on project: %+v", on.Project)
	}
	if on.Record.Output != domain.SHA256([]byte("DEV-BINARY")) || on.Record.Builder.IsZero() || on.Record.Source.IsZero() {
		t.Fatalf("dev record provenance incomplete: %+v", on.Record)
	}
	if !strings.HasPrefix(on.Artifact.Version, "dev-") || on.Artifact.Release.Source != "dev-build" {
		t.Fatalf("dev artifact record: %+v", on.Artifact)
	}
	// The shim handoff: `vibe` now points at the fresh dev binary.
	if on.BinaryPath == "" {
		t.Fatal("dev on did not hand off the binary symlink")
	}
	if data, err := os.ReadFile(on.BinaryPath); err != nil || string(data) != "DEV-BINARY" {
		t.Fatalf("vibe symlink content %q, %v", data, err)
	}

	status, err := a.DevStatus(ctx, DevStatusRequest{Dir: dir})
	if err != nil || status.Record == nil || status.Record.Output != on.Record.Output {
		t.Fatalf("dev status: %+v, %v", status, err)
	}

	// A sync of an already-dev project repeats the trusted decision
	// without re-prompting.
	if _, err := a.DevOn(ctx, DevOnRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if entry.confirms != 1 {
		t.Fatalf("dev sync re-prompted: %d confirms", entry.confirms)
	}

	// A real release artifact exists to hand back to: dev off reverts the
	// pin to it and repoints the shim at its binary.
	release := seedReleaseArtifact(t, a, "RELEASE-BINARY")
	off, err := a.DevOff(ctx, DevOffRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if off.Project.Mode != registry.ModeRelease {
		t.Fatalf("dev off project: %+v", off.Project)
	}
	if off.Project.Artifact == on.Artifact.Digest {
		t.Fatal("dev off left the dev artifact pinned")
	}
	if off.Project.Artifact != release.Digest {
		t.Fatalf("dev off did not repin to the release: %+v", off.Project)
	}
	// The reordered handoff ran before the flip: the shim points at the
	// release binary and the DevOffResult reports it.
	if off.BinaryPath == "" {
		t.Fatal("dev off did not hand the binary back")
	}
	if data, err := os.ReadFile(off.BinaryPath); err != nil || string(data) != "RELEASE-BINARY" {
		t.Fatalf("vibe symlink after dev off %q, %v", data, err)
	}
	// The stale dev record is gone.
	if _, err := os.Stat(a.devRecordPath(mustResolve(t, a, dir).ID)); !os.IsNotExist(err) {
		t.Fatalf("dev record survived dev off: %v", err)
	}
	// A second project stays untouched throughout.
	other := newProject(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: other}); err != nil {
		t.Fatal(err)
	}
	otherRec := mustResolve(t, a, other)
	if otherRec.Mode != registry.ModeRelease {
		t.Fatal("dev mode leaked to another project")
	}
}

// TestDevOffHandoffFailureKeepsDevMode pins the ordering fix: when the
// binary handoff fails, dev off must not have moved the registry record
// — the pointer moves only after the durable binary is in place.
func TestDevOffHandoffFailureKeepsDevMode(t *testing.T) {
	a, docker := newTestApp(t)
	ctx := context.Background()
	dir := newEngineRepo(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	docker.CreateHook = func(req dockerapi.CreateRequest) {
		if req.Labels["dev.vibe.role"] != "dev-builder" {
			return
		}
		for _, m := range req.Mounts {
			if m.Target == "/out" {
				os.WriteFile(filepath.Join(m.Source, "vibe"), []byte("DEV-BINARY"), 0o755)
			}
		}
	}
	a.deps.Prompt = terminal.AutoApprove{Approve: true}
	on, err := a.DevOn(ctx, DevOnRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}

	// A release exists to revert to, but a colliding non-symlink file with
	// different content sits at its install target: installBinary reports
	// a conflict rather than clobbering it, so the handoff fails.
	release := seedReleaseArtifact(t, a, "RELEASE-BINARY")
	target := filepath.Join(a.deps.Layout.Bin, "vibe-"+release.Digest.Hex()[:12])
	if err := os.MkdirAll(a.deps.Layout.Bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("STALE-COLLISION"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := a.DevOff(ctx, DevOffRequest{Dir: dir}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("dev off handoff should fail with conflict, got %v", err)
	}
	// The flip did not happen: the project is still in dev mode, still
	// pinned to the dev artifact.
	rec := mustResolve(t, a, dir)
	if rec.Mode != registry.ModeDev {
		t.Fatalf("failed handoff moved the record out of dev mode: %+v", rec)
	}
	if rec.Artifact != on.Artifact.Digest {
		t.Fatalf("failed handoff moved the artifact pin: %+v", rec)
	}
}

func TestRenderersProduceProtocolLines(t *testing.T) {
	a, docker := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Up(ctx, UpRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	rec := mustResolve(t, a, dir)

	// _state is display form (glyph, ▲n only when pending); the running
	// project with no pending requests renders the bare glyph.
	state, err := a.RenderState(ctx, RenderRequest{Project: rec.ID})
	if err != nil || len(state.Lines) != 1 || state.Lines[0] != "●" {
		t.Fatalf("state render: %+v, %v", state, err)
	}
	// _sidebar is display form too: the one compact meta segment line
	// — a nominal container is its bare role (no glyph: ● belongs to
	// agents on the sidebar surface).
	sidebar, err := a.RenderSidebar(ctx, RenderRequest{Project: rec.ID, Width: 40})
	if err != nil || len(sidebar.Lines) != 1 || sidebar.Lines[0] != "dev" {
		t.Fatalf("sidebar render: %+v, %v", sidebar, err)
	}
	// _fleet porcelain: US-separated, version 1, project ID as join key.
	fleet, err := a.RenderFleet(ctx, RenderRequest{Width: 80})
	if err != nil || len(fleet.Lines) != 1 {
		t.Fatalf("fleet render: %+v, %v", fleet, err)
	}
	fields := strings.Split(fleet.Lines[0], "\x1f")
	if len(fields) != 7 || fields[0] != "1" || fields[1] != string(rec.ID) {
		t.Fatalf("fleet porcelain fields: %q", fields)
	}
	// _agents porcelain: the container-side `vibe ps` join, one line per
	// agent session, keyed to its project. The feeder's optional cli and
	// model columns become their own fields; a row whose session name
	// could not survive a tmux target or a mouse range is dropped.
	docker.ExecOutputs[dockerfake.ExecKey([]string{"bash", model.PayloadAgentPS})] =
		"agent|working|0|claude - opus|claude|opus\nagent-ghost|idle|0||\nbad name|idle|0||\n"
	agents, err := a.RenderAgents(ctx, RenderRequest{Width: 80})
	if err != nil || len(agents.Lines) != 2 {
		t.Fatalf("agents render: %+v, %v", agents, err)
	}
	fields = strings.Split(agents.Lines[0], "\x1f")
	if len(fields) != 6 || fields[0] != "1" || fields[1] != string(rec.ID) ||
		fields[2] != "agent" || fields[3] != "working" || fields[4] != "claude" || fields[5] != "opus" {
		t.Fatalf("agents porcelain fields: %q", fields)
	}
	// Scoped to one project, and empty for a project that has none.
	scoped, err := a.RenderAgents(ctx, RenderRequest{Project: "no-such-project"})
	if err != nil || len(scoped.Lines) != 0 {
		t.Fatalf("scoped agents render: %+v, %v", scoped, err)
	}
}

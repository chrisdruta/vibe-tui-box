// Package dev builds the engine from a source checkout inside a
// recorded builder container and installs the result as a dev-mode
// artifact for one project. Release-mode projects are never affected:
// only the requesting project's record changes.
package dev

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"vibe/internal/dockerapi"
	"vibe/internal/domain"
	"vibe/internal/paths"
	"vibe/internal/payload"
	"vibe/internal/snapshot"
	"vibe/internal/store"
	"vibe/internal/terminal"
)

// BuilderImage is the pinned-by-resolution build environment.
const BuilderImage = "golang:1.26"

// BuilderRole is the dev.vibe.role value on the throwaway build
// container. It is managed but deliberately project- and
// candidate-less, so GC recognizes it by this role instead of failing
// closed on its missing labels (a hard kill mid-build can leave it
// behind past the best-effort removal).
const BuilderRole = "dev-builder"

// SourcesFile is the tracked allowlist in the engine repository.
const SourcesFile = "build/dev-sources.txt"

// defaultSources covers the engine layout when the allowlist file is
// absent (older checkouts).
var defaultSources = []string{
	"go.mod", "go.sum", "assets.go", "cmd", "internal", "payload",
}

// Record is the persistent dev-build provenance for one project.
type Record struct {
	Format    int              `json:"format"`
	ProjectID domain.ProjectID `json:"project_id"`
	Source    domain.Digest    `json:"source"`
	Builder   domain.Digest    `json:"builder"`
	Output    domain.Digest    `json:"output"`
	Artifact  domain.Digest    `json:"artifact"`
	Target    domain.Platform  `json:"target"`
	BuiltAt   time.Time        `json:"built_at"`
}

const recordFormat = 1

// DecodeRecord strictly decodes a persistent dev provenance record —
// the record discipline (DisallowUnknownFields, format dispatch)
// applied where a bare Unmarshal used to silently accept anything.
func DecodeRecord(data []byte) (Record, error) {
	var rec Record
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rec); err != nil {
		return Record{}, fmt.Errorf("%w: dev record: %v", domain.ErrInvalid, err)
	}
	if rec.Format != recordFormat {
		return Record{}, fmt.Errorf("%w: dev record format %d, this engine speaks %d", domain.ErrInvalid, rec.Format, recordFormat)
	}
	return rec, nil
}

// Service performs dev builds.
type Service struct {
	snapshots *snapshot.Service
	store     *store.Store
	docker    dockerapi.Client
	platform  domain.Platform
	now       func() time.Time
}

func NewService(snapshots *snapshot.Service, st *store.Store, docker dockerapi.Client, platform domain.Platform, now func() time.Time) (*Service, error) {
	if snapshots == nil || st == nil || docker == nil || now == nil {
		return nil, fmt.Errorf("%w: dev service requires snapshots, store, docker, and a clock", domain.ErrInvalid)
	}
	return &Service{snapshots: snapshots, store: st, docker: docker, platform: platform, now: now}, nil
}

// ReadSources parses the allowlist file from the source root, falling
// back to the built-in default set.
func ReadSources(sourceRoot string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(sourceRoot, filepath.FromSlash(SourcesFile)))
	if err != nil {
		if os.IsNotExist(err) {
			return defaultSources, nil
		}
		return nil, err
	}
	var entries []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		entries = append(entries, line)
	}
	if len(entries) == 0 {
		return defaultSources, nil
	}
	return entries, scanner.Err()
}

// VerifyEngineRepo confirms sourceRoot is an engine checkout by its
// module path.
func VerifyEngineRepo(sourceRoot, modulePath string) error {
	data, err := os.ReadFile(filepath.Join(sourceRoot, "go.mod"))
	if err != nil {
		return fmt.Errorf("%w: %s is not a Go module checkout", domain.ErrInvalid, sourceRoot)
	}
	if !strings.Contains(string(data), "module "+modulePath) {
		return fmt.Errorf("%w: %s is not the engine repository (%s)", domain.ErrInvalid, sourceRoot, modulePath)
	}
	return nil
}

// Build snapshots the allowlisted sources, compiles them in the builder
// container, assembles an artifact (binary + the source's payload
// tree), and publishes it. The returned record carries full provenance.
func (s *Service) Build(ctx context.Context, project domain.ProjectID, sourceRoot paths.Root, sink dockerapi.ProgressSink) (Record, store.Artifact, error) {
	sources, err := ReadSources(sourceRoot.Path)
	if err != nil {
		return Record{}, store.Artifact{}, err
	}
	entries := make([]snapshot.Entry, 0, len(sources))
	for _, src := range sources {
		entries = append(entries, snapshot.Entry{Source: src, Dest: src, Optional: true})
	}
	snap, err := s.snapshots.Create(ctx, snapshot.Spec{
		ProjectRoot: sourceRoot,
		Entries:     entries,
		Limits:      snapshot.Limits{MaxFiles: 20000, MaxTotalBytes: 512 << 20},
	})
	if err != nil {
		return Record{}, store.Artifact{}, fmt.Errorf("snapshot sources: %w", err)
	}

	builderImage, err := s.docker.ResolveImage(ctx, dockerapi.ImageRef(BuilderImage))
	if err != nil {
		return Record{}, store.Artifact{}, err
	}

	outDir, err := s.store.NewStaging("dev-out")
	if err != nil {
		return Record{}, store.Artifact{}, err
	}
	defer os.RemoveAll(outDir)
	if err := s.runBuild(ctx, builderImage, snap.Path, outDir, sink, "dev-src-"+snap.Digest.Short()); err != nil {
		return Record{}, store.Artifact{}, err
	}

	binary, err := os.ReadFile(filepath.Join(outDir, "vibe"))
	if err != nil {
		return Record{}, store.Artifact{}, fmt.Errorf("dev build produced no binary: %w", err)
	}
	outputDigest := domain.SHA256(binary)

	// Assemble the artifact: built binary plus the source's payload tree
	// (the same content the binary embeds).
	artifactStaging, err := s.store.NewStaging("dev-artifact")
	if err != nil {
		return Record{}, store.Artifact{}, err
	}
	defer os.RemoveAll(artifactStaging)
	binPath := filepath.Join(artifactStaging, store.ArtifactBinaryRelPath)
	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		return Record{}, store.Artifact{}, err
	}
	if err := os.WriteFile(binPath, binary, 0o755); err != nil {
		return Record{}, store.Artifact{}, err
	}
	bundle, err := payload.New(os.DirFS(filepath.Join(snap.Path, "payload")))
	if err != nil {
		return Record{}, store.Artifact{}, fmt.Errorf("source payload: %w", err)
	}
	payloadDigest, err := bundle.Extract(ctx, filepath.Join(artifactStaging, store.ArtifactPayloadRelPath))
	if err != nil {
		return Record{}, store.Artifact{}, err
	}

	digest, _, err := store.DigestTree(artifactStaging)
	if err != nil {
		return Record{}, store.Artifact{}, err
	}
	obj, err := s.store.Publish(ctx, store.ArtifactObject, artifactStaging, digest)
	if err != nil {
		return Record{}, store.Artifact{}, err
	}
	version := "dev-" + outputDigest.Short()
	// An unchanged source tree republishes the identical artifact; reuse
	// its record instead of writing one that differs only by timestamp
	// (records are immutable — a divergent rewrite is a conflict).
	artifactRecord, err := s.store.ReadArtifactRecord(digest)
	if err != nil {
		artifactRecord = store.ArtifactRecord{
			Digest:        digest,
			Version:       version,
			Platform:      s.platform,
			BinaryDigest:  outputDigest,
			PayloadDigest: payloadDigest,
			Release: domain.ReleaseProvenance{
				Source:  "dev-build",
				Version: version,
			},
			InstalledAt: s.now().UTC(),
		}
		if err := s.store.WriteArtifactRecord(artifactRecord); err != nil {
			return Record{}, store.Artifact{}, err
		}
	}
	rec := Record{
		Format:    recordFormat,
		ProjectID: project,
		Source:    snap.Digest,
		Builder:   builderImage.Digest,
		Output:    outputDigest,
		Artifact:  digest,
		Target:    s.platform,
		BuiltAt:   artifactRecord.InstalledAt, // original install time when reused
	}
	return rec, store.Artifact{Record: artifactRecord, Path: obj.Path}, nil
}

// versionPkg is the ldflags -X target carrying release metadata.
const versionPkg = "vibe/internal/version"

// runBuild compiles inside the builder container: read-only source
// snapshot, writable output staging, default network for module
// downloads. version is stamped into the binary — the snapshot has no
// .git, so without it every dev build reports the indistinguishable
// "devel"; the source-digest stamp both identifies the build and pairs
// it with `vibe dev status` provenance.
func (s *Service) runBuild(ctx context.Context, builder dockerapi.ResolvedImage, srcDir, outDir string, sink dockerapi.ProgressSink, version string) error {
	name := dockerapi.ContainerName(fmt.Sprintf("vibe-devbuild-%s", domain.SHA256([]byte(srcDir + outDir)).Hex()[:10]))
	status := "compiling engine in " + string(builder.Ref)
	sink.Emit(dockerapi.Progress{Stage: "dev-build", Message: status})
	// The status line above stays open until a Done or Failed event
	// terminates it; every exit from this function must send one, or
	// the next print lands on the same row.
	ok := false
	defer func() {
		if !ok {
			sink.Emit(dockerapi.Progress{Stage: "dev-build", Message: status, Failed: true})
		}
	}()
	id, err := s.docker.CreateContainer(ctx, dockerapi.CreateRequest{
		Name:  name,
		Image: string(builder.Ref),
		// The snapshot and staging mounts are 0700 host-user-owned; with
		// all capabilities dropped root has no CAP_DAC_OVERRIDE, so the
		// build must run as the invoking user to reach them.
		User: fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		// Argv only — the invariant names builders explicitly, and the
		// sh -c wrapper (a 2026 regression for log redirection) is
		// replaced by reading the container log on failure.
		Command: []string{"go", "build", "-trimpath",
			"-ldflags", "-X " + versionPkg + ".release=" + version,
			"-o", "/out/vibe", "./cmd/vibe"},
		// Compiler output is bounded at the daemon so a pathological
		// build can't grow the host's log unbounded.
		Log:     dockerapi.LogPolicy{MaxSize: "10m", MaxFiles: 1},
		Workdir: "/src",
		Env: []string{
			"CGO_ENABLED=0",
			"HOME=/tmp",
			"GOCACHE=/tmp/gocache",
			"GOMODCACHE=/tmp/gomodcache",
			"GOOS=" + s.platform.OS,
			"GOARCH=" + s.platform.Arch,
		},
		Mounts: []dockerapi.Mount{
			{Kind: dockerapi.BindMount, Source: srcDir, Target: "/src", ReadOnly: true},
			{Kind: dockerapi.BindMount, Source: outDir, Target: "/out"},
		},
		Network: dockerapi.DefaultNetwork,
		Labels:  map[string]string{"dev.vibe.managed": "true", "dev.vibe.role": BuilderRole},
		Policy:  dockerapi.Policy{DropAllCapabilities: true, NoNewPrivileges: true},
	})
	if err != nil {
		return err
	}
	defer func() {
		// Best-effort cleanup of the throwaway builder container.
		_ = s.docker.RemoveContainer(context.WithoutCancel(ctx), id, dockerapi.RemoveOptions{Force: true})
	}()
	if err := s.docker.StartContainer(ctx, id); err != nil {
		return err
	}
	code, err := s.docker.WaitContainer(ctx, id)
	if err != nil {
		return err
	}
	if code != 0 {
		// Compiler output lives in the (daemon-bounded) container log;
		// it is container-produced, so it is bounded again here and
		// encoded before it can reach a terminal (same rule as the dns
		// crash detail).
		tail := &boundedBuffer{max: 64 << 10}
		lerr := s.docker.Logs(context.WithoutCancel(ctx), dockerapi.LogsRequest{
			Container: name,
			Tail:      30,
			Streams:   dockerapi.Streams{Out: tail, Err: tail},
		})
		if lerr == nil && tail.buf.Len() > 0 {
			enc := terminal.Encode(strings.TrimSpace(tailLines(tail.buf.String(), 30)),
				terminal.Limits{MaxWidth: 200, MaxLines: 30})
			return fmt.Errorf("%w: dev build failed with exit code %d:\n%s",
				domain.ErrInvalid, code, strings.Join(enc.Lines, "\n"))
		}
		return fmt.Errorf("%w: dev build failed with exit code %d", domain.ErrInvalid, code)
	}
	ok = true
	sink.Emit(dockerapi.Progress{Stage: "dev-build", Message: status, Done: true})
	return nil
}

// boundedBuffer keeps the first max bytes and drops the rest; the log
// drain must never grow or fail an error path.
type boundedBuffer struct {
	buf bytes.Buffer
	max int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if room := b.max - b.buf.Len(); room > 0 {
		if len(p) > room {
			p = p[:room]
		}
		b.buf.Write(p)
	}
	return n, nil
}

// tailLines returns at most the last n lines of s.
func tailLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

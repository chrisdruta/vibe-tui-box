// Package dev builds the engine from a source checkout inside a
// recorded builder container and installs the result as a dev-mode
// artifact for one project. Release-mode projects are never affected:
// only the requesting project's record changes.
package dev

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chrisdruta/vibe-tui-box/internal/dockerapi"
	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/paths"
	"github.com/chrisdruta/vibe-tui-box/internal/payload"
	"github.com/chrisdruta/vibe-tui-box/internal/snapshot"
	"github.com/chrisdruta/vibe-tui-box/internal/store"
)

// BuilderImage is the pinned-by-resolution build environment.
const BuilderImage = "golang:1.26"

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

// Service performs dev builds.
type Service struct {
	snapshots *snapshot.Service
	store     *store.Store
	docker    dockerapi.Client
	platform  domain.Platform
}

func NewService(snapshots *snapshot.Service, st *store.Store, docker dockerapi.Client, platform domain.Platform) (*Service, error) {
	if snapshots == nil || st == nil || docker == nil {
		return nil, fmt.Errorf("%w: dev service requires snapshots, store, and docker", domain.ErrInvalid)
	}
	return &Service{snapshots: snapshots, store: st, docker: docker, platform: platform}, nil
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
	if err := s.runBuild(ctx, builderImage, snap.Path, outDir, sink); err != nil {
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
	version := "dev-" + outputDigest.Hex()[:12]
	artifactRecord := store.ArtifactRecord{
		Digest:        digest,
		Version:       version,
		Platform:      s.platform,
		BinaryDigest:  outputDigest,
		PayloadDigest: payloadDigest,
		Release: domain.ReleaseProvenance{
			Source:  "dev-build",
			Version: version,
		},
		InstalledAt: time.Now().UTC(),
	}
	if err := s.store.WriteArtifactRecord(artifactRecord); err != nil {
		return Record{}, store.Artifact{}, err
	}
	rec := Record{
		Format:    recordFormat,
		ProjectID: project,
		Source:    snap.Digest,
		Builder:   builderImage.Digest,
		Output:    outputDigest,
		Artifact:  digest,
		Target:    s.platform,
		BuiltAt:   artifactRecord.InstalledAt,
	}
	return rec, store.Artifact{Record: artifactRecord, Path: obj.Path}, nil
}

// runBuild compiles inside the builder container: read-only source
// snapshot, writable output staging, default network for module
// downloads.
func (s *Service) runBuild(ctx context.Context, builder dockerapi.ResolvedImage, srcDir, outDir string, sink dockerapi.ProgressSink) error {
	name := dockerapi.ContainerName(fmt.Sprintf("vibe-devbuild-%s", domain.SHA256([]byte(srcDir + outDir)).Hex()[:10]))
	sink.Emit(dockerapi.Progress{Stage: "dev-build", Message: "compiling engine in " + string(builder.Ref)})
	id, err := s.docker.CreateContainer(ctx, dockerapi.CreateRequest{
		Name:    name,
		Image:   string(builder.Ref),
		Command: []string{"go", "build", "-trimpath", "-o", "/out/vibe", "./cmd/vibe"},
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
		Labels:  map[string]string{"dev.vibe.managed": "true", "dev.vibe.role": "dev-builder"},
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
		return fmt.Errorf("%w: dev build failed with exit code %d", domain.ErrInvalid, code)
	}
	return nil
}

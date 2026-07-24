package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	dockerfake "github.com/chrisdruta/vibe-tui-box/internal/dockerapi/fake"
	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/lock"
	"github.com/chrisdruta/vibe-tui-box/internal/model"
	"github.com/chrisdruta/vibe-tui-box/internal/paths"
	"github.com/chrisdruta/vibe-tui-box/internal/registry"
	"github.com/chrisdruta/vibe-tui-box/internal/runner"
	"github.com/chrisdruta/vibe-tui-box/internal/snapshot"
	"github.com/chrisdruta/vibe-tui-box/internal/store"
	"github.com/chrisdruta/vibe-tui-box/internal/version"
)

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC) }

func newTestApp(t *testing.T) (*App, *dockerfake.Client) {
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
	reg, err := registry.New(layout.Projects, locks, fixedClock{})
	if err != nil {
		t.Fatal(err)
	}
	snaps, err := snapshot.NewService(st)
	if err != nil {
		t.Fatal(err)
	}
	docker := dockerfake.New()
	a, err := New(Dependencies{
		Clock:     fixedClock{},
		Layout:    layout,
		Locks:     locks,
		Store:     st,
		Registry:  reg,
		Snapshots: snaps,
		Docker:    docker,
		Runner:    runner.NewHost(),
		Version:   version.Info{Release: "test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return a, docker
}

const testManifest = `schema: 1
harness: v2.0.0
image:
  base: "mcr.microsoft.com/devcontainers/base:debian"
  agents: [claude]
runtime:
  env: {FLAG: "1"}
agent:
  cmd: claude
  tmux: true
env_file: .env
`

func newProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".vibe"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, paths.ManifestRelPath), []byte(testManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRegisterConfigForget(t *testing.T) {
	a, _ := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)

	// Config before registration: exit-code class "not registered".
	if _, err := a.Config(ctx, ConfigRequest{Dir: dir}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unregistered config should be not-found, got %v", err)
	}

	reg, err := a.Register(ctx, RegisterRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if reg.Record.DisplayName == "" || reg.Record.ID == "" {
		t.Fatalf("register result incomplete: %+v", reg.Record)
	}

	// Re-registration conflicts.
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate register should conflict, got %v", err)
	}

	cfg, err := a.Config(ctx, ConfigRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Plan.Project.ID != reg.Record.ID {
		t.Fatal("plan project mismatch")
	}
	if cfg.Plan.CanonicalHash.IsZero() {
		t.Fatal("canonical hash missing")
	}
	var decoded model.Plan
	if err := json.Unmarshal(cfg.Canonical, &decoded); err != nil {
		t.Fatalf("canonical output is not JSON: %v", err)
	}
	// The env file's values are container data: they must not leak into
	// the plan.
	if bytes.Contains(cfg.Canonical, []byte("s3cret")) {
		t.Fatal("env file value leaked into canonical plan")
	}
	// Snapshot is published and immutable.
	if _, err := os.Stat(filepath.Join(cfg.Snapshot.Path, "vibe.yaml")); err != nil {
		t.Fatal(err)
	}

	// Identical inputs → identical snapshot digest and plan hash.
	cfg2, err := a.Config(ctx, ConfigRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.Snapshot.Digest != cfg.Snapshot.Digest || cfg2.Plan.CanonicalHash != cfg.Plan.CanonicalHash {
		t.Fatal("config is not deterministic")
	}

	ps, err := a.PS(ctx, PSRequest{})
	if err != nil || len(ps.Projects) != 1 {
		t.Fatalf("ps: %+v, %v", ps, err)
	}

	if _, err := a.Forget(ctx, ForgetRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, paths.ManifestRelPath)); err != nil {
		t.Fatal("forget must not touch the workspace")
	}
	ps, err = a.PS(ctx, PSRequest{})
	if err != nil || len(ps.Projects) != 0 {
		t.Fatalf("ps after forget: %+v, %v", ps, err)
	}
}

func TestConfigInvalidManifest(t *testing.T) {
	a, _ := newTestApp(t)
	ctx := context.Background()
	dir := newProject(t)
	if _, err := a.Register(ctx, RegisterRequest{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, paths.ManifestRelPath),
		[]byte("schema: 1\nharness: v2.0.0\nimage: {base: x, agents: [claude]}\nagent: {cmd: codex}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := a.Config(ctx, ConfigRequest{Dir: dir})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("invalid manifest should classify as invalid, got %v", err)
	}
}

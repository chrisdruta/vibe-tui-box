package dockerapi

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
)

// requireDaemon skips unless a Docker daemon is reachable, so the suite
// stays green on hosts without Docker while CI with a daemon exercises
// the adapter for real.
func requireDaemon(t *testing.T) *SDK {
	t.Helper()
	sdk, err := NewSDK()
	if err != nil {
		t.Skipf("docker client unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := sdk.Ping(ctx); err != nil {
		t.Skipf("docker daemon unreachable: %v", err)
	}
	return sdk
}

const integrationImage = "busybox:latest"

func TestSDKLifecycle(t *testing.T) {
	sdk := requireDaemon(t)
	ctx := context.Background()

	resolved, err := sdk.ResolveImage(ctx, ImageRef(integrationImage))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Digest.IsZero() {
		t.Fatal("resolve returned zero digest")
	}

	project := domain.ProjectID("integrationtestintegration")
	const netName = "vibe-integration-net"
	const volName = "vibe-integration-vol"
	const ctrName = "vibe-integration-dev"
	labels := map[string]string{
		"dev.vibe.managed": "true",
		projectLabel:       string(project),
		"dev.vibe.role":    "dev",
	}

	if err := sdk.EnsureNetwork(ctx, NetworkSpec{Name: netName, Labels: labels}); err != nil {
		t.Fatal(err)
	}
	defer sdk.RemoveNetwork(ctx, netName)
	// Idempotent re-ensure.
	if err := sdk.EnsureNetwork(ctx, NetworkSpec{Name: netName, Labels: labels}); err != nil {
		t.Fatalf("re-ensure network: %v", err)
	}
	if err := sdk.EnsureVolume(ctx, VolumeSpec{Name: volName, Labels: labels}); err != nil {
		t.Fatal(err)
	}
	defer sdk.RemoveVolume(ctx, volName)

	id, err := sdk.CreateContainer(ctx, CreateRequest{
		Name:    ctrName,
		Image:   integrationImage,
		Command: []string{"sleep", "60"},
		Labels:  labels,
		Mounts:  []Mount{{Kind: VolumeMount, Source: volName, Target: "/data"}},
		Network: netName,
		Policy:  Policy{DropAllCapabilities: true, NoNewPrivileges: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sdk.RemoveContainer(ctx, id, RemoveOptions{Force: true})

	if err := sdk.StartContainer(ctx, id); err != nil {
		t.Fatal(err)
	}
	state, err := sdk.InspectContainer(ctx, ctrName)
	if err != nil || !state.Running {
		t.Fatalf("container not running: %+v, %v", state, err)
	}

	listed, err := sdk.ListProjectContainers(ctx, project)
	if err != nil || len(listed) != 1 || listed[0].Name != ctrName {
		t.Fatalf("list: %+v, %v", listed, err)
	}

	var out bytes.Buffer
	res, err := sdk.Exec(ctx, ExecRequest{
		Container: ctrName,
		Argv:      []string{"sh", "-c", "echo hello && exit 7"},
		Streams:   Streams{Out: &out, Err: &out},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 7 || !bytes.Contains(out.Bytes(), []byte("hello")) {
		t.Fatalf("exec result: code=%d out=%q", res.ExitCode, out.String())
	}

	if err := sdk.StopContainer(ctx, id, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := sdk.RemoveContainer(ctx, id, RemoveOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := sdk.InspectContainer(ctx, ctrName); err == nil {
		t.Fatal("container should be gone")
	}
}

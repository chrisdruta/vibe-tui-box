package dockerapi

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"vibe/internal/domain"
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
		DNS:     []string{"192.0.2.53"}, // TEST-NET: never queried, only configured
		Log:     LogPolicy{MaxSize: "1m", MaxFiles: 2},
		Policy:  Policy{DropAllCapabilities: true, NoNewPrivileges: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sdk.RemoveContainer(ctx, id, RemoveOptions{Force: true})

	// Subscribe before the start so the stream sees it: the collected
	// events prove the managed-label filter, the action filter, and the
	// actor-attribute translation against the real daemon.
	evCtx, evCancel := context.WithCancel(ctx)
	defer evCancel()
	var evMu sync.Mutex
	var seen []ContainerEvent
	evDone := make(chan struct{})
	go func() {
		defer close(evDone)
		_ = sdk.Events(evCtx, EventsRequest{Actions: []string{"start", "die"}}, func(ev ContainerEvent) {
			evMu.Lock()
			seen = append(seen, ev)
			evMu.Unlock()
		})
	}()

	if err := sdk.StartContainer(ctx, id); err != nil {
		t.Fatal(err)
	}
	state, err := sdk.InspectContainer(ctx, ctrName)
	if err != nil || !state.Running {
		t.Fatalf("container not running: %+v, %v", state, err)
	}
	if state.Networks[netName] == "" {
		t.Fatalf("inspect returned no address on %s: %+v", netName, state.Networks)
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

	// Both lifecycle transitions arrive with the engine's own fields
	// filled from the daemon's actor attributes.
	evDeadline := time.Now().Add(10 * time.Second)
	for {
		got := map[string]bool{}
		evMu.Lock()
		for _, ev := range seen {
			if ev.Name == ctrName && ev.Project == project {
				got[ev.Action] = true
			}
		}
		evMu.Unlock()
		if got["start"] && got["die"] {
			break
		}
		if time.Now().After(evDeadline) {
			t.Fatalf("events stream missed start/die for %s: %+v", ctrName, seen)
		}
		time.Sleep(100 * time.Millisecond)
	}
	evCancel()
	<-evDone

	if err := sdk.RemoveContainer(ctx, id, RemoveOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := sdk.InspectContainer(ctx, ctrName); err == nil {
		t.Fatal("container should be gone")
	}
}

// TestSDKStagedTwinCoexistence pins the two daemon behaviors the
// transactional replacement protocol is built on: a created-but-never-
// started twin with identical host-port bindings coexists with the
// running old container (ports bind at start, not create), and after
// the old is stopped and parked the twin starts cleanly on the same
// port — the forward swap. The reverse (rollback) direction is the
// same shape with the roles reversed.
func TestSDKStagedTwinCoexistence(t *testing.T) {
	sdk := requireDaemon(t)
	ctx := context.Background()

	project := domain.ProjectID("integrationtestreplace")
	const ctrName = "vibe-integration-replace"
	labels := map[string]string{
		"dev.vibe.managed": "true",
		projectLabel:       string(project),
		"dev.vibe.role":    "dev",
	}
	req := CreateRequest{
		Name:    ctrName,
		Image:   integrationImage,
		Command: []string{"sleep", "60"},
		Labels:  labels,
		Ports:   []PortBinding{{HostIP: "127.0.0.1", HostPort: 39184, ContainerPort: 8080}},
		Network: DefaultNetwork,
		Policy:  Policy{DropAllCapabilities: true, NoNewPrivileges: true},
	}

	oldID, err := sdk.CreateContainer(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	defer sdk.RemoveContainer(ctx, oldID, RemoveOptions{Force: true})
	if err := sdk.StartContainer(ctx, oldID); err != nil {
		t.Fatal(err)
	}

	// Stage the twin under .next while the old container runs and holds
	// the host port.
	staged := req
	staged.Name = ctrName + ".next"
	newID, err := sdk.CreateContainer(ctx, staged)
	if err != nil {
		t.Fatalf("staged twin with identical port bindings must coexist with the running old container: %v", err)
	}
	defer sdk.RemoveContainer(ctx, newID, RemoveOptions{Force: true})

	// Retire and swap: stop old, park it, rename the twin in, start it.
	if err := sdk.StopContainer(ctx, oldID, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := sdk.RenameContainer(ctx, oldID, ctrName+".prev"); err != nil {
		t.Fatal(err)
	}
	if err := sdk.RenameContainer(ctx, newID, ctrName); err != nil {
		t.Fatal(err)
	}
	if err := sdk.StartContainer(ctx, newID); err != nil {
		t.Fatalf("swapped-in twin must start on the released port: %v", err)
	}
	state, err := sdk.InspectContainer(ctx, ctrName)
	if err != nil || !state.Running || state.ID != newID {
		t.Fatalf("canonical name should be the running new container: %+v, %v", state, err)
	}

	// Rename onto an occupied name must fail, not clobber.
	if err := sdk.RenameContainer(ctx, oldID, ctrName); err == nil {
		t.Fatal("rename onto an occupied name must fail")
	}
}

// TestSDKRollbackSequence pins the reverse (rollback) direction of the
// replacement protocol against the real daemon: stop the running new
// generation, park it aside, rename the old one back, and restart it
// on the same published port.
func TestSDKRollbackSequence(t *testing.T) {
	sdk := requireDaemon(t)
	ctx := context.Background()

	project := domain.ProjectID("integrationtestrollback")
	const ctrName = "vibe-integration-rollback"
	labels := map[string]string{
		"dev.vibe.managed": "true",
		projectLabel:       string(project),
		"dev.vibe.role":    "dev",
	}
	req := CreateRequest{
		Name:    ctrName,
		Image:   integrationImage,
		Command: []string{"sleep", "60"},
		Labels:  labels,
		Ports:   []PortBinding{{HostIP: "127.0.0.1", HostPort: 39185, ContainerPort: 8080}},
		Network: DefaultNetwork,
		Policy:  Policy{DropAllCapabilities: true, NoNewPrivileges: true},
	}

	// Old generation, retired to .prev (stopped) while the new one runs
	// at the canonical name — the state a mid-transaction failure
	// leaves behind.
	oldReq := req
	oldReq.Name = ctrName + ".prev"
	oldID, err := sdk.CreateContainer(ctx, oldReq)
	if err != nil {
		t.Fatal(err)
	}
	defer sdk.RemoveContainer(ctx, oldID, RemoveOptions{Force: true})
	newID, err := sdk.CreateContainer(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	defer sdk.RemoveContainer(ctx, newID, RemoveOptions{Force: true})
	if err := sdk.StartContainer(ctx, newID); err != nil {
		t.Fatal(err)
	}

	// Rollback: stop new, park it, restore old, restart it.
	if err := sdk.StopContainer(ctx, newID, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := sdk.RenameContainer(ctx, newID, ctrName+".next"); err != nil {
		t.Fatal(err)
	}
	if err := sdk.RenameContainer(ctx, oldID, ctrName); err != nil {
		t.Fatal(err)
	}
	if err := sdk.StartContainer(ctx, oldID); err != nil {
		t.Fatalf("restored old container must start on the released port: %v", err)
	}
	state, err := sdk.InspectContainer(ctx, ctrName)
	if err != nil || !state.Running || state.ID != oldID {
		t.Fatalf("canonical name should be the restored old container: %+v, %v", state, err)
	}
}

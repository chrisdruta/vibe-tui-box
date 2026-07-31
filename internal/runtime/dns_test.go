package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/chrisdruta/vibe-tui-box/internal/dockerapi"
	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/model"
)

// dnsFixtureNames is the artifact-bearing topology: the synthesized
// ledger sidecar leads, manifest services follow, dev last.
var (
	dnsName = dockerapi.ContainerName(model.SidecarContainerName(testProjectID, model.DNSServiceName))
	dbName  = dockerapi.ContainerName(model.SidecarContainerName(testProjectID, "db"))
	devName = dockerapi.ContainerName(model.DevContainerName(testProjectID))
)

// TestUpDNSSidecarTopology pins the full create request of the
// synthesized dns sidecar, the creation order, and the runtime-resolved
// resolver injection into the dev container.
func TestUpDNSSidecarTopology(t *testing.T) {
	f := newFixture(t, testManifest, withArtifact(t))
	ctx := context.Background()

	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	creates := f.docker.CallsTo("CreateContainer")
	if len(creates) != 3 {
		t.Fatalf("want 3 creates (dns, db, dev), got %d", len(creates))
	}
	dns := creates[0].Request.(dockerapi.CreateRequest)
	db := creates[1].Request.(dockerapi.CreateRequest)
	dev := creates[2].Request.(dockerapi.CreateRequest)
	if dns.Name != dnsName || db.Name != dbName || dev.Name != devName {
		t.Fatalf("creation order wrong: %s, %s, %s", dns.Name, db.Name, dev.Name)
	}

	// Full request equality for the ledger sidecar. The image runs the
	// engine's own @sha256 pin (no separate digest resolution in this
	// fixture), the Corefile rides the artifact payload read-only, and
	// nothing else — no user, env, ports, or published surface.
	plan := f.cand.Plan
	want := dockerapi.CreateRequest{
		Name:    dnsName,
		Image:   model.CoreDNSImageRef,
		User:    "0",
		Command: []string{"-conf", model.DNSCorefile},
		Labels: map[string]string{
			ManagedLabel:   "true",
			ProjectLabel:   string(testProjectID),
			RoleLabel:      "sidecar:" + model.DNSServiceName,
			CandidateLabel: f.cand.Record.Digest.String(),
			ArtifactLabel:  plan.Artifact.Digest.String(),
			TxnLabel:       dns.Labels[TxnLabel], // per-Up nonce, shape-checked below
		},
		Mounts: []dockerapi.Mount{{
			Kind:     dockerapi.BindMount,
			Source:   plan.Services[0].Mounts[0].Source,
			Target:   model.DNSConfTarget,
			ReadOnly: true,
		}},
		Ports:   []dockerapi.PortBinding{},
		Network: model.NetworkName(testProjectID),
		Log:     dockerapi.LogPolicy{MaxSize: "10m", MaxFiles: 3},
		Policy:  dockerapi.Policy{DropAllCapabilities: true, NoNewPrivileges: true, NetBindService: true},
	}
	if len(dns.Labels[TxnLabel]) != 32 {
		t.Fatalf("txn label %q is not a 32-hex nonce", dns.Labels[TxnLabel])
	}
	if !strings.HasSuffix(want.Mounts[0].Source, "/payload/dns") {
		t.Fatalf("plan mount source %q should end in /payload/dns", want.Mounts[0].Source)
	}
	assertCreateEqual(t, dns, want)

	// The dev container's resolver is the sidecar's minted address,
	// recorded as the drift label; the db sidecar stays unpointed (v1).
	ip := f.docker.Containers[dnsName].Networks[model.NetworkName(testProjectID)]
	if ip == "" {
		t.Fatal("fake minted no address for the dns sidecar")
	}
	if !equalSlices(dev.DNS, []string{ip}) || dev.Labels[DNSLabel] != ip {
		t.Fatalf("dev resolver wrong: dns=%v label=%q want %q", dev.DNS, dev.Labels[DNSLabel], ip)
	}
	if len(db.DNS) != 0 || db.Labels[DNSLabel] != "" {
		t.Fatalf("db sidecar must not be pointed in v1: %v %q", db.DNS, db.Labels[DNSLabel])
	}
}

// TestUpDNSCrashRollsBack pins the hard-dependency stance: a dns
// sidecar that starts and immediately exits has no address, which
// fails the Up before dev is created and rolls the topology back.
func TestUpDNSCrashRollsBack(t *testing.T) {
	f := newFixture(t, testManifest, withArtifact(t))
	ctx := context.Background()
	// First created container is the dns sidecar.
	f.docker.StartExits = map[dockerapi.ContainerID]bool{"ctr-001": true}
	// The forwarder's dying words are container bytes: the error must
	// carry them encoded, never raw.
	f.docker.LogsOutputs[dnsName] = "listen udp :53: bind: permission denied\x1b[31m!"

	_, err := f.svc.Up(ctx, f.cand, UpOptions{})
	if err == nil || !errors.Is(err, domain.ErrUnavailable) || !strings.Contains(err.Error(), "has no address") {
		t.Fatalf("crashing dns sidecar should fail Up with no-address, got %v", err)
	}
	if !strings.Contains(err.Error(), "running=false") || !strings.Contains(err.Error(), "bind: permission denied") {
		t.Fatalf("error should carry the crash detail, got %v", err)
	}
	if strings.ContainsRune(err.Error(), 0x1b) || !strings.Contains(err.Error(), "⟨U+001B⟩") {
		t.Fatalf("log bytes must reach the error encoded, got %q", err.Error())
	}
	if len(f.docker.Containers) != 0 {
		t.Fatalf("rollback should remove every created container: %v", f.docker.Containers)
	}
	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err != nil {
		t.Fatalf("recovered Up should succeed: %v", err)
	}
}

// TestUpDNSDriftReplacesDev pins the escalation: a restarted dns
// sidecar with a new address must replace the dev container (its
// resolv.conf was baked at create), while an unchanged address keeps
// the cheap journaled start.
func TestUpDNSDriftReplacesDev(t *testing.T) {
	f := newFixture(t, testManifest, withArtifact(t))
	ctx := context.Background()
	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	oldDevID := f.docker.Containers[devName].ID
	dnsID := f.docker.Containers[dnsName].ID

	// A plain stop/start round-trip: sticky address, no drift — dev is
	// started, never replaced.
	for _, name := range []dockerapi.ContainerName{dnsName, devName} {
		st := f.docker.Containers[name]
		st.Running = false
		st.Networks = nil
	}
	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := f.docker.Containers[devName].ID; got != oldDevID {
		t.Fatalf("same address must not replace dev: id %s -> %s", oldDevID, got)
	}
	if removes := f.docker.CallsTo("RemoveContainer"); len(removes) != 0 {
		t.Fatalf("same address should remove nothing: %d removes", len(removes))
	}

	// The sidecar re-addresses across the next stop: dev must be
	// replaced and carry the new resolver.
	for _, name := range []dockerapi.ContainerName{dnsName, devName} {
		st := f.docker.Containers[name]
		st.Running = false
		st.Networks = nil
	}
	f.docker.MintedIPs[dnsID] = "10.90.0.99"
	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	dev := f.docker.Containers[devName]
	if dev.ID == oldDevID {
		t.Fatal("re-addressed dns sidecar must replace the dev container")
	}
	if dev.Labels[DNSLabel] != "10.90.0.99" {
		t.Fatalf("replaced dev label %q, want the new address", dev.Labels[DNSLabel])
	}
	if !dev.Running {
		t.Fatal("replaced dev not running")
	}
	// The db sidecar was never replaced — only started containers moved.
	if f.docker.Containers[dbName] == nil {
		t.Fatal("db sidecar lost in the drift replace")
	}
}

// TestUpEgressOffTopology pins the opt-out: the pre-feature topology,
// no resolver injection, no drift label.
func TestUpEgressOffTopology(t *testing.T) {
	off := strings.Replace(testManifest, "runtime:\n", "runtime:\n  egress: off\n", 1)
	f := newFixture(t, off, withArtifact(t))
	ctx := context.Background()

	if _, err := f.svc.Up(ctx, f.cand, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	creates := f.docker.CallsTo("CreateContainer")
	if len(creates) != 2 {
		t.Fatalf("egress off should create db and dev only, got %d", len(creates))
	}
	dev := creates[1].Request.(dockerapi.CreateRequest)
	if dev.Name != devName || len(dev.DNS) != 0 {
		t.Fatalf("dev request wrong: %s dns=%v", dev.Name, dev.DNS)
	}
	if _, ok := dev.Labels[DNSLabel]; ok {
		t.Fatal("egress off must not stamp the dns label")
	}
}

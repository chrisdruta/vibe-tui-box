package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"vibe/internal/dockerapi"
	dockerfake "vibe/internal/dockerapi/fake"
	"vibe/internal/domain"
	"vibe/internal/model"
)

const testCoreDNSLogs = `.:53
CoreDNS-1.14.6
linux/amd64, go1.24, abcdef
[INFO] 10.4.0.2:52580 - 29008 "A IN registry.npmjs.org. udp 47 false 4096" NOERROR qr,rd,ra 110 0.000530463s
[INFO] 10.4.0.2:52581 - 29009 "AAAA IN registry.npmjs.org. udp 47 false 4096" NOERROR qr,rd,ra 110 0.000530463s
[INFO] 10.4.0.2:52582 - 29010 "A IN Registry.NPMJS.org. udp 47 false 4096" NOERROR qr,rd,ra 110 0.000530463s
[INFO] 10.4.0.2:52583 - 29011 "A IN api.anthropic.com. udp 45 false 4096" NOERROR qr,rd,ra 98 0.000410463s
[INFO] 10.4.0.2:52584 - 29012 "A IN evil.` + "\x1b[31m" + `red.example. udp 45 false 4096" NXDOMAIN qr,rd,ra 98 0.000410463s
[ERROR] plugin/errors: 2 read udp 10.4.0.53:44488->192.168.1.1:53: i/o timeout
`

const testSamplerOutput = "egress-sample\t1\n" +
	"tcp\t10.4.0.2:41892\t140.82.112.22:443\t4132\tnode\n" +
	"udp\t10.4.0.2:53412\t10.4.0.53:53\t-\t-\n" +
	"bogus\t1.2.3.4:1\t5.6.7.8:2\t1\tx\n" + // bad proto
	"tcp\tnot an address\t5.6.7.8:2\t1\tx\n" + // bad addr shape
	"tcp\t1.2.3.4:1\t5.6.7.8:2\tzero\tx\n" // bad pid

var samplerArgv = []string{"bash", model.PayloadEgressSample}

// seedEgressProject registers a project and plants the dns sidecar and
// dev container directly in the fake's world model — Egress reads live
// state, it does not reconcile.
func seedEgressProject(t *testing.T, a *App, docker *dockerfake.Client, withDNS, devRunning bool) string {
	t.Helper()
	ctx := context.Background()
	dir := newProject(t)
	reg, err := a.Register(ctx, RegisterRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	id := reg.Record.ID
	if withDNS {
		name := dockerapi.ContainerName(model.SidecarContainerName(id, model.DNSServiceName))
		docker.Containers[name] = &dockerapi.ContainerState{
			ID: "dns-1", Name: name, Running: true,
			Labels: map[string]string{"dev.vibe.managed": "true"},
		}
		docker.LogsOutputs[name] = testCoreDNSLogs
	}
	devName := dockerapi.ContainerName(model.DevContainerName(id))
	docker.Containers[devName] = &dockerapi.ContainerState{
		ID: "dev-1", Name: devName, Running: devRunning,
		Labels: map[string]string{"dev.vibe.managed": "true"},
	}
	docker.ExecOutputs[dockerfake.ExecKey(samplerArgv)] = testSamplerOutput
	return dir
}

func TestEgressFullView(t *testing.T) {
	a, docker := newTestApp(t)
	dir := seedEgressProject(t, a, docker, true, true)

	res, err := a.Egress(context.Background(), EgressRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !res.DNSAvailable || !res.SamplerAvailable {
		t.Fatalf("both halves should be available: %+v", res)
	}

	// Dedup folds case variants; order is count desc, then name.
	if len(res.Domains) != 3 {
		t.Fatalf("domains: %+v", res.Domains)
	}
	if res.Domains[0].Name != "registry.npmjs.org" || res.Domains[0].Queries != 3 || res.Domains[0].LastType != "A" {
		t.Fatalf("top domain wrong: %+v", res.Domains[0])
	}
	if res.Domains[1].Name != "api.anthropic.com" || res.Domains[1].Queries != 1 {
		t.Fatalf("second domain wrong: %+v", res.Domains[1])
	}
	// The hostile qname survives only in encoded form.
	hostile := res.Domains[2].Name
	if strings.ContainsRune(hostile, 0x1b) || !strings.Contains(hostile, "⟨U+001B⟩") {
		t.Fatalf("hostile qname not encoded: %q", hostile)
	}
	// Banner (3 lines) + [ERROR] line are unparsed, never fatal.
	if res.UnparsedLogLines != 4 {
		t.Fatalf("unparsed = %d, want 4", res.UnparsedLogLines)
	}

	if len(res.Conns) != 2 || res.MalformedRows != 3 {
		t.Fatalf("conns %+v malformed %d", res.Conns, res.MalformedRows)
	}
	if res.Conns[0].PID != 4132 || res.Conns[0].Comm != "node" || res.Conns[0].Proto != "tcp" {
		t.Fatalf("attributed conn wrong: %+v", res.Conns[0])
	}
	if res.Conns[1].PID != 0 || res.Conns[1].Comm != "" {
		t.Fatalf("unattributed conn wrong: %+v", res.Conns[1])
	}

	// Full request equality for both reads.
	logCalls := docker.CallsTo("Logs")
	if len(logCalls) != 1 {
		t.Fatalf("logs calls: %d", len(logCalls))
	}
	logReq := logCalls[0].Request.(dockerapi.LogsRequest)
	if logReq.Follow || logReq.Tail != egressDefaultTail || !strings.HasSuffix(string(logReq.Container), "-svc-dns") {
		t.Fatalf("logs request wrong: %+v", logReq)
	}
	execCalls := docker.CallsTo("Exec")
	if len(execCalls) != 1 {
		t.Fatalf("exec calls: %d", len(execCalls))
	}
	execReq := execCalls[0].Request.(dockerapi.ExecRequest)
	if execReq.User != devContainerUser || execReq.WorkDir != model.WorkspaceTarget ||
		len(execReq.Env) != 0 || execReq.TTY || execReq.Stdin {
		t.Fatalf("exec request wrong: %+v", execReq)
	}
	for i, w := range samplerArgv {
		if execReq.Argv[i] != w {
			t.Fatalf("exec argv %v", execReq.Argv)
		}
	}
}

func TestEgressDegradesPerSide(t *testing.T) {
	// No dns sidecar: ledger unavailable with a note, sampler intact.
	a, docker := newTestApp(t)
	dir := seedEgressProject(t, a, docker, false, true)
	res, err := a.Egress(context.Background(), EgressRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if res.DNSAvailable || res.DNSNote == "" || !res.SamplerAvailable {
		t.Fatalf("ledger should degrade alone: %+v", res)
	}

	// Dev stopped: sampler unavailable, ledger intact, no exec attempted.
	a, docker = newTestApp(t)
	dir = seedEgressProject(t, a, docker, true, false)
	res, err = a.Egress(context.Background(), EgressRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !res.DNSAvailable || res.SamplerAvailable || res.SamplerNote == "" {
		t.Fatalf("sampler should degrade alone: %+v", res)
	}
	if len(docker.CallsTo("Exec")) != 0 {
		t.Fatal("stopped dev must not be exec'd")
	}

	// Both missing: unavailable, exit-code class 6.
	a, docker = newTestApp(t)
	dir = seedEgressProject(t, a, docker, false, false)
	if _, err := a.Egress(context.Background(), EgressRequest{Dir: dir}); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("both halves missing should be unavailable, got %v", err)
	}
}

func TestEgressSamplerVersionSkew(t *testing.T) {
	a, docker := newTestApp(t)
	dir := seedEgressProject(t, a, docker, true, true)
	docker.ExecOutputs[dockerfake.ExecKey(samplerArgv)] = "egress-sample\t2\nrubbish\n"

	res, err := a.Egress(context.Background(), EgressRequest{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if res.SamplerAvailable || !strings.Contains(res.SamplerNote, "not understood") {
		t.Fatalf("version skew should fail the sample: %+v", res)
	}
	if !res.DNSAvailable {
		t.Fatal("ledger half should survive sampler skew")
	}
}

func TestParseCoreDNSLogsBounds(t *testing.T) {
	// Domain cap: existing keys keep counting, new ones drop.
	var b strings.Builder
	for i := range 4 {
		fmt.Fprintf(&b, "[INFO] 1.2.3.4:1 - 1 %q NOERROR qr 100 0.001s\n",
			fmt.Sprintf("A IN d%c.example. udp 40 false 4096", 'a'+i))
	}
	domains, unparsed, truncated := parseCoreDNSLogs(b.String(), 2)
	if len(domains) != 2 || !truncated || unparsed != 0 {
		t.Fatalf("cap wrong: %d domains truncated=%v unparsed=%d", len(domains), truncated, unparsed)
	}

	// Oversize and empty qnames are unparsed, not retained.
	long := `[INFO] 1.2.3.4:1 - 1 "A IN ` + strings.Repeat("x", 300) + `. udp 40 false 4096" NOERROR qr 100 0.001s`
	if d, u, _ := parseCoreDNSLogs(long, 10); len(d) != 0 || u != 1 {
		t.Fatalf("oversize qname should be unparsed: %d %d", len(d), u)
	}
}

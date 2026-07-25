package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chrisdruta/vibe-tui-box/internal/app"
	"github.com/chrisdruta/vibe-tui-box/internal/runtime"
)

func decodeEnvelope(t *testing.T, r Result) (kind string, data map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if err := r.RenderJSON(&buf); err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Format int            `json:"format"`
		Kind   string         `json:"kind"`
		Data   map[string]any `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &envelope); err != nil {
		t.Fatalf("envelope does not decode: %v\n%s", err, buf.String())
	}
	if envelope.Format != 1 {
		t.Fatalf("format = %d, want 1", envelope.Format)
	}
	return envelope.Kind, envelope.Data
}

func TestExecResult(t *testing.T) {
	r := &execResult{Code: 3}
	if r.ExitCode() != 3 {
		t.Fatal("exec result must propagate the container exit code")
	}
	var buf bytes.Buffer
	if err := r.RenderHuman(&buf); err != nil || buf.Len() != 0 {
		t.Fatal("exec result human output must be empty")
	}
	kind, data := decodeEnvelope(t, r)
	if kind != "exec" || data["exit_code"] != float64(3) {
		t.Fatalf("exec envelope: kind %q, data %v", kind, data)
	}
}

func TestPSResultJSONNeverNull(t *testing.T) {
	// Empty result: both collections must encode as [], not null —
	// scripted consumers iterate them unconditionally.
	kind, data := decodeEnvelope(t, &psResult{Result: app.PSResult{}})
	if kind != "ps" {
		t.Fatalf("kind = %q", kind)
	}
	for _, key := range []string{"projects", "agents"} {
		v, ok := data[key]
		if !ok {
			t.Fatalf("%s missing from envelope", key)
		}
		if _, isList := v.([]any); !isList {
			t.Fatalf("%s = %v (%T), want empty list", key, v, v)
		}
	}
}

func TestRenderState(t *testing.T) {
	var buf bytes.Buffer
	if err := renderState(&buf, runtime.State{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "no containers") {
		t.Fatalf("empty state hint missing: %q", buf.String())
	}

	buf.Reset()
	state := runtime.State{Containers: []runtime.ContainerStatus{
		{Name: "proj-dev", Role: "dev", Running: true, InSync: true},
		{Name: "proj-db", Role: "db", Running: false, InSync: false},
	}}
	if err := renderState(&buf, state); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "running") || !strings.Contains(out, "stopped") {
		t.Fatalf("container states missing:\n%s", out)
	}
	if !strings.Contains(out, "stale candidate") {
		t.Fatalf("out-of-sync marker missing:\n%s", out)
	}
}

func TestHealthExitCodes(t *testing.T) {
	if (&doctorResult{Result: app.DoctorResult{Healthy: true}}).ExitCode() != 0 {
		t.Fatal("healthy doctor must exit 0")
	}
	if (&doctorResult{Result: app.DoctorResult{Healthy: false}}).ExitCode() != 1 {
		t.Fatal("unhealthy doctor must exit 1")
	}
	if (&bootstrapResult{Result: app.BootstrapResult{Missing: 0}}).ExitCode() != 0 {
		t.Fatal("complete bootstrap must exit 0")
	}
	if (&bootstrapResult{Result: app.BootstrapResult{Missing: 2}}).ExitCode() != 1 {
		t.Fatal("missing tools must exit 1")
	}
}

func TestGCResultHuman(t *testing.T) {
	var buf bytes.Buffer
	empty := &gcResult{Result: app.GCResult{}}
	if err := empty.RenderHuman(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "nothing to collect") {
		t.Fatalf("empty gc: %q", buf.String())
	}

	buf.Reset()
	dry := &gcResult{Result: app.GCResult{
		DryRun:     true,
		Removed:    []app.GCEntry{{Kind: "artifact"}, {Kind: "snapshot"}},
		TotalBytes: 3 << 20,
	}}
	if err := dry.RenderHuman(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "would remove 2 item(s)") {
		t.Fatalf("dry-run verb wrong:\n%s", out)
	}
	if !strings.Contains(out, "3.0 MiB") {
		t.Fatalf("byte size missing:\n%s", out)
	}
}

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{512, "512 B"},
		{2 << 10, "2.0 KiB"},
		{5 << 20, "5.0 MiB"},
		{3 << 30, "3.0 GiB"},
	}
	for _, tc := range cases {
		if got := humanBytes(tc.n); got != tc.want {
			t.Errorf("humanBytes(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

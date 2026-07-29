package builder

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chrisdruta/vibe-tui-box/internal/schema"
)

// probeServer fakes the three release endpoints on one mux; the probe's
// URLs are consts, so tests exercise Resolve's parsing/vetting through
// the fetch helpers plus ParseAgentRefresh, and leave the const URLs to
// the wiring (one GET against the same files the installers read).
func probeServer(t *testing.T, handler http.HandlerFunc) (AgentVersionProbe, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return AgentVersionProbe{Client: srv.Client()}, srv
}

func TestFetchVersionFile(t *testing.T) {
	p, srv := probeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			w.Write([]byte("2.1.220\n"))
		case "/html":
			w.Write([]byte("<html>maintenance</html>"))
		default:
			http.NotFound(w, r)
		}
	})
	v, err := p.fetchVersionFile(context.Background(), srv.URL+"/latest")
	if err != nil || v != "2.1.220" {
		t.Fatalf("version file: %q, %v", v, err)
	}
	// A non-200 is a failed probe, never a token.
	if _, err := p.fetchVersionFile(context.Background(), srv.URL+"/missing"); err == nil {
		t.Fatal("404 must fail the probe")
	}
	// Resolve vets the shape: HTML (an error page behind a 200) is
	// rejected before it can reach a build arg. Exercised through the
	// shared validation, not the const URL.
	if resolvedVersionRe.MatchString("<html>maintenance</html>") {
		t.Fatal("error-page content must not pass the version shape")
	}
}

func TestResolvedVersionShape(t *testing.T) {
	// The value lands inside generated RUN text as a build arg: only
	// version-shaped, shell-inert content may pass.
	for _, ok := range []string{"2.1.220", "0.146.0", "0.2.114", "1.2.3-beta.1", "1.2.3+build_4"} {
		if !resolvedVersionRe.MatchString(ok) {
			t.Errorf("%q should pass", ok)
		}
	}
	for _, bad := range []string{
		"", "latest", `2.1.220"; rm -rf /`, "2.1.220 extra", "2.1.220$PATH",
		"v2.1.220", "2.1.220\n2.1.221", strings.Repeat("1.2.3", 20),
	} {
		if resolvedVersionRe.MatchString(bad) {
			t.Errorf("%q must not pass", bad)
		}
	}
}

func TestResolveVetsChannel(t *testing.T) {
	p := AgentVersionProbe{}
	// A channel joins a URL path: anything outside the closed charset
	// fails before any network I/O (nil client would panic on use —
	// not reaching it is the assertion).
	for _, bad := range []string{"", "a/b", "a b", "..%2f", strings.Repeat("x", 65)} {
		if _, err := p.Resolve(context.Background(), schema.AgentClaude, bad); err == nil {
			t.Errorf("channel %q must be rejected", bad)
		}
	}
}

func TestParseAgentRefresh(t *testing.T) {
	got := ParseAgentRefresh("claude=2.1.220 codex=0.146.0")
	if len(got) != 2 || got[schema.AgentClaude] != "2.1.220" || got[schema.AgentCodex] != "0.146.0" {
		t.Fatalf("pairs: %v", got)
	}
	// A legacy bare-timestamp token has no pairs: callers fall back to
	// the whole token, the pre-fingerprint bust.
	if got := ParseAgentRefresh("2026-07-29T12:00:00.000000001Z"); len(got) != 0 {
		t.Fatalf("legacy token must parse empty: %v", got)
	}
	if got := ParseAgentRefresh(""); len(got) != 0 {
		t.Fatalf("empty token must parse empty: %v", got)
	}
	// Timestamps as fallback VALUES round-trip (RFC3339Nano carries no
	// space and no '=').
	got = ParseAgentRefresh("claude=2.1.220 codex=2026-07-29T12:00:00.000000001Z")
	if got[schema.AgentCodex] != "2026-07-29T12:00:00.000000001Z" {
		t.Fatalf("timestamp value: %v", got)
	}
}

func TestAgentChannel(t *testing.T) {
	for _, tc := range []struct {
		spec schema.AgentSpec
		want string
	}{
		{schema.AgentSpec{Kind: schema.AgentClaude}, claudeChannel},
		{schema.AgentSpec{Kind: schema.AgentCodex}, codexChannel},
		{schema.AgentSpec{Kind: schema.AgentGrok}, grokChannel},
		{schema.AgentSpec{Kind: schema.AgentClaude, Version: "stable"}, "stable"},
		{schema.AgentSpec{Kind: schema.AgentCodex, Version: "next"}, "next"},
		{schema.AgentSpec{Kind: schema.AgentClaude, Version: "2.1.220"}, ""}, // exact pin: nothing to probe
	} {
		if got := AgentChannel(tc.spec); got != tc.want {
			t.Errorf("AgentChannel(%+v) = %q, want %q", tc.spec, got, tc.want)
		}
	}
}

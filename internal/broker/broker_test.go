package broker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/paths"
)

func TestParseRequest(t *testing.T) {
	good := `{"format":1,"id":"req-1","kind":"rebuild","reason":"new port","summary":"add 8080"}`
	req, err := ParseRequest([]byte(good))
	if err != nil || req.ID != "req-1" || req.Kind != RebuildKind {
		t.Fatalf("good request: %+v, %v", req, err)
	}
	bad := map[string]string{
		"unknown field": `{"format":1,"id":"a","kind":"rebuild","extra":1}`,
		"bad format":    `{"format":9,"id":"a","kind":"rebuild"}`,
		"bad id":        `{"format":1,"id":"UPPER CASE","kind":"rebuild"}`,
		"bad kind":      `{"format":1,"id":"a","kind":"exec"}`,
		"not json":      `hello`,
		"long text":     `{"format":1,"id":"a","kind":"rebuild","reason":"` + strings.Repeat("x", 5000) + `"}`,
	}
	for name, data := range bad {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseRequest([]byte(data)); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func workspaceWithRequests(t *testing.T, files map[string]string) paths.Root {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, RequestsRelDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, paths.ManifestRelPath), []byte("schema: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, RequestsRelDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root, err := paths.Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestReadRequests(t *testing.T) {
	root := workspaceWithRequests(t, map[string]string{
		"b.json":      `{"format":1,"id":"beta","kind":"rebuild","summary":"b"}`,
		"a.json":      `{"format":1,"id":"alpha","kind":"rebuild","summary":"a"}`,
		"broken.json": `{nope`,
		"notes.txt":   "ignored",
	})
	raws, problems := ReadRequests(root)
	if len(raws) != 2 || raws[0].Request.ID != "alpha" || raws[1].Request.ID != "beta" {
		t.Fatalf("raws: %+v", raws)
	}
	if len(problems) != 1 || !strings.Contains(problems[0].Error(), "broken.json") {
		t.Fatalf("problems: %v", problems)
	}
	// A symlinked request file is skipped, not followed.
	if err := os.Symlink("/etc/passwd", filepath.Join(root.Path, RequestsRelDir, "link.json")); err != nil {
		t.Fatal(err)
	}
	raws, _ = ReadRequests(root)
	if len(raws) != 2 {
		t.Fatalf("symlink was not ignored: %+v", raws)
	}
}

func TestReadRequestsNoDir(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".vibe"), 0o755)
	os.WriteFile(filepath.Join(dir, paths.ManifestRelPath), []byte("schema: 1\n"), 0o644)
	root, err := paths.Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	raws, problems := ReadRequests(root)
	if raws != nil || problems != nil {
		t.Fatalf("missing dir should be empty: %v %v", raws, problems)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	s, err := NewStore(t.TempDir(), "abcdefghijklmnopqrstuvwxyz")
	if err != nil {
		t.Fatal(err)
	}
	p := Pending{
		RequestID: "req-1",
		ProjectID: "abcdefghijklmnopqrstuvwxyz",
		Candidate: domain.SHA256([]byte("cand")),
		Content:   domain.SHA256([]byte("content")),
		Summary:   "s",
		CreatedAt: time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC),
	}
	if err := s.WritePending(p); err != nil {
		t.Fatal(err)
	}
	pending, err := s.ListPending()
	if err != nil || len(pending) != 1 || pending[0].Candidate != p.Candidate {
		t.Fatalf("pending: %+v, %v", pending, err)
	}
	now := p.CreatedAt.Add(time.Hour)
	if err := s.WriteResult(Result{
		RequestID: p.RequestID, ProjectID: p.ProjectID, Candidate: &p.Candidate,
		Status: StatusApproved, CreatedAt: p.CreatedAt, CompletedAt: &now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePending(p.RequestID); err != nil {
		t.Fatal(err)
	}
	pending, _ = s.ListPending()
	if len(pending) != 0 {
		t.Fatal("pending not cleared")
	}
	results, err := s.ListResults()
	if err != nil || len(results) != 1 || results[0].Status != StatusApproved {
		t.Fatalf("results: %+v, %v", results, err)
	}
	if _, err := os.Stat(filepath.Join(s.ResultsDir(), "req-1.json")); err != nil {
		t.Fatal("result file not in the results mount dir")
	}
}

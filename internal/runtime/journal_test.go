package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vibe/internal/domain"
)

// TestLoadJournalStrictness pins the decode contract at the unit
// level: every valid entry kind round-trips, and structurally invalid
// or trailing content is rejected.
func TestLoadJournalStrictness(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.json")
	digest := domain.SHA256([]byte("cand")).String()
	head := `{"version":1,"candidate":"` + digest + `","nonce":"` + strings.Repeat("ab", 16) + `"`

	valid := head + `,"entries":[` +
		`{"kind":"create","name":"a"},` +
		`{"kind":"replace","name":"b","old_id":"x","was_running":true,"new_id":"y"},` +
		`{"kind":"start","name":"c","id":"z"}]}`
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	j, err := loadJournal(path)
	if err != nil || j == nil || len(j.Entries) != 3 {
		t.Fatalf("valid journal must load: %+v, %v", j, err)
	}

	for name, body := range map[string]string{
		"trailing delimiter": valid + `]`,
		"trailing object":    valid + `{"x":1}`,
		"unknown field":      head + `,"entries":[],"extra":1}`,
		"unknown kind":       head + `,"entries":[{"kind":"weird","name":"a"}]}`,
		"replace no old_id":  head + `,"entries":[{"kind":"replace","name":"a"}]}`,
		"start no id":        head + `,"entries":[{"kind":"start","name":"a"}]}`,
		"bad nonce":          `{"version":1,"candidate":"` + digest + `","nonce":"xyz","entries":[]}`,
		"bad version":        `{"version":9,"candidate":"` + digest + `","nonce":"` + strings.Repeat("ab", 16) + `","entries":[]}`,
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadJournal(path); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}

	// A missing file is not an error — it is the committed state.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if j, err := loadJournal(path); err != nil || j != nil {
		t.Fatalf("missing journal must be (nil, nil), got %+v, %v", j, err)
	}
}

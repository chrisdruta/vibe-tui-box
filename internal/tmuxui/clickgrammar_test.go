package tmuxui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestClickGrammarContract pins the one hand-mirrored cross-language
// vocabulary a contributor is most likely to extend: the click-target
// grammar. frame.go mints targets into @vibe_sidebar_map; sidebar.sh
// (click mode) and agent-menu.sh (row mode) dispatch them with case
// patterns. Nothing but dogfooding catches a new target kind with no
// dispatcher arm, so this test does: every ":word" / ":word-" literal
// in frame.go must have a matching arm in sidebar.sh, and the roster
// shapes must have arms in agent-menu.sh. Extraction is textual on
// purpose — the grammar lives in string literals on both sides, and
// go/parser would pin the same strings with more machinery.
func TestClickGrammarContract(t *testing.T) {
	src, err := os.ReadFile("frame.go")
	if err != nil {
		t.Fatal(err)
	}
	// Emitted shapes: the ":word-" prefix and ":word" leaf literals in
	// target expressions. The viewer-window form (SESS:@WID) is built
	// from window ids rather than a literal, so it is seeded by hand.
	shapes := map[string]bool{"@": true}
	for _, m := range regexp.MustCompile(`":([a-z]+-?)"`).FindAllStringSubmatch(string(src), -1) {
		shapes[m[1]] = true
	}
	if len(shapes) < 7 { // @, dead-, agent-, svc-, svcx-, egress, svcfold
		t.Fatalf("extracted only %d target shapes from frame.go — did the literals move out of reach of this test's regex?", len(shapes))
	}

	script := func(name string) string {
		data, err := os.ReadFile(filepath.Join("..", "..", "payload", "host", "scripts", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	arm := func(shape string) string {
		if strings.HasSuffix(shape, "-") || shape == "@" {
			return "*:" + shape + "*)"
		}
		return "*:" + shape + ")"
	}

	// sidebar.sh's click mode is the primary dispatcher: every emitted
	// shape must have an arm (its trailing default arm is the bare
	// project row, not a licence to drop typed targets).
	sidebar := script("sidebar.sh")
	for shape := range shapes {
		if !strings.Contains(sidebar, arm(shape)) {
			t.Errorf("frame.go emits click targets shaped %q but sidebar.sh has no %q case arm", "SESS:"+shape+"…", arm(shape))
		}
	}

	// agent-menu.sh serves right-clicks for the roster shapes only;
	// egress and svcfold rows have no menu by design (the catch-all
	// exits), so those two are exempt rather than silently dropped.
	menu := script("agent-menu.sh")
	for shape := range shapes {
		if shape == "egress" || shape == "svcfold" {
			continue
		}
		if !strings.Contains(menu, arm(shape)) {
			t.Errorf("roster click targets shaped %q have no %q case arm in agent-menu.sh row mode", "SESS:"+shape+"…", arm(shape))
		}
	}
}

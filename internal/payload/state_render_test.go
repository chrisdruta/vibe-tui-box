package payload

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stateRender runs state-render.sh against a fake tmux that scripts
// the two out-of-band reads (pane info and pane title) and records
// every tmux argv. The title is the ONE place container-controlled
// bytes become tmux option values with no Go in the loop (the
// title-channel design spike), so its four hand-rolled allowlists get
// real execution here, not just prose.
func stateRender(t *testing.T, info, title string, args ...string) []string {
	t.Helper()
	script, err := filepath.Abs(filepath.Join("..", "..", "payload", "host", "scripts", "state-render.sh"))
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	rec := filepath.Join(t.TempDir(), "argv")
	fake := `#!/bin/sh
printf '%s\n' "$@" >>"$VIBE_TEST_REC"
printf -- '----\n' >>"$VIBE_TEST_REC"
case "$*" in
*pane_dead*) printf '%s\n' "$VIBE_TEST_INFO" ;;
*pane_title*) printf '%s\n' "$VIBE_TEST_TITLE" ;;
*show-options*) printf '%s\n' "$VIBE_TEST_PREV" ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"bash", "printf", "date"} {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("test needs %s", tool)
		}
		if err := os.Symlink(p, filepath.Join(bin, tool)); err != nil && !os.IsExist(err) {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("bash", append([]string{script}, args...)...)
	cmd.Env = []string{
		"PATH=" + bin,
		"VIBE_TEST_REC=" + rec,
		"VIBE_TEST_INFO=" + info,
		"VIBE_TEST_TITLE=" + title,
		"VIBE_TEST_PREV=idle",
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("state-render.sh: %v\n%s", err, out)
	}
	data, _ := os.ReadFile(rec)
	return strings.Split(string(data), "----\n")
}

// optionValue digs one set-option value out of a recorded tmux call.
func optionValue(call, option string) (string, bool) {
	words := strings.Split(strings.TrimRight(call, "\n"), "\n")
	for i, w := range words {
		if w == option && i+1 < len(words) {
			return words[i+1], true
		}
	}
	return "", false
}

// TestStateRenderScrubsHostileTitle feeds a title whose
// container-controlled fields carry every class of byte the allowlists
// exist for — shell metacharacters, quotes, an ESC byte, an
// over-length run — and asserts what lands in tmux options and the
// window name is the scrubbed, clamped remainder.
func TestStateRenderScrubsHostileTitle(t *testing.T) {
	esc := string(rune(0x1b))
	title := "vibe1|proj|agent-x;$(reboot)|7|idle|cl'au\"de" + esc + "[31m:v2|opus $(id) 4" + esc + "]52;x"
	calls := stateRender(t, "0||", title, "%42")

	var set string
	for _, c := range calls {
		if strings.HasPrefix(c, "set-option\n") {
			set = c
			break
		}
	}
	if set == "" {
		t.Fatalf("no set-option chain recorded:\n%q", calls)
	}
	if strings.ContainsRune(set, 0x1b) {
		t.Fatalf("raw ESC byte reached tmux options:\n%q", set)
	}
	if v, ok := optionValue(set, "@vibe_session"); !ok || v != "agent-xreboot" {
		t.Fatalf("@vibe_session = %q — the address charset must strip metacharacters", v)
	}
	if v, ok := optionValue(set, "@vibe_model"); !ok || v != "opus id 452x" {
		t.Fatalf("@vibe_model = %q — the model charset must strip quotes and escapes", v)
	}
	if v, ok := optionValue(set, "@vibe_state"); !ok || v != "idle" {
		t.Fatalf("@vibe_state = %q", v)
	}

	// The display field becomes the window NAME: same scrub, applied
	// before rename-window ever sees it.
	var rename string
	for _, c := range calls {
		if strings.HasPrefix(c, "rename-window\n") {
			rename = c
			break
		}
	}
	if rename == "" {
		t.Fatalf("display present, but no rename-window recorded:\n%q", calls)
	}
	words := strings.Split(strings.TrimRight(rename, "\n"), "\n")
	found := false
	for _, w := range words {
		// The allowlist strips bytes, it does not parse sequences: the
		// ESC and bracket vanish, the digits stay — plain text either way.
		if w == "claude31m:v2" {
			found = true
		}
		if strings.ContainsAny(w, "'\"$();") && strings.Contains(w, "clau") {
			t.Fatalf("unscrubbed display reached rename-window: %q", w)
		}
	}
	if !found {
		t.Fatalf("scrubbed display missing from rename-window: %q", words)
	}
}

// TestStateRenderRefusesOffGrammarInput pins the render-nothing
// verdicts: a non-vibe title, an unknown state word, and a title event
// racing a dead pane must all end before any option write.
func TestStateRenderRefusesOffGrammarInput(t *testing.T) {
	cases := []struct {
		name  string
		info  string
		title string
	}{
		{"not an agent title", "0||", "root@host: ~/src"},
		{"unknown state word", "0||", "vibe1|proj|agent|7|exploded|d|m"},
		{"dead pane keeps its mark", "1|working|w", "vibe1|proj|agent|7|idle|d|m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, call := range stateRender(t, tc.info, tc.title, "%42") {
				if strings.HasPrefix(call, "set-option\n") || strings.HasPrefix(call, "rename-window\n") {
					t.Fatalf("must render nothing, wrote:\n%q", call)
				}
			}
		})
	}
}

// TestStateRenderFrontendDead pins the pane-died path: the forced
// state lands only on a pane that is actually dead AND already carried
// an agent state — host shell panes never grow a dot.
func TestStateRenderFrontendDead(t *testing.T) {
	calls := stateRender(t, "1|working|w", "ignored", "%42", "frontend-dead")
	var set string
	for _, c := range calls {
		if strings.HasPrefix(c, "set-option\n") {
			set = c
			break
		}
	}
	if set == "" {
		t.Fatalf("dead pane with agent state must take the forced mark:\n%q", calls)
	}
	if v, ok := optionValue(set, "@vibe_state"); !ok || v != "frontend-dead" {
		t.Fatalf("@vibe_state = %q, want frontend-dead", v)
	}

	// A live pane must not: the guards are the inverse of the title path.
	for _, call := range stateRender(t, "0|working|w", "ignored", "%42", "frontend-dead") {
		if strings.HasPrefix(call, "set-option\n") {
			t.Fatalf("live pane took a forced dead mark:\n%q", call)
		}
	}
}

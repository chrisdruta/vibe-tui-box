package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stageThemeTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "host", "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	conf := "# head, hand-edited\n" +
		themeBegin + "\nset -g @thm_bg \"#stale0\"\n" + themeEnd + "\n" +
		"# middle, hand-edited\n" +
		winlistBegin + "\n" + winlistEnd + "\n" +
		borderBegin + "\nset -g status-format[0] \"stale\"\n" + borderEnd + "\n" +
		"# tail, hand-edited\n"
	if err := os.WriteFile(filepath.Join(root, "host", "tmux-tui.conf"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestRenderThemeIdempotent(t *testing.T) {
	root := stageThemeTree(t)
	if err := renderTheme(root); err != nil {
		t.Fatal(err)
	}
	read := func(rel string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	sh1 := read("host/scripts/theme.sh")
	conf1 := read("host/tmux-tui.conf")

	// The renderings carry the palette and the state map; the stale
	// in-marker content is gone; hand-edited text survives.
	if !strings.Contains(sh1, `VIBE_THM_CORAL="#e8735a"`) ||
		!strings.Contains(sh1, `vibe_state_hex="$VIBE_THM_GREEN"`) {
		t.Fatalf("theme.sh incomplete:\n%s", sh1)
	}
	if !strings.Contains(conf1, `set -g @thm_coral   "#e8735a"`) ||
		strings.Contains(conf1, "#stale0") {
		t.Fatalf("conf splice wrong:\n%s", conf1)
	}
	if !strings.HasPrefix(conf1, "# head, hand-edited\n") ||
		!strings.Contains(conf1, "# middle, hand-edited\n") ||
		!strings.HasSuffix(conf1, "# tail, hand-edited\n") {
		t.Fatalf("hand-edited text lost:\n%s", conf1)
	}
	// The winlist block carries the composed tray line: the stock W:
	// construct wrapped in the vibe cells; the border block replaced its
	// stale content with the fixed-width rule.
	if !strings.Contains(conf1, `set -g @vibe_winlist "#[list=on align=#{status-justify}]`) ||
		!strings.Contains(conf1, "#{W:"+stockWindowCell+","+stockCurrentWindowCell+"}") {
		t.Fatalf("winlist block wrong:\n%s", conf1)
	}
	if strings.Contains(conf1, `"stale"`) ||
		!strings.Contains(conf1, "╰"+strings.Repeat("─", barBorderWidth-1)+`#[align=right]╯`) {
		t.Fatalf("border block wrong:\n%s", conf1)
	}

	// Second run: byte-identical (the drift gate depends on this).
	if err := renderTheme(root); err != nil {
		t.Fatal(err)
	}
	if read("host/scripts/theme.sh") != sh1 || read("host/tmux-tui.conf") != conf1 {
		t.Fatal("renderTheme is not idempotent")
	}
}

func TestSpliceBlockRequiresMarkers(t *testing.T) {
	if _, err := spliceBlock("# a conf without markers\n", themeBegin, themeEnd, "x\n"); err == nil {
		t.Fatal("missing markers must error, not append")
	}
	if _, err := spliceBlock(themeEnd+"\n"+themeBegin+"\n", themeBegin, themeEnd, "x\n"); err == nil {
		t.Fatal("reordered markers must error")
	}
}

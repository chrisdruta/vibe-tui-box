package terminal

import (
	"fmt"
	"strings"
	"testing"
)

func TestDiffIdentical(t *testing.T) {
	for _, s := range []string{"", "one\ntwo\n", "x"} {
		got := Diff(s, s, DiffLimits{})
		if len(got.Lines) != 0 || got.Truncated {
			t.Fatalf("Diff(%q, same) = %+v, want empty", s, got)
		}
	}
}

func TestDiffLocalChange(t *testing.T) {
	before := "a\nb\nc\nd\ne\nf\ng\n"
	after := "a\nb\nc\nD\ne\nf\ng\n"
	got := Diff(before, after, DiffLimits{})
	want := []string{
		"··· 1 unchanged line(s)",
		"  b",
		"  c",
		"- d",
		"+ D",
		"  e",
		"  f",
		"··· 1 unchanged line(s)",
	}
	if strings.Join(got.Lines, "|") != strings.Join(want, "|") {
		t.Fatalf("diff lines:\n%s\nwant:\n%s", strings.Join(got.Lines, "\n"), strings.Join(want, "\n"))
	}
	if got.Truncated {
		t.Fatalf("unexpected truncation")
	}
}

func TestDiffMergesCloseHunks(t *testing.T) {
	before := "a\nb\nc\nd\ne\n"
	after := "A\nb\nc\nd\nE\n"
	got := Diff(before, after, DiffLimits{})
	// One merged hunk: the 3-line gap fits inside 2*context+1.
	for _, line := range got.Lines {
		if strings.HasPrefix(line, "···") {
			t.Fatalf("expected one merged hunk, got separator in %q", got.Lines)
		}
	}
	joined := strings.Join(got.Lines, "\n")
	for _, want := range []string{"- a", "+ A", "- e", "+ E", "  c"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in:\n%s", want, joined)
		}
	}
}

func TestDiffAdditionOnly(t *testing.T) {
	got := Diff("", "x\ny\n", DiffLimits{})
	want := []string{"+ x", "+ y"}
	if strings.Join(got.Lines, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q, want %q", got.Lines, want)
	}
}

func TestDiffBoundsOutputLines(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&b, "line-%d\n", i)
	}
	got := Diff("", b.String(), DiffLimits{MaxLines: 10})
	if len(got.Lines) != 10 || !got.Truncated {
		t.Fatalf("got %d lines truncated=%v, want 10 truncated", len(got.Lines), got.Truncated)
	}
}

func TestDiffRejectsOversizedInput(t *testing.T) {
	big := strings.Repeat("x\n", 5000)
	got := Diff(big, "y\n", DiffLimits{})
	if !got.Truncated || len(got.Lines) != 1 || !strings.Contains(got.Lines[0], "too large") {
		t.Fatalf("got %+v, want single too-large note", got)
	}
}

func TestDiffSanitizesContent(t *testing.T) {
	got := Diff("safe\n", "safe\nevil\x1b[2Jline\n", DiffLimits{})
	joined := strings.Join(got.Lines, "\n")
	if strings.ContainsRune(joined, 0x1b) {
		t.Fatalf("escape byte survived: %q", joined)
	}
	if !strings.Contains(joined, "⟨U+001B⟩") {
		t.Fatalf("escape not made visible: %q", joined)
	}
}

func TestDiffTruncatesWideLines(t *testing.T) {
	wide := strings.Repeat("w", 300)
	got := Diff("", wide+"\n", DiffLimits{MaxWidth: 40})
	if len(got.Lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(got.Lines))
	}
	if r := []rune(got.Lines[0]); len(r) > 40 {
		t.Fatalf("line width %d exceeds 40: %q", len(r), got.Lines[0])
	}
	if !strings.HasSuffix(got.Lines[0], "…") {
		t.Fatalf("missing ellipsis: %q", got.Lines[0])
	}
}

func TestDiffLargeMiddleDegradesButBounds(t *testing.T) {
	// Force the LCS fallback: two disjoint 1500-line middles exceed the
	// cell budget (2.25M > 1M) once shared frame is absent.
	var a, b strings.Builder
	for i := 0; i < 1500; i++ {
		fmt.Fprintf(&a, "a-%d\n", i)
		fmt.Fprintf(&b, "b-%d\n", i)
	}
	got := Diff(a.String(), b.String(), DiffLimits{MaxLines: 20})
	if !got.Truncated || len(got.Lines) != 20 {
		t.Fatalf("got %d lines truncated=%v, want 20 truncated", len(got.Lines), got.Truncated)
	}
	if !strings.HasPrefix(got.Lines[0], "- ") {
		t.Fatalf("fallback should start with deletions, got %q", got.Lines[0])
	}
}

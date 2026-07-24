package terminal

import (
	"context"
	"strings"
	"testing"
)

func TestEncodeSanitizes(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string // first line
	}{
		{"plain", "hello", "hello"},
		{"ansi escape", "\x1b[31mred\x1b[0m", "⟨U+001B⟩[31mred⟨U+001B⟩[0m"},
		{"bell and cr", "ding\a", "ding⟨U+0007⟩"},
		{"bidi override", "abc\u202edef", "abc⟨U+202E⟩def"},
		{"tab", "a\tb", "a    b"},
		{"invalid utf8", "a\xffb", "a�b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc := Encode(tc.input, Limits{})
			if len(enc.Lines) == 0 || enc.Lines[0] != tc.want {
				t.Fatalf("Encode(%q) = %q, want first line %q", tc.input, enc.Lines, tc.want)
			}
		})
	}
}

func TestEncodeBounds(t *testing.T) {
	enc := Encode(strings.Repeat("x", 50), Limits{MaxWidth: 10, MaxLines: 3})
	if len(enc.Lines) != 3 || !enc.Truncated {
		t.Fatalf("width/line bounding failed: %d lines, truncated=%v", len(enc.Lines), enc.Truncated)
	}
	for _, l := range enc.Lines {
		if len([]rune(l)) > 10 {
			t.Fatalf("line %q exceeds width", l)
		}
	}
	many := strings.Repeat("line\n", 100)
	enc = Encode(many, Limits{MaxLines: 5})
	if len(enc.Lines) != 5 || !enc.Truncated {
		t.Fatal("line cap not enforced")
	}
}

func TestLine(t *testing.T) {
	if got := Line("short", 20); got != "short" {
		t.Fatalf("Line = %q", got)
	}
	got := Line(strings.Repeat("a", 50), 10)
	if len([]rune(got)) > 10 || !strings.HasSuffix(got, "…") {
		t.Fatalf("Line truncation wrong: %q", got)
	}
	if got := Line("evil\nchrome", 40); strings.Contains(got, "\n") {
		t.Fatalf("Line leaked newline: %q", got)
	}
}

func TestStdioPromptQuestion(t *testing.T) {
	ask := func(c Confirmation, answer string) (bool, string) {
		t.Helper()
		var out strings.Builder
		p := &StdioPrompt{In: strings.NewReader(answer), Out: &out}
		ok, err := p.Confirm(context.Background(), c)
		if err != nil {
			t.Fatal(err)
		}
		return ok, out.String()
	}

	// Default chrome stays the approval form.
	ok, out := ask(Confirmation{Title: "t"}, "y\n")
	if !ok || !strings.Contains(out, "approve? [y/N] ") {
		t.Fatalf("default question wrong (ok=%v):\n%s", ok, out)
	}

	// A plain question replaces only the ask line; y/N chrome stays.
	ok, out = ask(Confirmation{Title: "t", Question: "enable auto memory?"}, "n\n")
	if ok || !strings.Contains(out, "enable auto memory? [y/N] ") || strings.Contains(out, "approve?") {
		t.Fatalf("custom question wrong (ok=%v):\n%s", ok, out)
	}
}

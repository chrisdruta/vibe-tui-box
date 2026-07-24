package terminal

import (
	"testing"
	"unicode"
)

// controlFree fails the test if any rendered line still carries a
// control or format rune — the encoder's whole contract.
func controlFree(t *testing.T, lines []string) {
	t.Helper()
	for _, line := range lines {
		for _, r := range line {
			if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || unicode.Is(unicode.Cf, r) {
				t.Fatalf("control rune %U survived encoding: %q", r, line)
			}
		}
	}
}

func FuzzEncode(f *testing.F) {
	f.Add("plain text", 80, 10)
	f.Add("evil\x1b[2J\x9bclear\u202e bidi", 40, 5)
	f.Add("tabs\tand\r\nnewlines\rmixed", 0, 0)
	f.Add("\x00\x07\x08", 1, 1)

	f.Fuzz(func(t *testing.T, s string, width, lines int) {
		if width > 1<<12 || lines > 1<<12 || width < -1 || lines < -1 {
			return // keep the harness itself bounded
		}
		limits := Limits{MaxWidth: width, MaxLines: lines}
		enc := Encode(s, limits)
		eff := limits.withDefaults()
		if len(enc.Lines) > eff.MaxLines {
			t.Fatalf("line cap broke: %d > %d", len(enc.Lines), eff.MaxLines)
		}
		controlFree(t, enc.Lines)
	})
}

func FuzzDiff(f *testing.F) {
	f.Add("a\nb\nc\n", "a\nX\nc\n", 40, 80)
	f.Add("", "evil\x1b[2J\n", 0, 0)
	f.Add("same\n", "same\n", 10, 10)

	f.Fuzz(func(t *testing.T, before, after string, width, lines int) {
		if width > 1<<12 || lines > 1<<12 || width < -1 || lines < -1 {
			return
		}
		if len(before) > 1<<16 || len(after) > 1<<16 {
			return
		}
		limits := DiffLimits{MaxWidth: width, MaxLines: lines}
		d := Diff(before, after, limits)
		eff := limits.withDefaults()
		if len(d.Lines) > eff.MaxLines {
			t.Fatalf("diff line cap broke: %d > %d", len(d.Lines), eff.MaxLines)
		}
		controlFree(t, d.Lines)
		if before == after && len(d.Lines) != 0 {
			t.Fatalf("identical inputs produced a diff: %q", d.Lines)
		}
	})
}

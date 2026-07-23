// Package terminal renders untrusted text safely: control characters
// become visible notation, width and line counts are bounded, and
// callers keep chrome separate from encoded content so agent-authored
// text can never masquerade as interface elements.
package terminal

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Limits bound one encoded block. Zero fields take defaults.
type Limits struct {
	MaxWidth int // runes per line
	MaxLines int
}

func (l Limits) withDefaults() Limits {
	if l.MaxWidth <= 0 {
		l.MaxWidth = 120
	}
	if l.MaxLines <= 0 {
		l.MaxLines = 40
	}
	return l
}

// Encoded is sanitized, bounded text ready to print.
type Encoded struct {
	Lines     []string
	Truncated bool
}

// Encode sanitizes untrusted text: CRLF/CR normalize to LF, tabs become
// spaces, every other control character (including ANSI escapes) is
// replaced with its visible ⟨U+XXXX⟩ notation, lines are wrapped hard at
// the width limit, and the line count is capped.
func Encode(s string, limits Limits) Encoded {
	limits = limits.withDefaults()
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	var out Encoded
	for line := range strings.SplitSeq(s, "\n") {
		for _, wrapped := range wrap(sanitizeLine(line), limits.MaxWidth) {
			if len(out.Lines) >= limits.MaxLines {
				out.Truncated = true
				return out
			}
			out.Lines = append(out.Lines, wrapped)
		}
	}
	return out
}

// Line sanitizes one line to a single bounded row — the form used for
// statuslines and sidebar cells.
func Line(s string, maxWidth int) string {
	enc := Encode(s, Limits{MaxWidth: maxWidth, MaxLines: 1})
	if len(enc.Lines) == 0 {
		return ""
	}
	if enc.Truncated {
		return truncateRunes(enc.Lines[0], maxWidth-1) + "…"
	}
	return enc.Lines[0]
}

func sanitizeLine(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\t':
			b.WriteString("    ")
		case r == utf8.RuneError:
			b.WriteString("�")
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			fmt.Fprintf(&b, "⟨U+%04X⟩", r)
		case unicode.Is(unicode.Cf, r):
			// Format characters (bidi controls, ZWJ abuse) become visible.
			fmt.Fprintf(&b, "⟨U+%04X⟩", r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func wrap(s string, width int) []string {
	if s == "" {
		return []string{""}
	}
	var lines []string
	runes := []rune(s)
	for len(runes) > width {
		lines = append(lines, string(runes[:width]))
		runes = runes[width:]
	}
	return append(lines, string(runes))
}

func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

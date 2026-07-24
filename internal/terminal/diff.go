package terminal

import (
	"fmt"
	"strings"
)

// DiffLimits bound one rendered diff. Zero fields take defaults.
type DiffLimits struct {
	MaxLines      int // rendered output lines
	MaxWidth      int // runes per line, ellipsis-truncated (diffs never wrap)
	MaxInputLines int // per side; larger inputs degrade to a size note
}

func (l DiffLimits) withDefaults() DiffLimits {
	if l.MaxLines <= 0 {
		l.MaxLines = 80
	}
	if l.MaxWidth <= 0 {
		l.MaxWidth = 120
	}
	// Below marker prefix + ellipsis a width cannot render anything.
	if l.MaxWidth < 8 {
		l.MaxWidth = 8
	}
	if l.MaxInputLines <= 0 {
		l.MaxInputLines = 4000
	}
	return l
}

// diffContext is how many unchanged lines frame each hunk.
const diffContext = 2

// Diff renders a bounded line diff of two texts, sanitized like every
// other untrusted output: "-"/"+" mark removed/added lines, unchanged
// runs collapse to a count marker, and identical inputs yield zero
// lines. Both sides count as untrusted; every content line passes
// through the same encoder as request text.
func Diff(before, after string, limits DiffLimits) Encoded {
	limits = limits.withDefaults()
	a := splitDiffLines(before)
	b := splitDiffLines(after)
	if len(a) > limits.MaxInputLines || len(b) > limits.MaxInputLines {
		return Encoded{
			Lines:     []string{fmt.Sprintf("(too large to diff: %d → %d lines)", len(a), len(b))},
			Truncated: true,
		}
	}

	// Trim the common frame; changes in canonical plans are localized.
	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(a)-prefix && suffix < len(b)-prefix &&
		a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}
	middle := diffOps(a[prefix:len(a)-suffix], b[prefix:len(b)-suffix])
	if len(middle) == 0 {
		return Encoded{}
	}

	// Rebuild the full aligned sequence so hunk context can come from
	// the trimmed frame too.
	ops := make([]op, 0, prefix+len(middle)+suffix)
	for _, l := range a[:prefix] {
		ops = append(ops, op{' ', l})
	}
	ops = append(ops, middle...)
	for _, l := range a[len(a)-suffix:] {
		ops = append(ops, op{' ', l})
	}
	return renderHunks(ops, limits)
}

// op is one aligned step through both sequences.
type op struct {
	kind byte // ' ' keep, '-' delete, '+' insert
	line string
}

// diffOps aligns two changed middles. Small inputs get an LCS
// alignment; oversized products degrade to delete-all/insert-all, which
// stays correct and bounded.
func diffOps(a, b []string) []op {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	const maxCells = 1 << 20
	if len(a)*len(b) > maxCells {
		ops := make([]op, 0, len(a)+len(b))
		for _, l := range a {
			ops = append(ops, op{'-', l})
		}
		for _, l := range b {
			ops = append(ops, op{'+', l})
		}
		return ops
	}
	n, m := len(a), len(b)
	table := make([]int32, (n+1)*(m+1))
	idx := func(i, j int) int { return i*(m+1) + j }
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				table[idx(i, j)] = table[idx(i+1, j+1)] + 1
			} else {
				table[idx(i, j)] = max(table[idx(i+1, j)], table[idx(i, j+1)])
			}
		}
	}
	var ops []op
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, op{' ', a[i]})
			i++
			j++
		case table[idx(i+1, j)] >= table[idx(i, j+1)]:
			ops = append(ops, op{'-', a[i]})
			i++
		default:
			ops = append(ops, op{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, op{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, op{'+', b[j]})
	}
	return ops
}

// renderHunks emits framed hunks with bounded context; every unchanged
// run longer than the context window collapses to a count line.
func renderHunks(ops []op, limits DiffLimits) Encoded {
	changed := make([]int, 0, len(ops))
	for i, o := range ops {
		if o.kind != ' ' {
			changed = append(changed, i)
		}
	}
	if len(changed) == 0 {
		return Encoded{}
	}

	var out Encoded
	emit := func(line string) bool {
		if len(out.Lines) >= limits.MaxLines {
			out.Truncated = true
			return false
		}
		out.Lines = append(out.Lines, line)
		return true
	}

	last := -1 // last emitted op index
	for c := 0; c < len(changed); {
		hunkStart := max(changed[c]-diffContext, last+1)
		end := changed[c]
		c++
		// Merge runs whose gap fits inside one context window.
		for c < len(changed) && changed[c]-end <= 2*diffContext+1 {
			end = changed[c]
			c++
		}
		hunkEnd := min(end+diffContext, len(ops)-1)
		if gap := hunkStart - (last + 1); gap > 0 {
			if !emit(fmt.Sprintf("··· %d unchanged line(s)", gap)) {
				return out
			}
		}
		for i := hunkStart; i <= hunkEnd; i++ {
			if !emit(renderOp(ops[i], limits.MaxWidth)) {
				return out
			}
		}
		last = hunkEnd
	}
	if gap := len(ops) - (last + 1); gap > 0 {
		emit(fmt.Sprintf("··· %d unchanged line(s)", gap))
	}
	return out
}

func renderOp(o op, maxWidth int) string {
	prefix := "  "
	switch o.kind {
	case '-':
		prefix = "- "
	case '+':
		prefix = "+ "
	}
	content := sanitizeLine(o.line)
	if width := maxWidth - 2; len([]rune(content)) > width {
		content = truncateRunes(content, width-1) + "…"
	}
	return prefix + content
}

// splitDiffLines splits without a trailing phantom line.
func splitDiffLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

// Package progressui renders pull/build progress on stderr. Builds
// draw as a live bill-of-materials block: the part rows announced
// before the build (or discovered per step for extension Dockerfiles)
// flip pending → building → cached/built in place, raw layer output is
// suppressed but ring-buffered, and a failure replays the failing
// step's tail. Pulls stay a single overwriting ticker line.
//
// The renderer assumes a terminal; the caller gates on TTY-ness (the
// non-TTY default is silence, --verbose is a raw stream — see Verbose).
package progressui

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"vibe/internal/dockerapi"
	"vibe/internal/terminal"
)

// tailLines is how much of the failing step's raw output a failure
// replays. The full stream is never persisted; --verbose exists for
// that.
const tailLines = 30

// State glyphs, shared vocabulary with the tmux sidebar's StateToken.
const (
	glyphPending  = "·"
	glyphBuilding = "◐"
	glyphCached   = "○"
	glyphBuilt    = "●"
	glyphFailed   = "✗"
)

const (
	ansiDim   = "\x1b[2m"
	ansiBold  = "\x1b[1m"
	ansiRed   = "\x1b[31m"
	ansiReset = "\x1b[0m"
	ansiEOL   = "\x1b[2K"
)

// row is one bill-of-materials line: an announced install part, or a
// step discovered from the stream on plan-less (extension) builds.
type row struct {
	title     string
	detail    string
	refresh   bool
	steps     int // expected step count; announced rows know it up front
	done      int // credited steps
	cached    int // credited steps satisfied from layer cache
	active    bool
	failed    bool
	started   time.Time
	completed time.Time
}

func (w *row) finished() bool { return w.steps > 0 && w.done >= w.steps }

// Renderer folds Progress events into terminal state. All entry points
// lock: events arrive from the operation's goroutine and repaints also
// come from the elapsed-time ticker.
type Renderer struct {
	mu    sync.Mutex
	out   io.Writer
	width func() int
	now   func() time.Time

	pending []row // build-plan announcements awaiting build-start

	tag       string
	rows      []*row
	index     map[string]*row
	cur       *row
	curCached bool
	building  bool
	begun     time.Time
	tail      []string
	painted   int // lines the live block occupies on screen
	lastPaint time.Time
	ticker    chan struct{}

	statusLine bool // an overwriting one-line ticker (pull/dev-build) is on screen
}

// New returns a renderer writing to out, asking width for the terminal
// width before each paint.
func New(out io.Writer, width func() int) *Renderer {
	return &Renderer{out: out, width: width, now: time.Now}
}

// Sink adapts the renderer to the dockerapi progress contract.
func (r *Renderer) Sink() dockerapi.ProgressSink { return r.handle }

func (r *Renderer) handle(p dockerapi.Progress) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch p.Stage {
	case dockerapi.StageBuildPlan:
		r.pending = append(r.pending, row{
			title:   p.Part,
			detail:  p.Detail,
			refresh: p.Refresh,
			steps:   int(p.Total),
		})
	case dockerapi.StageBuildStart:
		r.beginBuild(p.Message)
	case dockerapi.StageBuild:
		r.buildEvent(p)
	default:
		// Pulls and other stages: one overwriting status line.
		if p.Done {
			fmt.Fprintf(r.out, "\r%s%s: done\n", ansiEOL, r.clip(p.Message, 8))
			r.statusLine = false
			return
		}
		fmt.Fprintf(r.out, "\r%s%s", ansiEOL, r.clip(p.Message, 8))
		r.statusLine = true
	}
}

func (r *Renderer) beginBuild(tag string) {
	r.clearStatusLine()
	r.tag = tag
	r.rows = nil
	r.index = map[string]*row{}
	for i := range r.pending {
		w := r.pending[i]
		r.rows = append(r.rows, &w)
		r.index[w.title] = &w
	}
	r.pending = nil
	r.cur = nil
	r.building = true
	r.begun = r.now()
	r.tail = nil
	r.painted = 0
	r.paint()
	stop := make(chan struct{})
	r.ticker = stop
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				r.mu.Lock()
				if r.building {
					r.paint()
				}
				r.mu.Unlock()
			}
		}
	}()
}

func (r *Renderer) buildEvent(p dockerapi.Progress) {
	if !r.building && !p.Done && !p.Failed {
		return // stray events outside a block (defensive)
	}
	switch {
	case p.StepStart:
		r.credit()
		w := r.index[p.Part]
		if w == nil {
			// Plan-less build (extension Dockerfile): the instruction
			// itself is the row. Sanitized — it is project input.
			title := p.Part
			if title == "" {
				title = terminal.Line(p.Message, 48)
			}
			if w = r.index[title]; w == nil {
				w = &row{title: title, steps: 1}
				r.rows = append(r.rows, w)
				r.index[title] = w
			} else {
				w.steps++
			}
		}
		if r.cur != nil && r.cur != w {
			r.cur.active = false
		}
		if w.started.IsZero() {
			w.started = r.now()
		}
		w.active = true
		r.cur = w
		r.curCached = false
		r.tail = r.tail[:0]
		r.paint()
	case p.Cached:
		r.curCached = true
	case p.Failed:
		r.fail(p.Message)
	case p.Done:
		r.finish()
	default:
		if p.Message == "" {
			return
		}
		if len(r.tail) == tailLines {
			copy(r.tail, r.tail[1:])
			r.tail = r.tail[:tailLines-1]
		}
		r.tail = append(r.tail, p.Message)
		// Busy layers stream thousands of lines; repaint at most a few
		// times a second (the elapsed ticker covers quiet stretches).
		if r.now().Sub(r.lastPaint) > 250*time.Millisecond {
			r.paint()
		}
	}
}

// credit books the just-finished step onto its row. Steps complete
// when the next one starts (or the build ends); the daemon has no
// explicit step-done marker.
func (r *Renderer) credit() {
	if r.cur == nil {
		return
	}
	r.cur.done++
	if r.curCached {
		r.cur.cached++
	}
	if r.cur.finished() {
		r.cur.active = false
		r.cur.completed = r.now()
	}
}

func (r *Renderer) finish() {
	r.credit()
	r.stopTicker()
	for _, w := range r.rows {
		w.active = false
		if w.completed.IsZero() {
			w.completed = r.now()
		}
	}
	r.building = false
	r.paint()
	r.painted = 0 // the block is final; the next build starts below it
}

func (r *Renderer) fail(msg string) {
	r.stopTicker()
	if r.cur != nil {
		r.cur.failed = true
		r.cur.active = false
	}
	r.building = false
	r.paint()
	r.painted = 0
	width := r.width()
	if len(r.tail) > 0 {
		part := ""
		if r.cur != nil {
			part = " from " + r.cur.title
		}
		fmt.Fprintf(r.out, "%slast output%s:%s\n", ansiDim, part, ansiReset)
		for _, line := range r.tail {
			fmt.Fprintf(r.out, "  %s│%s %s\n", ansiDim, ansiReset, terminal.Line(line, max(20, width-4)))
		}
	}
	fmt.Fprintf(r.out, "%s%s%s\n", ansiRed, terminal.Line(msg, max(20, width)), ansiReset)
}

func (r *Renderer) stopTicker() {
	if r.ticker != nil {
		close(r.ticker)
		r.ticker = nil
	}
}

func (r *Renderer) clearStatusLine() {
	if r.statusLine {
		fmt.Fprintf(r.out, "\r%s", ansiEOL)
		r.statusLine = false
	}
}

// paint redraws the whole block in place: cursor up over the previous
// frame, then one cleared line per row. Lines are truncated to the
// terminal width before coloring, so a narrow terminal never wraps and
// never breaks the cursor arithmetic.
func (r *Renderer) paint() {
	width := max(40, r.width())
	lines := r.compose(width - 1)
	if r.painted > 0 {
		fmt.Fprintf(r.out, "\x1b[%dA", r.painted)
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString("\r")
		b.WriteString(ansiEOL)
		b.WriteString(l)
		b.WriteString("\n")
	}
	b.WriteString("\x1b[J")
	fmt.Fprint(r.out, b.String())
	r.painted = len(lines)
	r.lastPaint = r.now()
}

func (r *Renderer) compose(width int) []string {
	titleW, detailW := 0, 0
	for _, w := range r.rows {
		titleW = max(titleW, len([]rune(w.title)))
		detailW = max(detailW, len([]rune(w.detail)))
	}
	titleW = min(titleW, 48)
	detailW = min(detailW, 32)

	var lines []string
	head := "building " + r.tag
	if !r.building {
		cached, built, failed := 0, 0, false
		for _, w := range r.rows {
			switch {
			case w.failed:
				failed = true
			case w.done > 0 && w.cached == w.done:
				cached++
			case w.done > 0:
				built++
			}
		}
		if failed {
			head = "build failed " + r.tag
		} else {
			head = fmt.Sprintf("built %s in %s · %d cached · %d built",
				r.tag, durShort(r.now().Sub(r.begun)), cached, built)
		}
	}
	lines = append(lines, ansiBold+clipRunes(head, width)+ansiReset)
	for _, w := range r.rows {
		glyph, status, color := glyphPending, "", ansiDim
		switch {
		case w.failed:
			glyph, status, color = glyphFailed, "failed", ansiRed
		case w.active:
			glyph, color = glyphBuilding, ""
			status = "building " + durShort(r.now().Sub(w.started))
		case w.finished() && w.cached == w.steps:
			glyph, status, color = glyphCached, "cached", ""
		case w.finished():
			glyph, color = glyphBuilt, ""
			status = "built " + durShort(w.completed.Sub(w.started))
		case w.refresh:
			status = "will rebuild (refresh)"
		}
		line := fmt.Sprintf("  %s %-*s  %-*s  %s", glyph,
			titleW, clipRunes(w.title, titleW),
			detailW, clipRunes(w.detail, detailW), status)
		line = clipRunes(strings.TrimRight(line, " "), width)
		if color != "" {
			line = color + line + ansiReset
		}
		lines = append(lines, line)
	}
	return lines
}

func (r *Renderer) clip(s string, margin int) string {
	return clipRunes(s, max(20, r.width()-margin))
}

func clipRunes(s string, width int) string {
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	if width < 1 {
		return ""
	}
	return string(runes[:width-1]) + "…"
}

func durShort(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// Verbose is the --verbose sink: the raw build stream, unabridged and
// uncolored, on any output. Pull ticks collapse to their completions so
// a log file is not carpeted with byte counters.
func Verbose(out io.Writer) dockerapi.ProgressSink {
	return func(p dockerapi.Progress) {
		switch p.Stage {
		case dockerapi.StageBuild:
			switch {
			case p.Done:
				fmt.Fprintf(out, "built %s\n", p.Message)
			case p.StepStart:
				fmt.Fprintf(out, "Step %d/%d : %s\n", p.Step, p.Steps, p.Message)
			case p.Cached:
				fmt.Fprintln(out, " ---> Using cache")
			case p.Failed:
				fmt.Fprintf(out, "ERROR: %s\n", p.Message)
			case p.Message != "":
				fmt.Fprintln(out, p.Message)
			}
		case dockerapi.StageBuildStart:
			fmt.Fprintf(out, "building %s\n", p.Message)
		case dockerapi.StageBuildPlan:
			// The plan renders itself through the steps; skip.
		default:
			if p.Done {
				fmt.Fprintf(out, "%s: done\n", p.Message)
			}
		}
	}
}

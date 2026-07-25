package tmuxui

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/chrisdruta/vibe-tui-box/internal/terminal"
)

// The sidebar frame renderer: the layout arithmetic that used to live
// in sidebar.sh's frame(). The shell keeps the tmux mechanics — pane
// creation, the poll loop, the click dispatch, the background engine
// fetch — and pipes raw tmux porcelain here; this file owns budgets,
// truncation, the two-line records, the midpoint split, and the click
// map, so the drawn frame and the click targets are built by one
// function and tested as one function.

// FrameWindow is one stateful (or plain) tmux window of a session.
type FrameWindow struct {
	ID     string // tmux window id (@N)
	Name   string
	Glyph  string // empty = plain window (host shell, popup): no dot, no roster row
	DotHex string // dot color as #rrggbb (state-render.sh's @vibe_dot_fg)
	Attn   bool
	Active bool
	Model  string
}

// FrameSession is one tmux session on the vibe socket.
type FrameSession struct {
	ID      string // tmux session id ($N)
	Name    string // display name (@vibe_name, falling back to session_name)
	Path    string // session_path; the caller resolves Branch from it
	Branch  string
	Project string // full project ID (@vibe_project), join key for engine facts
	Windows []FrameWindow
}

// FrameInput is the prepared model for one sidebar frame.
type FrameInput struct {
	Width, Height int
	SelfSession   string // session id this sidebar's pane lives in
	Sessions      []FrameSession
	Fleet         []FleetEntry // engine truth from the fleet cache; may be empty
	Detail        []string     // own-project detail block from the detail cache
}

// FrameOutput is one rendered frame: the click map in the
// @vibe_sidebar_map format ("row:target" pairs, space-separated,
// 0-based mouse_y rows) and the ANSI body, newline-free (every row is
// absolutely positioned) so it transports as a single protocol line.
type FrameOutput struct {
	Map  string
	Body string
}

// FleetEntry is one parsed `vibe _fleet` line — the read twin of
// Fleet().
type FleetEntry struct {
	ID      string
	Token   string
	Mode    string
	Version string
	Pending int
	Name    string
}

// ParseFleet parses the `vibe _fleet` porcelain, skipping lines of an
// unknown protocol version or shape.
func ParseFleet(lines []string) []FleetEntry {
	var out []FleetEntry
	for _, line := range lines {
		f := strings.Split(line, fleetSep)
		if len(f) != 7 || f[0] != "1" || f[1] == "" {
			continue
		}
		pending, _ := strconv.Atoi(f[5])
		out = append(out, FleetEntry{
			ID: f[1], Token: f[2], Mode: f[3], Version: f[4],
			Pending: pending, Name: f[6],
		})
	}
	return out
}

// ParseFrameData parses the tmux porcelain the sidebar loop pipes to
// `vibe _frame`: US-separated, line-typed records.
//
//	G<US>width<US>height<US>self_session_id
//	S<US>session_id<US>name<US>path<US>project_id
//	W<US>session_id<US>glyph<US>dot_hex<US>attn<US>window_id<US>window_name<US>active<US>model
//
// Windows attach to their session by id; records for unknown sessions
// and malformed lines are dropped, never errors — a sidebar frame
// renders what it can.
func ParseFrameData(data string) FrameInput {
	in := FrameInput{Width: 30, Height: 24}
	index := map[string]int{}
	for line := range strings.SplitSeq(data, "\n") {
		f := strings.Split(line, fleetSep)
		switch {
		case f[0] == "G" && len(f) == 4:
			if w, err := strconv.Atoi(f[1]); err == nil && w > 0 {
				in.Width = w
			}
			if h, err := strconv.Atoi(f[2]); err == nil && h > 0 {
				in.Height = h
			}
			in.SelfSession = f[3]
		case f[0] == "S" && len(f) == 5 && f[1] != "":
			index[f[1]] = len(in.Sessions)
			in.Sessions = append(in.Sessions, FrameSession{
				ID: f[1], Name: f[2], Path: f[3], Project: f[4],
			})
		case f[0] == "W" && len(f) == 9 && f[1] != "":
			i, ok := index[f[1]]
			if !ok {
				continue
			}
			in.Sessions[i].Windows = append(in.Sessions[i].Windows, FrameWindow{
				Glyph:  f[2],
				DotHex: f[3],
				Attn:   f[4] == "1",
				ID:     f[5],
				Name:   f[6],
				Active: f[7] == "1",
				Model:  f[8],
			})
		}
	}
	return in
}

// ANSI building blocks. Colors come from the one palette in theme.go —
// the same source theme.sh and the conf's @thm block render from.
const (
	ansiBold  = "\x1b[1m"
	ansiReset = "\x1b[0m"
	ansiEOL   = "\x1b[K" // clear to end of line
)

var hexColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// fg renders a truecolor foreground escape; anything but #rrggbb (a
// missing or mangled tmux option) falls back to the dim palette color.
func fg(hex string) string {
	if !hexColorRe.MatchString(hex) {
		hex = PaletteHex("dim")
	}
	r, _ := strconv.ParseUint(hex[1:3], 16, 8)
	g, _ := strconv.ParseUint(hex[3:5], 16, 8)
	b, _ := strconv.ParseUint(hex[5:7], 16, 8)
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b)
}

// frameCanvas accumulates absolutely-positioned rows plus the click
// map. One appender advances the buffer and the map together, so the
// drawn frame and the click targets cannot skew.
type frameCanvas struct {
	body strings.Builder
	maps []string
}

// putAt draws content at the 0-based row (map keys are 0-based mouse_y
// rows); an empty target means the row is not clickable.
func (c *frameCanvas) putAt(row int, content, target string) {
	fmt.Fprintf(&c.body, "\x1b[%d;1H%s%s", row+1, content, ansiEOL)
	if target != "" {
		c.maps = append(c.maps, fmt.Sprintf("%d:%s", row, target))
	}
}

// rosterEntry is one aggregate agent-roster record: a name line and a
// dim detail line sharing one click target.
type rosterEntry struct {
	nameLine   string
	detailLine string
	target     string // "SESSION:WINDOW", the click mode's jump format
}

// Frame renders one sidebar frame. Layout contract (tui-layout.md):
// row 0 stays blank; the fleet section flows from row 1 — per session
// a name row with state dots, a branch row, engine facts or the own
// project's detail block, and a blank slop row, all claiming the
// session as click target; cold registered projects (fleet entries
// with no live session) render dim and unclickable. The aggregate
// agent roster starts at the pane midpoint (pushed down by a long
// fleet), under a ruled header, as two-line entries with a gap row;
// when only part fits, the last slot becomes an overflow count.
func Frame(in FrameInput) FrameOutput {
	var (
		cFG     = fg(PaletteHex("fg"))
		cDim    = fg(PaletteHex("dim"))
		cCoral  = fg(PaletteHex("coral"))
		cBorder = fg(PaletteHex("border"))
	)
	// Text budget: 2-col left gutter, keep 1 clear on the right.
	max := in.Width - 3
	if max < 8 {
		max = 8
	}

	sessions := append([]FrameSession(nil), in.Sessions...)
	sort.SliceStable(sessions, func(i, j int) bool { return sessions[i].Name < sessions[j].Name })

	fleetByID := make(map[string]FleetEntry, len(in.Fleet))
	for _, f := range in.Fleet {
		fleetByID[f.ID] = f
	}

	var c frameCanvas
	c.body.WriteString("\x1b[H\x1b[K") // home; row 0 stays blank
	row := 1
	var roster []rosterEntry
	seen := map[string]bool{}
	for _, s := range sessions {
		self := s.ID == in.SelfSession
		// Dots ride the name row in window order, same semantics as the
		// tabs: attention renders coral (the tab-blend @vibe_dot_fg would
		// vanish here), plain windows emit nothing. The same pass
		// collects the aggregate roster.
		var dots strings.Builder
		ndots := 0
		for _, w := range s.Windows {
			if w.Glyph == "" {
				continue
			}
			ndots++
			dotc := fg(w.DotHex)
			if w.Attn {
				dotc = cCoral
			}
			dots.WriteString(" ")
			dots.WriteString(dotc)
			dots.WriteString(w.Glyph)

			amark, acol := " ", cFG
			if self && w.Active {
				amark, acol = cCoral+"▍", ansiBold+cFG
			}
			nbudget := max - 4
			if nbudget < 8 {
				nbudget = 8
			}
			det := s.Name
			if w.Model != "" {
				det = w.Model + " · " + s.Name
			}
			roster = append(roster, rosterEntry{
				nameLine:   amark + ansiReset + " " + dotc + w.Glyph + ansiReset + " " + acol + terminal.Line(w.Name, nbudget) + ansiReset,
				detailLine: "   " + cDim + terminal.Line(det, max-3) + ansiReset,
				target:     s.ID + ":" + w.ID,
			})
		}
		mark, nameC := " ", cDim
		if self {
			mark, nameC = cCoral+"▍", cFG
		}
		// The dots shrink the name budget (2 cols each) so a long name
		// can never push them onto a wrapped row and skew the click map.
		nmax := max - ndots*2
		if nmax < 8 {
			nmax = 8
		}
		c.putAt(row, mark+ansiReset+" "+ansiBold+nameC+terminal.Line(s.Name, nmax)+ansiReset+dots.String()+ansiReset, s.ID)
		row++
		if s.Branch != "" {
			c.putAt(row, "   "+cDim+"⎇ "+terminal.Line(s.Branch, max-2)+ansiReset, s.ID)
			row++
		}
		// Engine truth, cache-only. The own session gets its detail
		// block; other sessions (and the own one while its detail cache
		// hasn't landed) get one dim facts line when anything is
		// interesting. All rows keep the session as click slop.
		if s.Project != "" {
			seen[s.Project] = true
		}
		if self && s.Project != "" && len(in.Detail) > 0 {
			for _, dline := range in.Detail {
				if dline == "" {
					continue
				}
				c.putAt(row, " "+cDim+terminal.Line(dline, max-1)+ansiReset, s.ID)
				row++
			}
		} else if f, ok := fleetByID[s.Project]; ok && s.Project != "" {
			var parts []string
			switch f.Token {
			case string(StateStale):
				parts = append(parts, string(StateStale)+" stale")
			case string(StateStopped):
				parts = append(parts, string(StateStopped)+" stopped")
			}
			if f.Pending > 0 {
				parts = append(parts, fmt.Sprintf("▲%d", f.Pending))
			}
			if f.Mode == "dev" {
				parts = append(parts, "dev")
			}
			if len(parts) > 0 {
				c.putAt(row, "   "+cDim+strings.Join(parts, " · ")+ansiReset, s.ID)
				row++
			}
		}
		c.putAt(row, "", s.ID) // blank slop row
		row++
	}
	// Cold registered projects — fleet entries with no live session.
	// Render-only dim rows: click dispatching `up` is a recorded product
	// call, not half-shipped here.
	for _, f := range in.Fleet {
		if seen[f.ID] {
			continue
		}
		c.putAt(row, " "+cDim+"· "+terminal.Line(f.Name, max-2)+ansiReset, "")
		row++
	}
	// Clear everything below the fleet section before the roster draws.
	fmt.Fprintf(&c.body, "\x1b[%d;1H\x1b[J", row+1)

	// The aggregate agent roster, from the pane midpoint: projects own
	// the top half, agents the bottom — a fleet past the midpoint pushes
	// the roster down instead of overlapping it.
	minStart := row + 1
	start := in.Height / 2
	if start < minStart {
		start = minStart
	}
	avail := in.Height - start - 1
	// Each entry is name + detail + gap, with no trailing gap.
	nShow := min((avail+1)/3, len(roster))
	if len(roster) > 0 && nShow >= 1 {
		// The header wears the same ruled dress as the pane border's
		// "projects" title above it.
		fill := max - 9
		if fill < 0 {
			fill = 0
		}
		c.putAt(start, cBorder+"─"+ansiReset+" "+cDim+"agents"+ansiReset+" "+cBorder+strings.Repeat("─", fill)+ansiReset, "")
		r := start + 1
		overflow := len(roster) - nShow
		for i, e := range roster[:nShow] {
			if overflow > 0 && i == nShow-1 {
				c.putAt(r, "   "+cDim+fmt.Sprintf("… +%d more", overflow+1)+ansiReset, "")
				break
			}
			c.putAt(r, e.nameLine, e.target)
			c.putAt(r+1, e.detailLine, e.target)
			if i < nShow-1 {
				c.putAt(r+2, "", e.target) // gap row, same click slop
			}
			r += 3
		}
	}
	c.body.WriteString("\x1b[H") // park the cursor; a trailing newline cannot scroll
	return FrameOutput{Map: strings.Join(c.maps, " "), Body: c.body.String()}
}

// Package tmuxui builds and renders the text the tmux UI displays.
// Views are pure functions over prepared view models: no registry,
// Docker, or tmux calls happen here, and untrusted text is sanitized
// through the terminal encoder before it reaches a renderer.
package tmuxui

import (
	"fmt"
	"strings"

	"github.com/chrisdruta/vibe-tui-box/internal/terminal"
)

// ProjectView is the prepared model for one project. (StateToken and
// the palette live in theme.go — the single source for colors/glyphs.)
type ProjectView struct {
	ID         string // full project ID, the join key for tmux @vibe_project
	Name       string
	Mode       string // "release" or "dev"
	Version    string
	Containers []ContainerView
	Pending    int // broker requests awaiting a decision
}

type ContainerView struct {
	Role    string
	Running bool
	InSync  bool
}

// Token summarizes the project into one state glyph.
func (v ProjectView) Token() StateToken {
	if len(v.Containers) == 0 {
		return StateNone
	}
	stale := false
	for _, c := range v.Containers {
		if !c.Running {
			return StateStopped
		}
		if !c.InSync {
			stale = true
		}
	}
	if stale {
		return StateStale
	}
	return StateRunning
}

// State renders the display line for `vibe _state`. Its one consumer
// is the status bar, which splices the output verbatim — so this is
// display form, not protocol (the old version-prefixed line leaked
// "1 ● 2" into the bar): the state glyph, then ▲n only while requests
// are pending.
func State(v ProjectView) string {
	if v.Pending > 0 {
		return fmt.Sprintf("%s ▲%d", v.Token(), v.Pending)
	}
	return string(v.Token())
}

// Sidebar renders one project's detail block for `vibe _sidebar`: the
// display lines the shell sidebar nests under a project's name row (the
// name itself stays bash-drawn, so it never appears here). Display form
// (docs/tui-layout.md, detail display form): one line per container —
// StateToken glyph + role, the engine version riding the first line
// (`dev-` hashes stripped to their first 8, release versions as-is) —
// and a short pending row. The frame renderer draws the whole block
// dim, so the glyphs carry shape, not color. Roles and versions are
// semi-trusted and go through the encoder like everything else.
func Sidebar(v ProjectView, width int) []string {
	if width <= 0 {
		width = 40
	}
	ver := v.Version
	if rest, ok := strings.CutPrefix(ver, "dev-"); ok {
		if len(rest) > 8 {
			rest = rest[:8]
		}
		ver = rest
	}
	var lines []string
	for i, c := range v.Containers {
		glyph := StateRunning
		switch {
		case !c.Running:
			glyph = StateStopped
		case !c.InSync:
			glyph = StateStale
		}
		line := fmt.Sprintf("  %s %s", glyph, c.Role)
		if i == 0 && ver != "" {
			line += " · " + ver
		}
		lines = append(lines, terminal.Line(line, width))
	}
	// No containers yet: one dim facts line so the block never renders
	// empty under a registered project's name.
	if len(lines) == 0 && (v.Mode != "" || ver != "") {
		line := strings.TrimRight(fmt.Sprintf("  %s %s %s", StateNone, v.Mode, ver), " ")
		lines = append(lines, terminal.Line(line, width))
	}
	if v.Pending > 0 {
		lines = append(lines, terminal.Line(fmt.Sprintf("  ▲ %d pending", v.Pending), width))
	}
	return lines
}

// fleetSep separates `vibe _fleet` fields: US (0x1f) survives tmux
// option round-trips and `read` word-splitting where tabs collapse.
// sidebar.sh parses on the same byte.
const fleetSep = "\x1f"

// Fleet renders the `vibe _fleet` porcelain, one project per line:
//
//	1<US>id<US>token<US>mode<US>version<US>pending<US>display-name
//
// The leading field is the protocol version. The display name comes
// last because it is the only free-text field; it is sanitized, and
// consumers re-truncate for display, so the width budget only bounds
// pathological names. No projects renders no lines.
func Fleet(views []ProjectView, width int) []string {
	if width <= 0 {
		width = 80
	}
	lines := make([]string, 0, len(views))
	for _, v := range views {
		lines = append(lines, fmt.Sprintf("1%s%s%s%s%s%s%s%s%s%d%s%s",
			fleetSep, v.ID,
			fleetSep, v.Token(),
			fleetSep, v.Mode,
			fleetSep, v.Version,
			fleetSep, v.Pending,
			fleetSep, terminal.Line(v.Name, width)))
	}
	return lines
}

// Package tmuxui builds and renders the text the tmux UI displays.
// Views are pure functions over prepared view models: no registry,
// Docker, or tmux calls happen here, and untrusted text is sanitized
// through the terminal encoder before it reaches a renderer.
package tmuxui

import (
	"fmt"

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
// name itself stays bash-drawn, so it never appears here). Roles and
// versions are semi-trusted and go through the encoder like everything
// else.
func Sidebar(v ProjectView, width int) []string {
	if width <= 0 {
		width = 40
	}
	lines := []string{
		terminal.Line(fmt.Sprintf("  %s %s", v.Mode, v.Version), width),
	}
	for _, c := range v.Containers {
		state := "stopped"
		if c.Running {
			state = "running"
		}
		suffix := ""
		if !c.InSync {
			suffix = " (stale)"
		}
		lines = append(lines, terminal.Line(fmt.Sprintf("  %-12s %s%s", c.Role, state, suffix), width))
	}
	if v.Pending > 0 {
		lines = append(lines, terminal.Line(fmt.Sprintf("  ▲ %d request(s) pending — vibe request list", v.Pending), width))
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

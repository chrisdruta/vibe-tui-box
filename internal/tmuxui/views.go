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

// StateToken is the compact one-glyph project state for status lines.
type StateToken string

const (
	StateRunning StateToken = "●"
	StateStopped StateToken = "○"
	StateStale   StateToken = "◐"
	StateNone    StateToken = "·"
)

// ProjectView is the prepared model for one project.
type ProjectView struct {
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

// State renders the protocol line for `vibe _state`:
//
//	1 <token> <attention-count>
//
// The leading field is the protocol version.
func State(v ProjectView) string {
	return fmt.Sprintf("1 %s %d", v.Token(), v.Pending)
}

// Sidebar renders the project pane summary within a width budget.
func Sidebar(v ProjectView, width int) []string {
	if width <= 0 {
		width = 40
	}
	lines := []string{
		terminal.Line(fmt.Sprintf("%s %s", v.Token(), v.Name), width),
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

// Fleet renders one line per project for `vibe _fleet`.
func Fleet(views []ProjectView, width int) []string {
	if width <= 0 {
		width = 80
	}
	if len(views) == 0 {
		return []string{"no registered projects"}
	}
	lines := make([]string, 0, len(views))
	for _, v := range views {
		attention := ""
		if v.Pending > 0 {
			attention = fmt.Sprintf(" ▲%d", v.Pending)
		}
		lines = append(lines, terminal.Line(
			fmt.Sprintf("%s %-24s %-8s %s%s", v.Token(), v.Name, v.Mode, v.Version, attention), width))
	}
	return lines
}

// Statusline renders the single agent status row. Agent-supplied text
// is untrusted and is sanitized to one bounded cell.
func Statusline(v ProjectView, agent, message string, width int) string {
	if width <= 0 {
		width = 80
	}
	head := fmt.Sprintf("%s %s · %s", v.Token(), v.Name, agent)
	budget := width - len([]rune(head)) - 3
	if budget < 8 || message == "" {
		return terminal.Line(head, width)
	}
	return terminal.Line(head+" · "+strings.TrimSpace(terminal.Line(message, budget)), width)
}

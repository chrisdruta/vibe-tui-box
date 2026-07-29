// Package tmuxui builds and renders the text the tmux UI displays.
// Views are pure functions over prepared view models: no registry,
// Docker, or tmux calls happen here, and untrusted text is sanitized
// through the terminal encoder before it reaches a renderer.
package tmuxui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/chrisdruta/vibe-tui-box/internal/terminal"
)

// sessionNameRe is the shared inner-session charset (tmux session
// names, state-file names, the title channel, and — since the tray's
// ghost cells — mouse-range names).
var sessionNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ProjectView is the prepared model for one project. (StateToken and
// the palette live in theme.go — the single source for colors/glyphs.)
type ProjectView struct {
	ID         string // full project ID, the join key for tmux @vibe_project
	Name       string
	Mode       string // "release" or "dev"
	Version    string
	Containers []ContainerView
	Pending    int // broker requests awaiting a decision
	// Churn is the working-tree churn line ("+128 −40") the branch row
	// wears (docs/tui-layout.md "Signal density") — computed on the
	// fetch path (one `git diff --shortstat` per in-use project), empty
	// for a clean tree, a cold project, or no git at all.
	Churn string
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

// Sidebar renders one project's detail for `vibe _sidebar`: the ONE
// compact engine-facts line the frame merges into a project block's
// meta row (docs/tui-layout.md, the meta line — 2026-07-26,
// supersedes the multi-line detail block: three near-identical
// indented rows read as mush, and the detail's ● collided with the
// roster's agents-only dot). Segments join with ` · `: one per
// container — bare role when nominal (absence of a glyph IS the
// nominal signal), ◐/○ prefixed when stale/stopped — with the engine
// version riding the first (`dev-` hashes stripped to their first 8,
// release versions as-is), then `▲n` for pending. The frame draws the
// line dim; the name row above it stays bash-drawn and never appears
// here. Roles and versions are semi-trusted and go through the
// encoder like everything else.
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
	var segs []string
	for i, c := range v.Containers {
		seg := c.Role
		switch {
		case !c.Running:
			seg = string(StateStopped) + " " + seg
		case !c.InSync:
			seg = string(StateStale) + " " + seg
		}
		if i == 0 && ver != "" {
			seg += " " + ver
		}
		segs = append(segs, seg)
	}
	// No containers yet: mode + version keep the line from rendering
	// empty under a registered project's name.
	if len(segs) == 0 && (v.Mode != "" || ver != "") {
		segs = append(segs, strings.TrimSpace(v.Mode+" "+ver))
	}
	if v.Pending > 0 {
		segs = append(segs, fmt.Sprintf("▲%d", v.Pending))
	}
	if len(segs) == 0 {
		return nil
	}
	return []string{terminal.Line(strings.Join(segs, " · "), width)}
}

// fleetSep separates `vibe _fleet` fields: US (0x1f) survives tmux
// option round-trips and `read` word-splitting where tabs collapse.
// sidebar.sh parses on the same byte.
const fleetSep = "\x1f"

// AgentEntry is one container-side agent session as the `vibe ps` join
// reports it, keyed to the project that owns it. It is both the write
// model for Agents() and the read model ParseAgents() answers — one
// record, one grammar (unlike the fleet, whose write side is a whole
// ProjectView).
// AgentEntryKindService marks an AgentEntry as a workspace service
// (docs/tui-layout.md "Workspace services"): Session is then the svc.sh
// WINDOW name inside the container's `services` session, not an
// attachable address of its own.
const AgentEntryKindService = "svc"

type AgentEntry struct {
	Project string // full project ID, the join key for tmux @vibe_project
	Session string // inner tmux session name — the ADDRESS `vibe attach` takes
	State   string // agent-session state vocabulary (theme.go AgentStates)
	CLI     string // the CLI actually running at that address
	Model   string
	Epoch   int64  // unix epoch the state was entered; 0 unknown
	Detail  string // the feeder's free-text qualifier ("detached", …)
	Kind    string // "" = agent session; AgentEntryKindService = svc window
}

// Agents renders the `vibe _agents` porcelain, one container-side agent
// session or workspace-service window per line:
//
//	2<US>project<US>session<US>state<US>cli<US>model<US>epoch<US>detail[<US>kind]
//
// The leading field is the protocol version — bumped to 2 when epoch
// and detail joined the row (the signal-density pass); the trailing
// kind is omitted for agent rows, `svc` for workspace services.
// Sessions are addresses (or svc window names sharing the same closed
// charset): a name that could not survive a tmux target, a state-file
// name, or a mouse-range name is dropped outright rather than escaped
// — every consumer of this porcelain turns the session into one of
// those. CLI, model, and detail are container-fed free text, sanitized
// and bounded here; an unknown epoch writes empty.
func Agents(entries []AgentEntry, width int) []string {
	if width <= 0 {
		width = 80
	}
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Project == "" || !sessionNameRe.MatchString(e.Session) {
			continue
		}
		epoch := ""
		if e.Epoch > 0 {
			epoch = strconv.FormatInt(e.Epoch, 10)
		}
		line := fmt.Sprintf("2%s%s%s%s%s%s%s%s%s%s%s%s%s%s",
			fleetSep, e.Project,
			fleetSep, e.Session,
			fleetSep, terminal.Line(e.State, 24),
			fleetSep, terminal.Line(e.CLI, width),
			fleetSep, terminal.Line(e.Model, width),
			fleetSep, epoch,
			fleetSep, terminal.Line(e.Detail, width))
		if e.Kind == AgentEntryKindService {
			line += fleetSep + AgentEntryKindService
		}
		lines = append(lines, line)
	}
	return lines
}

// Fleet renders the `vibe _fleet` porcelain, one project per line:
//
//	2<US>id<US>token<US>mode<US>version<US>pending<US>churn<US>display-name
//
// The leading field is the protocol version — bumped to 2 when churn
// joined the row (the signal-density pass). The display name comes
// last because it is the only free-text field; it is sanitized, and
// consumers re-truncate for display, so the width budget only bounds
// pathological names. No projects renders no lines.
func Fleet(views []ProjectView, width int) []string {
	if width <= 0 {
		width = 80
	}
	lines := make([]string, 0, len(views))
	for _, v := range views {
		lines = append(lines, fmt.Sprintf("2%s%s%s%s%s%s%s%s%s%d%s%s%s%s",
			fleetSep, v.ID,
			fleetSep, v.Token(),
			fleetSep, v.Mode,
			fleetSep, v.Version,
			fleetSep, v.Pending,
			fleetSep, terminal.Line(v.Churn, 24),
			fleetSep, terminal.Line(v.Name, width)))
	}
	return lines
}

// CompactAge renders a duration in seconds as the sidebar's and
// `vibe ps`'s compact age ("42m"/"3h"/"2d") — one rendering for every
// surface that shows time-in-state.
func CompactAge(s int64) string {
	if s < 0 {
		s = 0
	}
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm", s/60)
	case s < 86400:
		return fmt.Sprintf("%dh", s/3600)
	default:
		return fmt.Sprintf("%dd", s/86400)
	}
}

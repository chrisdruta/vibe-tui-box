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

	"vibe/internal/terminal"
)

// SessionNameRe is the shared inner-session charset (tmux session
// names, state-file names, the title channel, and — since the tray's
// ghost cells — mouse-range names).
var SessionNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

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

// Token summarizes the project into one state glyph. The sidecar rule,
// declared once (2026-08-04 — three surfaces treated one Docker fact
// three ways): sidecar containers AGGREGATE here (a dead dns degrades
// the project token — it is part of the project's health), render as
// ROWS in the services tree (the `_agents` sidecar kind, SidecarStyle
// glyphs), and stay OUT of the detail line's segments (Sidebar below —
// `sidecar:d…` was the line's ragged clip magnet). Prose for a token
// comes from TokenWord (theme.go), never hand-rolled per surface.
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

// State renders the display cell for `vibe _state`. Its consumers are
// tmux status lines, which splice the output verbatim — so this is
// display form, not protocol (the old version-prefixed line leaked
// "1 ● 2" into the bar). Nominal renders EMPTY (2026-07-31, Chris —
// the always-on ● beside the clock's dim `·` read as "two dots"; the
// bar now extends the sidebar's own rule that absence of a glyph IS
// the nominal signal): only ◐/○ and ▲n appear, and a non-empty cell
// carries its own trailing dim ` · ` separator toward the clock — a
// tmux format cannot test #() emptiness, so the separator must live
// and die with the cell.
func State(v ProjectView) string {
	cell := ""
	switch tok := v.Token(); tok {
	case StateStale, StateStopped:
		cell = string(tok)
	}
	if v.Pending > 0 {
		if cell != "" {
			cell += " "
		}
		cell += fmt.Sprintf("▲%d", v.Pending)
	}
	if cell == "" {
		return ""
	}
	return cell + "#[fg=" + PaletteHex("dim") + "] · #[default]"
}

// Sidebar renders one project's detail for `vibe _sidebar`: the ONE
// compact engine-facts line the frame merges into a project block's
// meta row (docs/tui-layout.md, the meta line — 2026-07-26,
// supersedes the multi-line detail block: three near-identical
// indented rows read as mush, and the detail's ● collided with the
// roster's agents-only dot). Segments join with ` · `: ◐/○-prefixed
// roles for stale/stopped containers ONLY — a nominal container
// contributes nothing (2026-08-10, Chris: bare roles and the version
// made the self block one row taller than its fleet rendering, so
// every project switch jumped the rows below; absence IS the nominal
// signal here too now, and the version moved to the tray's brand
// cell — DisplayVersion) — then `▲n` for pending and the `dev` chip,
// mirroring the fleet meta exactly so self and non-self blocks keep
// the same height for the same state. `sidecar:*` containers are NOT
// segments (2026-07-31, Chris — `sidecar:d…` was the line's ragged
// clip magnet): they render as rows in the services tree instead
// (frame.go, the sidecar rows), fed by the `_agents` porcelain's
// sidecar kind. The frame draws the line dim; the name row above it
// stays bash-drawn and never appears here. Roles are semi-trusted and
// go through the encoder like everything else.
func Sidebar(v ProjectView, width int) []string {
	if width <= 0 {
		width = 40
	}
	var segs []string
	for _, c := range v.Containers {
		if strings.HasPrefix(c.Role, "sidecar:") {
			continue
		}
		switch {
		case !c.Running:
			segs = append(segs, string(StateStopped)+" "+c.Role)
		case !c.InSync:
			segs = append(segs, string(StateStale)+" "+c.Role)
		}
	}
	if v.Pending > 0 {
		segs = append(segs, fmt.Sprintf("▲%d", v.Pending))
	}
	if v.Mode == "dev" {
		segs = append(segs, "dev")
	}
	if len(segs) == 0 {
		return nil
	}
	return []string{terminal.Line(strings.Join(segs, " · "), width)}
}

// DisplayVersion renders a pinned engine version for the tray's brand
// cell (2026-08-10 — the version left the sidebar meta line, whose
// self-only row made project switches jump): `dev-` hashes drop the
// prefix, cut to 8, and wear the `build` label (2026-07-31, Chris: the
// bare hash kept reading as "what even is this?"; `build` names it in
// `vibe dev status`'s own vocabulary, and the hex IS the binary
// digest's first 8) — release versions as-is. The value lands in a
// tmux option a status format expands, so it goes through the encoder
// like every semi-trusted string.
func DisplayVersion(version string) string {
	if rest, ok := strings.CutPrefix(version, "dev-"); ok {
		if len(rest) > 8 {
			rest = rest[:8]
		}
		version = "build " + rest
	}
	return terminal.Line(version, 32)
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

// AgentEntryKindSidecar marks an AgentEntry as an engine sidecar
// container (2026-07-31): Session is the sidecar's manifest name
// (role minus the `sidecar:` prefix), State the engine vocabulary
// running/stale/stopped — Docker truth from the fetch path, no
// container-side feeder involved. It rides the services group in the
// sidebar; nothing about it is attachable.
const AgentEntryKindSidecar = "sidecar"

type AgentEntry struct {
	Project string // full project ID, the join key for tmux @vibe_project
	Session string // inner tmux session name — the ADDRESS `vibe attach` takes
	State   string // agent-session state vocabulary (theme.go AgentStates)
	CLI     string // the CLI actually running at that address
	Model   string
	Epoch   int64  // unix epoch the state was entered; 0 unknown
	Kind    string // "" = agent session; AgentEntryKindService = svc window
}

// Agents renders the `vibe _agents` porcelain, one container-side agent
// session or workspace-service window per line:
//
//	3<US>project<US>session<US>state<US>cli<US>model<US>epoch[<US>kind]
//
// The leading field is the protocol version — bumped to 3 when the
// never-consumed detail field left the row (the 2026-08-04 state-layer
// cleanup; v2 had added epoch and detail together in the
// signal-density pass, but only epoch ever found a reader); the
// trailing kind is omitted for agent rows, `svc` for workspace
// services, `sidecar` for engine sidecars (a new kind VALUE is not a
// new shape: an older parser drops unknown-kind lines by design, no
// bump). Sessions are addresses (or svc window names sharing the same
// closed charset): a name that could not survive a tmux target, a
// state-file name, or a mouse-range name is dropped outright rather
// than escaped — every consumer of this porcelain turns the session
// into one of those. CLI and model are container-fed free text,
// sanitized and bounded here; an unknown epoch writes empty.
func Agents(entries []AgentEntry, width int) []string {
	if width <= 0 {
		width = 80
	}
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Project == "" || !SessionNameRe.MatchString(e.Session) {
			continue
		}
		epoch := ""
		if e.Epoch > 0 {
			epoch = strconv.FormatInt(e.Epoch, 10)
		}
		line := fmt.Sprintf("3%s%s%s%s%s%s%s%s%s%s%s%s",
			fleetSep, e.Project,
			fleetSep, e.Session,
			fleetSep, terminal.Line(e.State, 24),
			fleetSep, terminal.Line(e.CLI, width),
			fleetSep, terminal.Line(e.Model, width),
			fleetSep, epoch)
		if e.Kind == AgentEntryKindService || e.Kind == AgentEntryKindSidecar {
			line += fleetSep + e.Kind
		}
		lines = append(lines, line)
	}
	return lines
}

// Fleet renders the `vibe _fleet` porcelain, one project per line:
//
//	3<US>id<US>token<US>mode<US>pending<US>churn<US>display-name
//
// The leading field is the protocol version — bumped to 3 when the
// never-consumed engine-version field left the row (the 2026-08-04
// state-layer cleanup: other projects' versions rendered nowhere; the
// own project's arrives via the detail cache). The display name comes
// last because it is the only free-text field; it is sanitized, and
// consumers re-truncate for display, so the width budget only bounds
// pathological names. No projects renders no lines.
func Fleet(views []ProjectView, width int) []string {
	if width <= 0 {
		width = 80
	}
	lines := make([]string, 0, len(views))
	for _, v := range views {
		lines = append(lines, fmt.Sprintf("3%s%s%s%s%s%s%s%d%s%s%s%s",
			fleetSep, v.ID,
			fleetSep, v.Token(),
			fleetSep, v.Mode,
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

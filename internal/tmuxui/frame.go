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
// truncation, the nested agent rows, the gutter bars, and the click
// map, so the drawn frame and the click targets are built by one
// function and tested as one function. It also renders the TRAY's
// ghost cells (the same fetch-cache truth, the same one-click reach),
// so the two agent surfaces cannot disagree about what exists.

// FrameWindow is one stateful (or plain) tmux window of a session.
type FrameWindow struct {
	ID      string // tmux window id (@N)
	Name    string
	Glyph   string // empty = plain window (host shell, popup): no dot, no agent row
	DotHex  string // dot color as #rrggbb (state-render.sh's @vibe_dot_fg)
	Attn    bool
	Active  bool
	Model   string
	State   string // raw @vibe_state; "" on pre-state artifacts (see windowSignal)
	Session string // the container-side session this window views (@vibe_session)
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
	Agents        []AgentEntry // container-side truth from the `vibe ps` cache
}

// FrameOutput is one rendered frame: the click map in the
// @vibe_sidebar_map format ("row:target" pairs, space-separated,
// 0-based mouse_y rows), the tray's ghost cells (a tmux format
// fragment published as the self session's @vibe_ghosts, spliced into
// the generated winlist), the ghost map (the cells' session names in
// range order, space-separated, published as @vibe_ghost_map — the
// name table the indexed ghost-N ranges resolve through), and the ANSI
// body, newline-free (every row is absolutely positioned) so it
// transports as a single protocol line.
type FrameOutput struct {
	Map      string
	Ghosts   string
	GhostMap string
	Body     string
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

// ParseAgents parses the `vibe _agents` porcelain, the read twin of
// Agents(), skipping lines of an unknown protocol version or shape.
// The optional seventh field is the row kind (`svc` = workspace
// service); an unknown kind drops the line like any other shape error.
func ParseAgents(lines []string) []AgentEntry {
	var out []AgentEntry
	for _, line := range lines {
		f := strings.Split(line, fleetSep)
		if len(f) < 6 || len(f) > 7 || f[0] != "1" || f[1] == "" || !sessionNameRe.MatchString(f[2]) {
			continue
		}
		e := AgentEntry{
			Project: f[1], Session: f[2], State: f[3], CLI: f[4], Model: f[5],
		}
		if len(f) == 7 {
			if f[6] != AgentEntryKindService {
				continue
			}
			e.Kind = AgentEntryKindService
		}
		out = append(out, e)
	}
	return out
}

// ParseFrameData parses the tmux porcelain the sidebar loop pipes to
// `vibe _frame`: US-separated, line-typed records.
//
//	G<US>width<US>height<US>self_session_id
//	S<US>session_id<US>name<US>path<US>project_id
//	W<US>session_id<US>glyph<US>dot_hex<US>attn<US>window_id<US>window_name<US>active<US>model[<US>state<US>session]
//
// Windows attach to their session by id; records for unknown sessions
// and malformed lines are dropped, never errors — a sidebar frame
// renders what it can. The W record's trailing fields are optional: a
// sidebar.sh older than the nested roster feeds nine, and the frame
// degrades to glyph-only signal detection (windowSignal).
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
		case f[0] == "W" && len(f) >= 9 && f[1] != "":
			i, ok := index[f[1]]
			if !ok {
				continue
			}
			w := FrameWindow{
				Glyph:  f[2],
				DotHex: f[3],
				Attn:   f[4] == "1",
				ID:     f[5],
				Name:   f[6],
				Active: f[7] == "1",
				Model:  f[8],
			}
			if len(f) >= 11 {
				w.State, w.Session = f[9], f[10]
			}
			in.Sessions[i].Windows = append(in.Sessions[i].Windows, w)
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
	body  strings.Builder
	maps  []string
	used  int // highest row drawn so far — the footer must never overdraw
	limit int // first row content may not use (the footer's)
}

// putAt draws content at the 0-based row (map keys are 0-based mouse_y
// rows); an empty target means the row is not clickable. Rows at or
// beyond the limit are dropped from BOTH the body and the map — an
// overfull fleet clips instead of painting (and click-mapping) rows the
// pane cannot show.
func (c *frameCanvas) putAt(row int, content, target string) {
	if row >= c.limit {
		return
	}
	fmt.Fprintf(&c.body, "\x1b[%d;1H%s%s", row+1, content, ansiEOL)
	if target != "" {
		c.maps = append(c.maps, fmt.Sprintf("%d:%s", row, target))
	}
	if row > c.used {
		c.used = row
	}
}

// agentRow is one nested agent row inside a project block: the state
// dot, the CLI actually running, its dim model, and the one click
// target — a window jump ("SESSION:WINDOW") for a viewer-backed agent,
// the attach-only viewer spawn ("SESSION:agent-NAME") for a
// container-side session with no window.
type agentRow struct {
	dot    string // colored glyph, ready to draw
	name   string
	model  string
	target string
	dim    bool // presence-not-signal (idle): the row renders dim
	// Grouped-block fields (docs/tui-layout.md "Workspace services",
	// second dogfood): header rows are the dim `agents` / `services`
	// labels — render-only, no dot; conn is the tree connector ("├ ",
	// "└ ") entry rows hang from when the block is grouped.
	header bool
	conn   string
}

// treeConns hangs a group's rows off its header: ├ for every row but
// the last, └ closing the group.
func treeConns(rows []agentRow) []agentRow {
	for i := range rows {
		if i == len(rows)-1 {
			rows[i].conn = "└ "
		} else {
			rows[i].conn = "├ "
		}
	}
	return rows
}

// windowSignal reports whether a viewer window's agent asks something
// of the operator. The recorded state is the truth; a window from an
// artifact older than the window-level @vibe_state stamp has none, so
// the pre-chosen glyph and the attention flag carry what they can (✗
// exited, ◌ frontend-dead/gone) and a plain ● degrades to presence —
// its dot still rides the name row.
func windowSignal(w FrameWindow) bool {
	if w.State != "" {
		return AgentSignal(w.State)
	}
	return w.Attn || w.Glyph == "✗" || w.Glyph == "◌"
}

// Frame renders one sidebar frame. Layout contract (tui-layout.md,
// "Sidebar frame contract"): row 0 stays blank; the fleet section flows
// from row 1 — per session a project block under its gutter bar (coral
// for the own project, border-hex for another in-use one): a name row,
// a `⎇ branch` row when known, engine facts or the own project's
// detail block, the nested agent rows (the full roster — idle rows
// dim), and a blank slop row, all claiming the session as click
// target. Cold registered
// projects (fleet entries with no live session) render dim, barless,
// and unclickable. The footer hint owns the last row (render-only) and
// content clips above it. The same pass renders the tray's ghost cells
// for the own project, so presence (tray) and signal (sidebar) are
// computed from one join.
func Frame(in FrameInput) FrameOutput {
	var (
		cFG    = fg(PaletteHex("fg"))
		cDim   = fg(PaletteHex("dim"))
		cCoral = fg(PaletteHex("coral"))
		bar    = fg(PaletteHex("border")) + "▏"
		selfB  = cCoral + "▍"
	)
	// Text budget: 2-col left gutter, keep 1 clear on the right.
	max := in.Width - 3
	if max < 8 {
		max = 8
	}
	// A nested agent row spends the gutter bar, the indent, and the dot.
	amax := max - 4
	if amax < 8 {
		amax = 8
	}

	sessions := append([]FrameSession(nil), in.Sessions...)
	sort.SliceStable(sessions, func(i, j int) bool { return sessions[i].Name < sessions[j].Name })

	fleetByID := make(map[string]FleetEntry, len(in.Fleet))
	for _, f := range in.Fleet {
		fleetByID[f.ID] = f
	}
	agentsByProject := make(map[string][]AgentEntry, len(in.Agents))
	servicesByProject := map[string][]AgentEntry{}
	for _, ag := range in.Agents {
		if ag.Kind == AgentEntryKindService {
			servicesByProject[ag.Project] = append(servicesByProject[ag.Project], ag)
			continue
		}
		agentsByProject[ag.Project] = append(agentsByProject[ag.Project], ag)
	}

	var c frameCanvas
	// Content clips above the footer's row; a pane too short for even
	// one content row draws none.
	if c.limit = in.Height - 1; c.limit < 0 {
		c.limit = 0
	}
	c.body.WriteString("\x1b[H\x1b[K")
	row := 1
	ghosts, ghostMap := "", ""
	seen := map[string]bool{}
	for _, s := range sessions {
		self := s.ID == in.SelfSession
		gutter, nameC := " "+bar+ansiReset, cDim
		if self {
			gutter, nameC = " "+selfB+ansiReset, cFG
		}
		// Every live agent is a ROW — the sidebar is the project's
		// roster (2026-07-26, Chris; supersedes idle-collapses-to-dot:
		// three dogfood rounds read the dim dot as "claude is
		// missing"). Signal still styles: idle rows render dim, and
		// attention keeps its coral dot (the tab-blend @vibe_dot_fg
		// would vanish here). No agent is drawn twice — a session's
		// window row wins over its cache row.
		var agents []agentRow
		// viewed is the VIEWER-EXISTS join: any window stamped with a
		// container session, glyph or not. The stamp arrives at window
		// birth (chooser launch, palette restart, agent-open) precisely
		// because hookless CLIs never earn a glyph — gating this map on
		// the glyph once left codex's ghost alive with three viewers
		// open, a zombie button that spawned another viewer per click.
		// drawn is narrower: the sessions this window pass actually put
		// on the surface (glyph windows), the no-double-draw rule for
		// the cache rows below.
		viewed := map[string]bool{}
		viewerWin := map[string]string{}
		drawn := map[string]bool{}
		for _, w := range s.Windows {
			if w.Session != "" {
				viewed[w.Session] = true
				if _, ok := viewerWin[w.Session]; !ok {
					viewerWin[w.Session] = w.ID
				}
			}
			if w.Glyph == "" {
				continue
			}
			if w.Session != "" {
				drawn[w.Session] = true
			}
			dotc := fg(w.DotHex)
			if w.Attn {
				dotc = cCoral
			}
			agents = append(agents, agentRow{
				dot:    dotc + w.Glyph,
				name:   w.Name,
				model:  w.Model,
				target: s.ID + ":" + w.ID,
				dim:    !windowSignal(w),
			})
		}
		// Container-side sessions the window pass did not draw (the
		// `vibe ps` fetch cache): viewer-less agents, and hookless CLIs
		// even with a viewer open — their windows carry the birth stamp
		// but no glyph (the startup claude: its record persisted `idle`
		// across the reopen, its first title event hasn't). The click
		// target honors the viewer join: jump to the stamped window
		// when one exists, the attach-only spawn only when none does.
		// Cache rows are only as fresh as the last fetch.
		for _, ag := range agentsByProject[s.Project] {
			if drawn[ag.Session] {
				continue
			}
			glyph, hex, ok := AgentStyle(ag.State)
			if !ok {
				continue
			}
			name := ag.CLI
			if name == "" {
				name = ag.Session // pre-truth rows: the address is all there is
			}
			// The attach-only spawn is for the LIVING (2026-07-28, the
			// refusal-button sweep's third site): a dead session's
			// attach refuses by design, so its click minted a fresh
			// corpse window. A dead viewer-less row carries a `dead-`
			// target instead — LEFT-click degrades to the project
			// switch, while the right-click menu reads the address
			// through it for dismissal (same day, second pass). A dead
			// row WITH a stamped window (a crash corpse) still jumps —
			// the corpse is exactly what the operator should see.
			target := s.ID + ":dead-" + ag.Session
			if AgentLive(ag.State) {
				target = s.ID + ":agent-" + ag.Session
			}
			if wid, ok := viewerWin[ag.Session]; ok {
				target = s.ID + ":" + wid
			}
			agents = append(agents, agentRow{
				dot:    fg(hex) + glyph,
				name:   name,
				model:  ag.Model,
				target: target,
				dim:    !AgentSignal(ag.State),
			})
		}
		// Workspace services close the roster (docs/tui-layout.md
		// "Workspace services"): one row per svc window. The dim rule
		// INVERTS the agent signal set: running is nominal (quiet, dim
		// — a service has no idle/working split), exited(RC) is the
		// glance that needs eyes. The click target always carries the
		// window NAME (`svc-`/`svcx-`, the dead marker feeding the
		// menu's dismiss label): the services viewer is shared, so the
		// window-id jump would strip the name the right-click menu
		// resolves verbs through. A DEAD service keeps its reach —
		// unlike a dead agent session, the `services` SESSION is alive
		// and the kept corpse window holds the crash log, exactly what
		// the click should show.
		var services []agentRow
		for _, sv := range servicesByProject[s.Project] {
			glyph, hex, ok := AgentStyle(sv.State)
			if !ok {
				continue
			}
			dead := strings.HasPrefix(sv.State, "exited")
			token := ":svc-"
			if dead {
				token = ":svcx-"
			}
			services = append(services, agentRow{
				dot:    fg(hex) + glyph,
				name:   sv.Session,
				target: s.ID + token + sv.Session,
				dim:    !dead,
			})
		}
		// Grouped block (second dogfood, 2026-07-28): with services
		// present the roster splits under dim `agents` / `services`
		// header rows and entries hang off tree connectors; the per-row
		// `svc` qualifier is gone — the header says it once. A block
		// with no services keeps the flat form verbatim, so the common
		// agents-only project pays nothing.
		if len(services) > 0 {
			grouped := make([]agentRow, 0, len(agents)+len(services)+2)
			if len(agents) > 0 {
				grouped = append(grouped, agentRow{name: "agents", header: true})
				grouped = append(grouped, treeConns(agents)...)
			}
			grouped = append(grouped, agentRow{name: "services", header: true})
			grouped = append(grouped, treeConns(services)...)
			agents = grouped
		}
		if self {
			ghosts, ghostMap = ghostCells(agentsByProject[s.Project], viewed)
		}
		c.putAt(row, gutter+ansiBold+nameC+terminal.Line(s.Name, max)+ansiReset, s.ID)
		row++
		// ONE dim meta line under the name (2026-07-26, supersedes the
		// separate branch row + multi-line detail block): branch, then
		// engine facts — the own project's compact `vibe _sidebar`
		// line (cache-only), other projects' fleet facts — joined with
		// ` · `. Engine state speaks ◐/○/▲; on this surface ● belongs
		// to agents alone. The row keeps the session as click slop.
		var meta []string
		if s.Branch != "" {
			meta = append(meta, "⎇ "+s.Branch)
		}
		if s.Project != "" {
			seen[s.Project] = true
		}
		if self && s.Project != "" && len(in.Detail) > 0 {
			for _, dline := range in.Detail {
				if d := strings.TrimSpace(dline); d != "" {
					meta = append(meta, d)
				}
			}
		} else if f, ok := fleetByID[s.Project]; ok && s.Project != "" {
			switch f.Token {
			case string(StateStale):
				meta = append(meta, string(StateStale)+" stale")
			case string(StateStopped):
				meta = append(meta, string(StateStopped)+" stopped")
			}
			if f.Pending > 0 {
				meta = append(meta, fmt.Sprintf("▲%d", f.Pending))
			}
			if f.Mode == "dev" {
				meta = append(meta, "dev")
			}
		}
		if len(meta) > 0 {
			c.putAt(row, gutter+"  "+cDim+terminal.Line(strings.Join(meta, " · "), max-2)+ansiReset, s.ID)
			row++
		}
		// The nested rows close the block. When they don't fit, this
		// block's last slot becomes its OWN overflow count (per-block,
		// not one fleet-wide tally); the slop row stays reserved so two
		// blocks can never run together.
		avail := c.limit - row - 1
		if avail < 0 {
			avail = 0
		}
		shown := min(len(agents), avail)
		for i, a := range agents[:shown] {
			if hidden := len(agents) - shown; hidden > 0 && i == shown-1 {
				// The overflow tally counts ENTRIES only — a clipped
				// header row is layout, not a hidden agent or service.
				n := 1
				for _, h := range agents[shown:] {
					if !h.header {
						n++
					}
				}
				if agents[shown-1].header {
					n--
				}
				c.putAt(row, gutter+"  "+cDim+terminal.Line(fmt.Sprintf("… +%d more", n), amax)+ansiReset, "")
				row++
				break
			}
			if a.header {
				c.putAt(row, gutter+"  "+cDim+terminal.Line(a.name, amax)+ansiReset, "")
				row++
				continue
			}
			pre, budget := "  ", amax
			if a.conn != "" {
				pre, budget = "  "+cDim+a.conn+ansiReset, amax-2
				if budget < 8 {
					budget = 8
				}
			}
			c.putAt(row, gutter+pre+a.dot+ansiReset+" "+agentLabel(a, budget, cFG, cDim), a.target)
			row++
		}
		c.putAt(row, "", s.ID) // blank slop row — the block separator
		row++
	}
	// Cold registered projects — fleet entries with no live session.
	// Render-only dim rows, and barless: the gutter marks projects in
	// use. (Click dispatching `up` is a recorded product call, not
	// half-shipped here.)
	for _, f := range in.Fleet {
		if seen[f.ID] {
			continue
		}
		c.putAt(row, "  "+cDim+"· "+terminal.Line(f.Name, max-2)+ansiReset, "")
		row++
	}
	// Clear everything below the fleet section: rows a shrinking frame
	// no longer draws would otherwise persist on the pane.
	fmt.Fprintf(&c.body, "\x1b[%d;1H\x1b[J", row+1)

	// The footer hint row (render-only, never clickable): the cold-start
	// pointer to the palette — the cheatsheet only appears once the
	// prefix is already known. It owns the last row, so the content
	// clip lifts for exactly this write.
	if fr := in.Height - 1; fr > c.used {
		c.limit = in.Height
		c.putAt(fr, " "+cDim+terminal.Line("C-Space · Space palette", max)+ansiReset, "")
	}
	c.body.WriteString("\x1b[H") // park the cursor; a trailing newline cannot scroll
	return FrameOutput{Map: strings.Join(c.maps, " "), Ghosts: ghosts, GhostMap: ghostMap, Body: c.body.String()}
}

// agentLabel renders a nested row's text: the CLI actually running,
// then its model dim — the model dropped first when the two cannot
// share the line (tui-layout.md budgets). A dim (idle) row keeps its
// place in the roster but whispers.
func agentLabel(a agentRow, budget int, cFG, cDim string) string {
	c := cFG
	if a.dim {
		c = cDim
	}
	name := terminal.Line(a.name, budget)
	if a.model == "" {
		return c + name + ansiReset
	}
	model := terminal.Line(a.model, budget)
	if len([]rune(name))+2+len([]rune(model)) > budget {
		return c + name + ansiReset
	}
	return c + name + ansiReset + "  " + cDim + model + ansiReset
}

// ghostLabelRe strips everything outside the CLI display charset
// (agent-session.sh mints "claude", "codex:review"): a tmux format must
// never see a '#' or a ',' the renderer did not put there.
var ghostLabelRe = regexp.MustCompile(`[^A-Za-z0-9:._-]`)

// ghostCells renders the tray's ghost cells for one project: every
// LIVE container-side agent session with NO viewer window, as a
// clickable cell in its own user mouse range. Presence and reach is
// the tray's whole contract — an idle agent still deserves one click,
// and an attention coral is visible with no window open at all — but
// reach is also why dead sessions get NO cell (2026-07-28, Chris, the
// corpse-UX dogfood): their click ran an attach that refuses dead
// sessions by design, a button wired to a refusal. Dead stays on the
// SIGNAL surfaces — the sidebar's ✗ row and the chooser's
// launch-again entry.
//
// The range carries an INDEX (ghost-N), never the session name: tmux
// stores a status range's name in a fixed 16-byte buffer (proven on
// the pinned 3.7b — struct style_range), so a name-carrying range
// silently truncates and the click dispatches a name nobody minted
// (the first dogfood attached "agent-agent-codex" as "agent-cod" and
// conjured a junk session by that name). The session names ride the
// second return in range order — published as @vibe_ghost_map beside
// @vibe_ghosts, resolved back to a name by agent-open.sh at click
// time, an option-value channel with no length cliff.
//
// The cells are a tmux FORMAT spliced into the generated winlist, so
// they obey the conf's cell rules: one attribute per #[...] block (the
// winlist is expanded through #{?…} comma parsing) and no byte outside
// the sanitized session/CLI charsets. Styling is the layout doc's
// proposal — dim italics on the surface color behind a hairline inset;
// the recorded fallback, if a terminal fights the italics, is dropping
// #[italics] and the hairline for dim alone.
func ghostCells(agents []AgentEntry, viewed map[string]bool) (cells, ghostMap string) {
	var b strings.Builder
	var names []string
	for _, ag := range agents {
		if viewed[ag.Session] || !sessionNameRe.MatchString(ag.Session) || !AgentLive(ag.State) {
			continue
		}
		glyph, hex, ok := AgentStyle(ag.State)
		if !ok {
			glyph, hex = string(StateNone), PaletteHex("dim")
		}
		label := ag.CLI
		if label == "" {
			label = ag.Session
		}
		label = ghostLabelRe.ReplaceAllString(label, "")
		if len(label) > ghostLabelMax {
			label = label[:ghostLabelMax] // ASCII by construction after the strip
		}
		if label == "" {
			continue
		}
		fmt.Fprintf(&b, " #[range=user|ghost-%d]#[fg=%s]#[bg=%s]#[italics] ▏#[fg=%s]%s#[fg=%s] %s #[norange]#[noitalics]#[default]",
			len(names), PaletteHex("dim"), PaletteHex("surface"),
			hex, glyph, PaletteHex("dim"), label)
		names = append(names, ag.Session)
	}
	return b.String(), strings.Join(names, " ")
}

// ghostLabelMax bounds one ghost cell's label; the tray is shared with
// the real tabs and tmux clips the whole list, so a pathological CLI
// name must not push them off the bar.
const ghostLabelMax = 16

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
	Epoch   int64  // @vibe_state_epoch: when the state was entered; 0 unknown
}

// FrameSession is one tmux session on the vibe socket.
type FrameSession struct {
	ID      string // tmux session id ($N)
	Name    string // display name (@vibe_name, falling back to session_name)
	Path    string // session_path; the caller resolves Branch from it
	Branch  string
	Project string // full project ID (@vibe_project), join key for engine facts
	// SvcFold collapses this block's services tree to its counted
	// header (@vibe_svc_fold, toggled by the header's own click —
	// sidebar.sh click mode). Per session, so each project folds alone.
	SvcFold bool
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
	// Now is the frame's clock (unix seconds): ages render at frame
	// time from cached epochs, so minute granularity needs no cadence
	// the frame does not already have. 0 disables ages.
	Now int64
}

// FrameOutput is one rendered frame: the click map in the
// @vibe_sidebar_map format ("row:target" pairs, space-separated,
// 0-based mouse_y rows), the tray's ghost cells (a tmux format
// fragment published as the self session's @vibe_ghosts, spliced into
// the generated winlist), the ghost map (the cells' session names in
// range order, space-separated, published as @vibe_ghost_map — the
// name table the indexed ghost-N ranges resolve through), the spin
// cells ("ROW:COL" pairs, space-separated, 1-based ANSI coordinates of
// every drawn WORKING dot — the sidebar's sub-tick overlay repaints
// exactly these cells between frames, docs/tui-layout.md "The working
// spinner"), and the ANSI body, newline-free (every row is absolutely
// positioned) so it transports as a single protocol line.
type FrameOutput struct {
	Map      string
	Ghosts   string
	GhostMap string
	Spin     string
	Body     string
}

// FleetEntry is one parsed `vibe _fleet` line — the read twin of
// Fleet().
type FleetEntry struct {
	ID      string
	Token   string
	Mode    string
	Pending int
	Churn   string // "+128 −40" working-tree churn; "" clean/unknown
	Name    string
}

// ParseFleet parses the `vibe _fleet` porcelain, skipping lines of an
// unknown protocol version or shape. The v2 grammar (which still
// carried the retired engine-version field) parses one generation so a
// cache written by the previous engine keeps rendering across the
// binary swap; v1 (pre-churn, 2026-07-29) tolerance was pruned
// 2026-08-04 — a stale v1 line skips and its row refills on the first
// good fetch, the graceful shape every unknown line already gets.
func ParseFleet(lines []string) []FleetEntry {
	var out []FleetEntry
	for _, line := range lines {
		f := strings.Split(line, fleetSep)
		v2, v3 := len(f) == 8 && f[0] == "2", len(f) == 7 && f[0] == "3"
		if (!v2 && !v3) || f[1] == "" {
			continue
		}
		e := FleetEntry{ID: f[1], Token: f[2], Mode: f[3], Name: f[len(f)-1]}
		if v3 {
			e.Pending, _ = strconv.Atoi(f[4])
			e.Churn = f[5]
		} else {
			e.Pending, _ = strconv.Atoi(f[5])
			e.Churn = f[6]
		}
		out = append(out, e)
	}
	return out
}

// ParseAgents parses the `vibe _agents` porcelain, the read twin of
// Agents(), skipping lines of an unknown protocol version or shape.
// The optional trailing field is the row kind (`svc` = workspace
// service, `sidecar` = engine sidecar container); an unknown kind
// drops the line like any other shape error.
// The v2 grammar (which still carried the retired detail field) parses
// one generation so a cache written by the previous engine keeps
// rendering across the binary swap; v1 (pre-epoch, 2026-07-29)
// tolerance was pruned 2026-08-04 — stale v1 lines skip and refill on
// the first good fetch.
func ParseAgents(lines []string) []AgentEntry {
	var out []AgentEntry
	for _, line := range lines {
		f := strings.Split(line, fleetSep)
		v2, v3 := len(f) >= 8 && len(f) <= 9 && f[0] == "2", len(f) >= 7 && len(f) <= 8 && f[0] == "3"
		if (!v2 && !v3) || f[1] == "" || !SessionNameRe.MatchString(f[2]) {
			continue
		}
		e := AgentEntry{
			Project: f[1], Session: f[2], State: f[3], CLI: f[4], Model: f[5],
		}
		if epoch, err := strconv.ParseInt(f[6], 10, 64); err == nil && epoch > 0 {
			e.Epoch = epoch
		}
		hasKind := (v3 && len(f) == 8) || (v2 && len(f) == 9)
		if hasKind {
			kind := f[len(f)-1]
			if kind != AgentEntryKindService && kind != AgentEntryKindSidecar {
				continue
			}
			e.Kind = kind
		}
		out = append(out, e)
	}
	return out
}

// ParseFrameData parses the tmux porcelain the sidebar loop pipes to
// `vibe _frame`: US-separated, line-typed records.
//
//	G<US>width<US>height<US>self_session_id
//	S<US>session_id<US>name<US>path<US>project_id[<US>svc_fold]
//	W<US>session_id<US>glyph<US>dot_hex<US>attn<US>window_id<US>window_name<US>active<US>model<US>state<US>session<US>epoch
//
// Windows attach to their session by id; records for unknown sessions
// and malformed lines are dropped, never errors — a sidebar frame
// renders what it can. The W record's 9/10/11-field tolerances
// (pre-2026-07-26 and pre-07-29 scripts) were pruned 2026-08-04 — a
// script that old degrades to no window rows for its ≤1-slow-tick
// self-exec window; empty VALUES in the trailing fields still degrade
// per-field (no state → windowSignal, no epoch → no age). The S
// record's trailing svc_fold stays optional one generation: a
// sidebar.sh older than the services fold (2026-08-02) feeds five and
// no block folds.
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
		case f[0] == "S" && (len(f) == 5 || len(f) == 6) && f[1] != "":
			index[f[1]] = len(in.Sessions)
			in.Sessions = append(in.Sessions, FrameSession{
				ID: f[1], Name: f[2], Path: f[3], Project: f[4],
				SvcFold: len(f) == 6 && f[5] == "1",
			})
		case f[0] == "W" && len(f) == 12 && f[1] != "":
			i, ok := index[f[1]]
			if !ok {
				continue
			}
			w := FrameWindow{
				Glyph:   f[2],
				DotHex:  f[3],
				Attn:    f[4] == "1",
				ID:      f[5],
				Name:    f[6],
				Active:  f[7] == "1",
				Model:   f[8],
				State:   f[9],
				Session: f[10],
			}
			if epoch, err := strconv.ParseInt(f[11], 10, 64); err == nil && epoch > 0 {
				w.Epoch = epoch
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
// dot, the CLI actually running, its dim model (or the state word that
// takes the model's slot on attention/exited rows), the right-aligned
// age, and the one click target — a window jump ("SESSION:WINDOW") for
// a viewer-backed agent, the attach-only viewer spawn
// ("SESSION:agent-NAME") for a container-side session with no window.
type agentRow struct {
	dot    string // colored glyph, ready to draw
	name   string
	model  string // the model slot: model, or the state word (stateWord)
	age    string // compact time-in-state, right-aligned; "" unknown
	target string
	dim    bool // presence-not-signal (idle): the row renders dim
	// Grouped-block fields (docs/tui-layout.md "Workspace services",
	// second dogfood): header rows are the dim `agents` / `services`
	// labels — render-only, no dot; count is the header's dim entry
	// tally (docs/tui-layout.md "Signal density"); conn is the tree
	// connector ("├ ", "└ ") entry rows hang from when the block is
	// grouped.
	header bool
	count  int
	conn   string
	folded bool // a collapsed group's header: wears the ▸ tell
	hide   bool // folded into the `… +n` overflow slot (rosterBlock)
	// spin marks a working row's dot for the sub-tick overlay: the
	// frame reports its drawn coordinates so the sidebar can animate
	// exactly that cell without re-rendering (the frame's own glyph
	// stays the static ● — the fallback truth under a dead animator).
	spin bool
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

// stateWord is the text that takes the model's slot on exactly the
// rows where the dot's color is the whole message (glance ambiguity,
// and color-blindness — docs/tui-layout.md "Signal density"):
// "attention", and "exit RC" on ✗ rows. Every other state pays
// nothing: signal styles, never hides, and quiet stays quiet.
func stateWord(state string) string {
	if state == "attention" {
		return state
	}
	if rest, ok := strings.CutPrefix(state, "exited"); ok {
		rc := strings.TrimSuffix(strings.TrimPrefix(rest, "("), ")")
		if rc == "" {
			return "exited"
		}
		return "exit " + rc
	}
	return ""
}

// modelSlot resolves a row's model-slot text from its state and model.
func modelSlot(state, model string) string {
	if w := stateWord(state); w != "" {
		return w
	}
	return model
}

// rowAge renders the right-aligned age for a roster row: time IN
// STATE, from the cached epoch against the frame's clock. Sub-minute
// ages floor at "<1m" (2026-07-29, the polish pass): exact seconds
// shimmered on every forced frame and overstated the cadence — the
// frame redraws at 10s, so "19s" was stale by up to 10s while looking
// live. `vibe ps` keeps the exact seconds: a point-in-time snapshot is
// accurate at the moment it prints.
func rowAge(now, epoch int64) string {
	if now <= 0 || epoch <= 0 || epoch > now {
		return ""
	}
	if now-epoch < 60 {
		return "<1m"
	}
	return CompactAge(now - epoch)
}

// rosterBlock lays out one project block's roster within avail rows:
// grouped under counted `agents` / `services` headers when services
// exist (flat otherwise, so the common agents-only project pays
// nothing), the trailing slot becoming the `… +n` overflow count when
// the rows don't fit. The fold takes DIM (nominal) rows first, bottom
// up — a signal row is never the hidden one while a quiet one shows
// (docs/tui-layout.md "Signal density"); headers always render, since
// their counts stand in for whatever folded.
// svcFold collapses the services group to its header (the header's
// click toggles @vibe_svc_fold): the entries leave the layout AND the
// overflow math, so folding a long tree is also what buys a crowded
// block its agent rows back; the header's count keeps saying what
// folded, and its ▸ says the hiding was chosen, not overflow. Headers
// reuse the dim flag (dim is a header's nominal look): a folded group
// hiding a SIGNAL row — a dead service, a stale sidecar — renders its
// header bright, so the fold can quiet the tree but never the glance.
func rosterBlock(agents, services []agentRow, avail int, svcFold bool) []agentRow {
	if avail <= 0 {
		return nil
	}
	grouped := len(services) > 0
	folded := grouped && svcFold
	svcCount, svcSignal := len(services), false
	if folded {
		for _, r := range services {
			if !r.dim {
				svcSignal = true
			}
		}
		services = nil
	}
	headers := 0
	if grouped {
		headers = 1
		if len(agents) > 0 {
			headers = 2
		}
	}
	hidden := 0
	if total := len(agents) + len(services) + headers; total > avail {
		slots := avail - headers - 1 // the marker takes a row of its own
		if slots < 0 {
			slots = 0
		}
		hidden = len(agents) + len(services) - slots
		k := hidden
		for pass := 0; pass < 2 && k > 0; pass++ {
			for _, list := range [][]agentRow{services, agents} { // bottom of the block first
				for i := len(list) - 1; i >= 0 && k > 0; i-- {
					if list[i].hide || (pass == 0 && !list[i].dim) {
						continue
					}
					list[i].hide = true
					k--
				}
			}
		}
	}
	var out []agentRow
	addGroup := func(name string, rows []agentRow) {
		if len(rows) == 0 {
			return
		}
		if grouped {
			out = append(out, agentRow{name: name, header: true, count: len(rows), dim: true})
		}
		var vis []agentRow
		for _, r := range rows {
			if !r.hide {
				vis = append(vis, r)
			}
		}
		if grouped {
			vis = treeConns(vis)
		}
		out = append(out, vis...)
	}
	addGroup("agents", agents)
	if folded {
		out = append(out, agentRow{name: "services", header: true, count: svcCount,
			folded: true, dim: !svcSignal})
	} else {
		addGroup("services", services)
	}
	if hidden > 0 {
		out = append(out, agentRow{name: fmt.Sprintf("… +%d more", hidden), header: true, dim: true})
	}
	if len(out) > avail {
		out = out[:avail]
	}
	return out
}

// wrapSegments packs the meta segments into ` · `-joined rows within
// the budget: one row when everything fits (the common case — a quiet
// block pays nothing), a segment-boundary wrap onto continuation rows
// when it doesn't (2026-07-29, Chris — supersedes the raw character
// clip, whose mid-segment `dev …` hid engine facts behind a ragged
// cut; overflow-driven wrapping is not the always-on multi-line detail
// block the meta line replaced). A single segment wider than the
// budget keeps a row of its own and still character-clips at draw time
// — the safety net, not the design.
func wrapSegments(segs []string, budget int) []string {
	var rows []string
	cur := ""
	for _, seg := range segs {
		switch {
		case cur == "":
			cur = seg
		case len([]rune(cur))+3+len([]rune(seg)) <= budget:
			cur += " · " + seg
		default:
			rows = append(rows, cur)
			cur = seg
		}
	}
	if cur != "" {
		rows = append(rows, cur)
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
// projects (fleet entries with no live session) render dim and
// barless, claiming `cold-<id>` — the click that brings them up. The footer hints own the last rows (render-only)
// and content clips above them. The same pass renders the tray's ghost cells
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
	sidecarsByProject := map[string][]AgentEntry{}
	for _, ag := range in.Agents {
		switch ag.Kind {
		case AgentEntryKindService:
			servicesByProject[ag.Project] = append(servicesByProject[ag.Project], ag)
		case AgentEntryKindSidecar:
			sidecarsByProject[ag.Project] = append(sidecarsByProject[ag.Project], ag)
		default:
			agentsByProject[ag.Project] = append(agentsByProject[ag.Project], ag)
		}
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
	var spin []string
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
				model:  modelSlot(w.State, w.Model),
				age:    rowAge(in.Now, w.Epoch),
				target: s.ID + ":" + w.ID,
				dim:    !windowSignal(w),
				spin:   w.State == "working",
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
				model:  modelSlot(ag.State, ag.Model),
				age:    rowAge(in.Now, ag.Epoch),
				target: target,
				dim:    !AgentSignal(ag.State),
				spin:   ag.State == "working",
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
			// Dead-service forensics: the RC already rides the folded
			// exited(RC) state — the word joins the row instead of
			// collapsing to a bare ✗, and pairs with the age: a corpse
			// tells you what and when.
			services = append(services, agentRow{
				dot:    fg(hex) + glyph,
				name:   sv.Session,
				model:  stateWord(sv.State),
				age:    rowAge(in.Now, sv.Epoch),
				target: s.ID + token + sv.Session,
				dim:    !dead,
			})
		}
		// Engine sidecars close the services group (2026-07-31, Chris —
		// they left the meta line, whose ragged clip kept hiding exactly
		// which sidecar exists): one row per sidecar container, dim
		// qualifier in the model slot while running, the state word
		// (stale/stopped) taking the slot otherwise — the same
		// color-blindness rule the agent rows follow. Most sidecars
		// have nothing to open, so their click is the project switch
		// the name row already carries — but the engine's own dns
		// ledger (2026-08-04 dogfood: "dns sidecar" said neither what
		// it does nor where to look) wears `ledger` and clicks through
		// to the network-egress popup (`:egress`, sidebar.sh — the
		// second door to prefix+E). The "dns" literal is the schema's
		// reserved engine-sidecar name; tmuxui depends only on
		// terminal, so the constant cannot be imported here.
		for _, sc := range sidecarsByProject[s.Project] {
			glyph, hex, ok := SidecarStyle(sc.State)
			if !ok {
				continue
			}
			dns := sc.Session == "dns"
			slot := "sidecar"
			if dns {
				slot = "ledger"
			}
			if sc.State != "running" {
				slot = sc.State
			}
			target := s.ID
			if dns {
				target = s.ID + ":egress"
			}
			services = append(services, agentRow{
				dot:    fg(hex) + glyph,
				name:   sc.Session,
				model:  slot,
				target: target,
				dim:    sc.State == "running",
			})
		}
		if self {
			ghosts, ghostMap = ghostCells(agentsByProject[s.Project], viewed)
		}
		c.putAt(row, gutter+ansiBold+nameC+terminal.Line(s.Name, max)+ansiReset, s.ID)
		row++
		// The dim meta line under the name (2026-07-26, supersedes the
		// separate branch row + multi-line detail block): branch, then
		// engine facts — the own project's compact `vibe _sidebar`
		// line (cache-only), other projects' fleet facts — joined with
		// ` · `. One row when it fits; over budget it wraps at segment
		// boundaries (wrapSegments) instead of character-clipping.
		// Engine state speaks ◐/○/▲; on this surface ● belongs
		// to agents alone. The rows keep the session as click slop.
		var meta []string
		if s.Branch != "" {
			// Churn rides the branch segment (`⎇ main  +128 −40`):
			// fleet-cache truth on the fetch cadence — "has the agent
			// actually changed anything" without opening lazygit.
			branch := "⎇ " + s.Branch
			if f, ok := fleetByID[s.Project]; ok && f.Churn != "" {
				branch += "  " + f.Churn
			}
			meta = append(meta, branch)
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
			// Glyph + word from the one shared mapping (theme.go
			// TokenWord); nominal tokens contribute nothing — absence
			// IS the signal, the rule every surface shares.
			if word := TokenWord(StateToken(f.Token)); word != "" {
				meta = append(meta, f.Token+" "+word)
			}
			if f.Pending > 0 {
				meta = append(meta, fmt.Sprintf("▲%d", f.Pending))
			}
			if f.Mode == "dev" {
				meta = append(meta, "dev")
			}
		}
		for _, line := range wrapSegments(meta, max-2) {
			c.putAt(row, gutter+"  "+cDim+terminal.Line(line, max-2)+ansiReset, s.ID)
			row++
		}
		// The nested rows close the block. When they don't fit, this
		// block's last slot becomes its OWN overflow count (per-block,
		// not one fleet-wide tally) with DIM rows folded first; the
		// slop row stays reserved so two blocks can never run together.
		avail := c.limit - row - 1
		if avail < 0 {
			avail = 0
		}
		for _, a := range rosterBlock(agents, services, avail, s.SvcFold) {
			if a.header {
				label := a.name
				if a.count > 0 {
					label = fmt.Sprintf("%s · %d", a.name, a.count)
				}
				if a.folded {
					label += " ▸"
				}
				// The services header is the fold toggle (the `:svcfold`
				// target flips @vibe_svc_fold in sidebar.sh's click mode);
				// the agents header and the overflow slot stay render-only.
				target := ""
				if a.name == "services" {
					target = s.ID + ":svcfold"
				}
				// A bright header is a folded group hiding a signal row.
				hcol := cDim
				if !a.dim {
					hcol = cFG
				}
				c.putAt(row, gutter+"  "+hcol+terminal.Line(label, amax)+ansiReset, target)
				row++
				continue
			}
			pre, budget, dotCol := "  ", amax, 5
			if a.conn != "" {
				pre, budget, dotCol = "  "+cDim+a.conn+ansiReset, amax-2, 7
				if budget < 8 {
					budget = 8
				}
			}
			// A drawn working dot reports its ANSI coordinates for the
			// sub-tick overlay — only rows putAt will actually paint.
			if a.spin && row < c.limit {
				spin = append(spin, fmt.Sprintf("%d:%d", row+1, dotCol))
			}
			c.putAt(row, gutter+pre+a.dot+ansiReset+" "+agentLabel(a, budget, cFG, cDim), a.target)
			row++
		}
		c.putAt(row, "", s.ID) // blank slop row — the block separator
		row++
	}
	// Cold registered projects — fleet entries with no live session.
	// Dim rows, and barless: the gutter marks projects in use. The
	// click target is `cold-<id>` (the product call, resolved
	// 2026-08-04: LEFT dispatches a background `up` through sidebar.sh
	// → `vibe _up`); no colon keeps it invisible to the right-click
	// menu's session-scoped grammar. The glyph is the fleet state
	// token, not a fixed `·` (same-day dogfood: the first click-up
	// visibly changed NOTHING — cold only encodes sessionless, so a
	// running project's row must say ● for the dispatched up to have
	// an effect the eye can find).
	for _, f := range in.Fleet {
		if seen[f.ID] {
			continue
		}
		glyph := f.Token
		if glyph == "" {
			glyph = string(StateNone)
		}
		c.putAt(row, "  "+cDim+glyph+" "+terminal.Line(f.Name, max-2)+ansiReset, "cold-"+f.ID)
		row++
	}
	// Clear everything below the fleet section: rows a shrinking frame
	// no longer draws would otherwise persist on the pane.
	fmt.Fprintf(&c.body, "\x1b[%d;1H\x1b[J", row+1)

	// The footer hint rows (render-only, never clickable): the
	// cold-start pointer to the palette — the cheatsheet only appears
	// once the prefix is already known — and, height-gated under it,
	// the review-stack keys (the product's best surface and, before
	// the prefix is known, its least discoverable). The three-surface
	// curation rule lives at the conf's @vibe_cheat: footer = discovery
	// pointer, cheat = daily-loop keys, palette = complete. The second row
	// renders only when the frame has slack, so a short pane loses the
	// new hint, never the old one. They own the last rows, so the
	// content clip lifts for exactly these writes.
	if fr := in.Height - 1; fr > c.used {
		c.limit = in.Height
		if fr-1 > c.used {
			c.putAt(fr-1, " "+cDim+terminal.Line("C-Space · Space palette", max)+ansiReset, "")
			c.putAt(fr, " "+cDim+terminal.Line("f files · g git · v clip", max)+ansiReset, "")
		} else {
			c.putAt(fr, " "+cDim+terminal.Line("C-Space · Space palette", max)+ansiReset, "")
		}
	}
	c.body.WriteString("\x1b[H") // park the cursor; a trailing newline cannot scroll
	return FrameOutput{Map: strings.Join(c.maps, " "), Ghosts: ghosts, GhostMap: ghostMap,
		Spin: strings.Join(spin, " "), Body: c.body.String()}
}

// agentLabel renders a nested row's text: the CLI actually running,
// its dim model slot, and the dim age right-aligned on the budget's
// edge. The drop order under pressure (tui-layout.md "Signal density"
// budgets): the model slot first, the age second-to-last, the name
// never. A dim (idle) row keeps its place in the roster but whispers.
func agentLabel(a agentRow, budget int, cFG, cDim string) string {
	c := cFG
	if a.dim {
		c = cDim
	}
	name := terminal.Line(a.name, budget)
	used := len([]rune(name))
	age := a.age
	if age != "" && used+2+len([]rune(age)) > budget {
		age = ""
	}
	slot := ""
	if a.model != "" {
		m := terminal.Line(a.model, budget)
		need := used + 2 + len([]rune(m))
		if age != "" {
			need += 2 + len([]rune(age))
		}
		if need <= budget {
			slot = m
		}
	}
	out := c + name + ansiReset
	if slot != "" {
		out += "  " + cDim + slot + ansiReset
		used += 2 + len([]rune(slot))
	}
	if age != "" {
		pad := budget - used - len([]rune(age))
		if pad < 2 {
			pad = 2
		}
		out += strings.Repeat(" ", pad) + cDim + age + ansiReset
	}
	return out
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
		if viewed[ag.Session] || !SessionNameRe.MatchString(ag.Session) || !AgentLive(ag.State) {
			continue
		}
		glyph, hex, ok := AgentStyle(ag.State)
		if !ok {
			glyph, hex = string(StateNone), PaletteHex("dim")
		}
		// A working ghost's dot rides the animator's option instead of
		// the literal glyph: the cell is a format fragment the status
		// line re-expands on every @vibe_spin write, so a viewer-less
		// working agent spins in the tray for free. The option's conf
		// default is the static ● — no animator, no difference.
		if ag.State == "working" {
			glyph = "#{@vibe_spin}"
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

package tmuxui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// frameRows decodes the newline-free ANSI body back into per-row plain
// text: absolute-position escapes select the (0-based) row, everything
// else on the row has its styling stripped. Later writes to a row win,
// like a screen.
func frameRows(t *testing.T, body string) map[int]string {
	t.Helper()
	posRe := regexp.MustCompile(`\x1b\[(?:(\d+);1)?H`)
	ansiRe := regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	locs := posRe.FindAllStringSubmatchIndex(body, -1)
	if len(locs) == 0 || locs[0][0] != 0 {
		t.Fatalf("body must start with a cursor-position escape: %q", body)
	}
	rows := map[int]string{}
	for i, loc := range locs {
		row := 0
		if loc[2] >= 0 {
			n, err := strconv.Atoi(body[loc[2]:loc[3]])
			if err != nil {
				t.Fatal(err)
			}
			row = n - 1
		}
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		if content := ansiRe.ReplaceAllString(body[loc[1]:end], ""); content != "" {
			rows[row] = content
		}
	}
	return rows
}

// mapRows parses the @vibe_sidebar_map format ("row:target" pairs).
func mapRows(t *testing.T, m string) map[int]string {
	t.Helper()
	out := map[int]string{}
	if m == "" {
		return out
	}
	for entry := range strings.FieldsSeq(m) {
		rowStr, target, ok := strings.Cut(entry, ":")
		if !ok {
			t.Fatalf("malformed map entry %q in %q", entry, m)
		}
		row, err := strconv.Atoi(rowStr)
		if err != nil {
			t.Fatal(err)
		}
		out[row] = target
	}
	return out
}

// twoSessionInput is the shared fixture: the own project (alpha) with
// an idle viewer window plus two container-side sessions the ps cache
// knows about — one of them the idle window's own session, one a
// viewer-less agent wanting attention — and another project (beta)
// whose agent is working.
func twoSessionInput() FrameInput {
	return FrameInput{
		Width: 30, Height: 24, SelfSession: "$1",
		Sessions: []FrameSession{
			{ID: "$2", Name: "beta", Path: "/b", Project: "projbeta", Windows: []FrameWindow{
				{ID: "@9", Name: "claude", Glyph: "●", DotHex: "#9ece6a", Model: "opus",
					State: "working", Session: "agent"},
			}},
			{ID: "$1", Name: "alpha", Path: "/a", Branch: "main", Project: "projalpha", Windows: []FrameWindow{
				{ID: "@3", Name: "codex:review", Glyph: "●", DotHex: "#5c6b96", Active: true,
					State: "idle", Session: "agent-codex-review"},
			}},
		},
		Agents: []AgentEntry{
			{Project: "projalpha", Session: "agent-codex-review", State: "idle", CLI: "codex:review"},
			{Project: "projalpha", Session: "agent-ghost", State: "attention", CLI: "claude", Model: "fable"},
			{Project: "projalpha", Session: "agent-quiet", State: "idle", CLI: "codex"},
		},
	}
}

func TestFrameClickMapAlignsWithRows(t *testing.T) {
	out := Frame(twoSessionInput())
	rows := frameRows(t, out.Body)
	clicks := mapRows(t, out.Map)

	// Row 0 stays blank; sessions sort by name, so alpha leads. Its
	// block: name, branch, the full agent roster (idle viewer, the
	// viewer-less attention agent, the viewer-less idle agent), slop.
	if _, ok := rows[0]; ok {
		t.Fatalf("row 0 must stay blank, got %q", rows[0])
	}
	if !strings.Contains(rows[1], "alpha") {
		t.Fatalf("row 1 should carry the first session name, got %q", rows[1])
	}
	if !strings.Contains(rows[2], "⎇ main") {
		t.Fatalf("row 2 should carry alpha's branch, got %q", rows[2])
	}
	// Name, branch, and the blank slop row all claim the session; the
	// nested agent rows (3-5) claim their agents instead.
	for _, r := range []int{1, 2, 6} {
		if clicks[r] != "$1" {
			t.Fatalf("row %d should click to $1, got %q (map %q)", r, clicks[r], out.Map)
		}
	}
	if !strings.Contains(rows[7], "beta") || clicks[7] != "$2" {
		t.Fatalf("beta's name row misplaced: rows[7]=%q clicks[7]=%q", rows[7], clicks[7])
	}
	// Every rendered row maps to a session, a window jump, or the
	// attach-only spawn — the frame and the click map cannot skew.
	for row, target := range clicks {
		switch {
		case target == "$1" || target == "$2":
		case strings.Contains(target, ":@"), strings.Contains(target, ":agent-"):
		default:
			t.Fatalf("row %d maps to unknown target %q", row, target)
		}
	}
}

func TestFrameNestedAgentRows(t *testing.T) {
	out := Frame(twoSessionInput())
	rows := frameRows(t, out.Body)
	clicks := mapRows(t, out.Map)

	// The roster (2026-07-26, supersedes idle-collapses-to-dot): every
	// live agent is a row. Alpha's idle viewer window leads, dim, with
	// the window jump; the viewer-less attention agent follows with the
	// attach-only spawn; the viewer-less idle agent closes the block.
	if !strings.Contains(rows[3], "codex:review") || clicks[3] != "$1:@3" {
		t.Fatalf("the idle viewer earns a dim row with its window jump: rows[3]=%q clicks[3]=%q", rows[3], clicks[3])
	}
	// The state word takes the model's slot on attention rows (the
	// signal-density pass): the dot's color is no longer the whole
	// message.
	if !strings.Contains(rows[4], "claude") || !strings.Contains(rows[4], "attention") {
		t.Fatalf("the viewer-less attention row should carry CLI + state word: %q", rows[4])
	}
	if strings.Contains(rows[4], "fable") {
		t.Fatalf("the state word must replace the model, not join it: %q", rows[4])
	}
	if clicks[4] != "$1:agent-agent-ghost" {
		t.Fatalf("viewer-less rows spawn a viewer: %q", clicks[4])
	}
	if !strings.Contains(rows[5], "codex") || clicks[5] != "$1:agent-agent-quiet" {
		t.Fatalf("the viewer-less idle agent keeps a roster row: rows[5]=%q clicks[5]=%q", rows[5], clicks[5])
	}
	// The name row carries no dots anymore — presence went to rows.
	if strings.Contains(rows[1], "●") {
		t.Fatalf("the name row must not carry agent dots: %q", rows[1])
	}
	// Idle whispers, signal speaks: the idle rows render their names
	// dim, the attention row keeps the fg color.
	dim, fgc := fg(PaletteHex("dim")), fg(PaletteHex("fg"))
	if !strings.Contains(out.Body, dim+"codex:review") {
		t.Fatal("an idle row's name must render dim")
	}
	if !strings.Contains(out.Body, fgc+"claude") {
		t.Fatal("a signal row's name must keep the fg color")
	}
	// beta's working agent has a window, so its row jumps to it.
	if !strings.Contains(rows[8], "claude") || !strings.Contains(rows[8], "opus") {
		t.Fatalf("beta's working agent row: %q", rows[8])
	}
	if clicks[8] != "$2:@9" {
		t.Fatalf("viewer-backed rows jump to session+window: %q", clicks[8])
	}
}

func TestFrameGutterBars(t *testing.T) {
	in := twoSessionInput()
	in.Fleet = []FleetEntry{
		{ID: "cold-project", Token: string(StateNone), Name: "coldname"},
		{ID: "warm-project", Token: string(StateRunning), Name: "warmname"},
	}
	out := Frame(in)
	rows := frameRows(t, out.Body)

	// The gutter is 2 cols: the bar spans its project's whole block —
	// coral for the own project, border-hex for another in use, none
	// for a cold row.
	for _, row := range []int{1, 2, 3, 4, 5, 6} {
		if !strings.HasPrefix(rows[row], " ▍") && rows[row] != "" {
			t.Fatalf("row %d should sit under the self gutter bar: %q", row, rows[row])
		}
	}
	for _, row := range []int{7, 8} {
		if !strings.HasPrefix(rows[row], " ▏") {
			t.Fatalf("row %d should sit under the other-project gutter bar: %q", row, rows[row])
		}
	}
	if !strings.Contains(out.Body, fg(PaletteHex("coral"))+"▍") {
		t.Fatal("the own project's bar must render coral")
	}
	if !strings.Contains(out.Body, fg(PaletteHex("border"))+"▏") {
		t.Fatal("another in-use project's bar must render border-hex")
	}
	cold, coldRow, warm := "", -1, ""
	for i, content := range rows {
		if strings.Contains(content, "coldname") {
			cold, coldRow = content, i
		}
		if strings.Contains(content, "warmname") {
			warm = content
		}
	}
	if !strings.HasPrefix(cold, "  · ") {
		t.Fatalf("a cold project renders barless in the gutter: %q", cold)
	}
	// The glyph is the fleet state token: a running-but-sessionless
	// project reads ●, so a click-dispatched up visibly changes the
	// row when the refetch lands.
	if !strings.HasPrefix(warm, "  ● ") {
		t.Fatalf("a running cold project must wear the state token: %q", warm)
	}
	// The cold row claims `cold-<id>` — the up-dispatching click
	// (sidebar.sh). No colon: the right-click menu's session-scoped
	// grammar must never match it.
	clicks := mapRows(t, out.Map)
	if clicks[coldRow] != "cold-cold-project" {
		t.Fatalf("cold row click target = %q, want cold-cold-project", clicks[coldRow])
	}
}

func TestFrameGhostCells(t *testing.T) {
	in := twoSessionInput()
	// Dead and viewer-less: a sidebar ✗ row (it still earns its roster
	// row — the other tests' fixture stays without it so their row
	// arithmetic holds), never a tray cell.
	in.Agents = append(in.Agents, AgentEntry{
		Project: "projalpha", Session: "agent-dead", State: "exited(1)", CLI: "codex",
	})
	out := Frame(in)

	// Presence and reach: every container-side session of the OWN
	// project with no window here, idle included, each its own user
	// mouse range. Ranges carry INDEXES (tmux clips range names at 15
	// bytes — a session name would truncate into a name nobody minted);
	// the names ride the ghost map in range order. The session that
	// already has a viewer window is a tab, never also a ghost.
	for _, want := range []string{"range=user|ghost-0", "range=user|ghost-1"} {
		if !strings.Contains(out.Ghosts, want) {
			t.Fatalf("ghost cells missing %q: %q", want, out.Ghosts)
		}
	}
	if out.GhostMap != "agent-ghost agent-quiet" {
		t.Fatalf("ghost map must carry the session names in range order: %q", out.GhostMap)
	}
	if strings.Contains(out.GhostMap, "agent-codex-review") {
		t.Fatalf("a session with a viewer window must not also be a ghost: %q", out.GhostMap)
	}
	// Reach means alive: a dead session gets no cell (2026-07-28) —
	// the sidebar's ✗ row and the chooser's launch entry carry it.
	if strings.Contains(out.GhostMap, "agent-dead") {
		t.Fatalf("a dead session must not be a tray cell: %q", out.GhostMap)
	}
	// Its sidebar row carries a dead- target: left-click degrades to
	// the project switch, right-click reads the address for dismissal.
	if !strings.Contains(out.Map, ":dead-agent-dead") {
		t.Fatalf("dead row must carry a dead- target: %q", out.Map)
	}
	if strings.Contains(out.Map, ":agent-agent-dead") {
		t.Fatalf("dead row must not carry the attach-spawn target: %q", out.Map)
	}
	// The dot carries real state: attention is visible with no window.
	if !strings.Contains(out.Ghosts, "#[fg="+PaletteHex("coral")+"]●") {
		t.Fatalf("the attention ghost's dot must render coral: %q", out.Ghosts)
	}
	if !strings.Contains(out.Ghosts, "#[italics]") || !strings.Contains(out.Ghosts, "#[bg="+PaletteHex("surface")+"]") {
		t.Fatalf("ghost cells wear the inset styling: %q", out.Ghosts)
	}
	// A tmux format spliced through comma-parsing constructs: one
	// attribute per #[...] block, and no stray separator bytes.
	for _, block := range strings.Split(out.Ghosts, "#[")[1:] {
		attrs, _, _ := strings.Cut(block, "]")
		if strings.Contains(attrs, ",") {
			t.Fatalf("style block %q must carry one attribute", attrs)
		}
	}
	// Another project's agents belong to another tray.
	in = twoSessionInput()
	in.Agents = append(in.Agents, AgentEntry{Project: "projbeta", Session: "agent-elsewhere", State: "idle"})
	if strings.Contains(Frame(in).Ghosts, "elsewhere") {
		t.Fatal("ghost cells must be scoped to the tray's own project")
	}
}

func TestFrameGlyphlessViewerClearsGhost(t *testing.T) {
	// A hookless CLI's viewer window: @vibe_session stamped at birth
	// (chooser/palette/agent-open), but no glyph — no title events ever
	// arrive. The viewer join must still count it: no ghost, and the
	// session's cache row jumps to the window instead of offering the
	// attach-only spawn (which would mint viewer after viewer).
	in := twoSessionInput()
	in.Sessions[1].Windows = append(in.Sessions[1].Windows,
		FrameWindow{ID: "@7", Name: "codex", Session: "agent-ghost"})
	out := Frame(in)
	if out.GhostMap != "agent-quiet" {
		t.Fatalf("a stamped glyphless viewer must clear its ghost: %q", out.GhostMap)
	}
	if !strings.Contains(out.Map, "$1:@7") {
		t.Fatalf("the session's row must jump to the glyphless viewer: %q", out.Map)
	}
	if strings.Contains(out.Map, "$1:agent-agent-ghost") {
		t.Fatalf("a session with a viewer must not offer the spawn target: %q", out.Map)
	}
	// The row itself still renders — the window pass drew nothing for
	// it (no glyph), and vanishing from the roster would be worse. Its
	// attention state word rides the model slot.
	rows := frameRows(t, out.Body)
	found := false
	for _, content := range rows {
		if strings.Contains(content, "claude") && strings.Contains(content, "attention") {
			found = true
		}
	}
	if !found {
		t.Fatal("the glyphless-viewer session must keep its roster row")
	}
}

func TestFrameGlyphlessViewerIdleRow(t *testing.T) {
	// The startup claude: its idle record persisted across the TUI
	// reopen, its viewer window is self-stamped but has no glyph yet
	// (title events only start with the first message). The roster
	// rule: a dim row from cache truth, jumping to the stamped window
	// — never a ghost, never the attach-only spawn.
	in := twoSessionInput()
	in.Sessions[1].Windows = append(in.Sessions[1].Windows,
		FrameWindow{ID: "@8", Name: "claude", Session: "agent-quiet"})
	out := Frame(in)
	if strings.Contains(out.GhostMap, "agent-quiet") {
		t.Fatalf("a stamped viewer must clear the ghost: %q", out.GhostMap)
	}
	if strings.Contains(out.Map, "$1:agent-agent-quiet") {
		t.Fatalf("a session with a viewer must not offer the spawn: %q", out.Map)
	}
	if !strings.Contains(out.Map, "$1:@8") {
		t.Fatalf("the idle row must jump to the stamped viewer: %q", out.Map)
	}
	if !strings.Contains(out.Body, fg(PaletteHex("dim"))+"codex") {
		t.Fatal("the idle roster row renders its CLI name dim")
	}
}

func TestFrameFooterHint(t *testing.T) {
	out := Frame(twoSessionInput())
	rows := frameRows(t, out.Body)
	clicks := mapRows(t, out.Map)
	// Height 24 has slack → both footer rows render: the palette
	// pointer with the review-stack keys under it, render-only.
	if !strings.Contains(rows[22], "palette") {
		t.Fatalf("palette hint missing above the keys row: %q", rows[22])
	}
	if !strings.Contains(rows[23], "f files · g git · v clip") {
		t.Fatalf("keys hint missing on the last row: %q", rows[23])
	}
	for _, r := range []int{22, 23} {
		if _, ok := clicks[r]; ok {
			t.Fatal("the footer must not be clickable")
		}
	}
}

func TestFrameLongNameStaysOneRow(t *testing.T) {
	in := twoSessionInput()
	in.Sessions[1].Name = strings.Repeat("verylongname", 6)
	in.Sessions[1].Windows = append(in.Sessions[1].Windows,
		FrameWindow{ID: "@4", Name: "w", Glyph: "●", DotHex: "#9ece6a", State: "idle"},
		FrameWindow{ID: "@5", Name: "w", Glyph: "●", DotHex: "#5c6b96", State: "idle"},
	)
	out := Frame(in)
	rows := frameRows(t, out.Body)
	var name string
	for _, content := range rows {
		if strings.Contains(content, "verylongname") {
			name = content
		}
	}
	if !strings.Contains(name, "…") {
		t.Fatalf("long name must truncate: %q (rows %v)", name, rows)
	}
	// The name row must fit the pane (text budget width-3) so it never
	// wraps and the click map below cannot skew; the idle windows live
	// as dim roster rows of their own, never on this row.
	if strings.Contains(name, "●") {
		t.Fatalf("the name row must not carry agent dots: %q", name)
	}
	if n := len([]rune(name)); n > in.Width {
		t.Fatalf("name row overflows the pane: %d runes %q", n, name)
	}
}

func TestFramePerBlockAgentOverflow(t *testing.T) {
	in := FrameInput{Width: 30, Height: 10, SelfSession: "$1"}
	var wins []FrameWindow
	for i := range 8 {
		wins = append(wins, FrameWindow{
			ID: fmt.Sprintf("@%d", i), Name: fmt.Sprintf("agent%d", i),
			Glyph: "●", DotHex: "#9ece6a", State: "working",
		})
	}
	in.Sessions = []FrameSession{
		{ID: "$1", Name: "p", Path: "/p", Windows: wins},
		{ID: "$2", Name: "q", Path: "/q", Windows: []FrameWindow{
			{ID: "@7", Name: "late", Glyph: "●", DotHex: "#9ece6a", State: "working"},
		}},
	}
	out := Frame(in)
	rows := frameRows(t, out.Body)
	clicks := mapRows(t, out.Map)

	// Height 10 → the footer owns row 9 and content clips above it. p's
	// name row is 1, its agent rows start at 2, and the last slot before
	// the reserved slop row (row 8) is row 7: six slots for eight
	// agents, so the sixth becomes the count for the three it stands in
	// for.
	var overflow string
	overflowRow := -1
	for row, content := range rows {
		if strings.Contains(content, "more") {
			overflow, overflowRow = content, row
		}
	}
	if !strings.Contains(overflow, "… +3 more") || overflowRow != 7 {
		t.Fatalf("per-block overflow slot wrong: %q at row %d (rows %v)", overflow, overflowRow, rows)
	}
	if _, ok := clicks[overflowRow]; ok {
		t.Fatal("the overflow slot must not be clickable")
	}
	// Nothing is drawn into the footer's row or below it, and no click
	// target survives there either.
	for row := range rows {
		if row > 9 {
			t.Fatalf("row %d is outside the pane: %q", row, rows[row])
		}
	}
	for row := range clicks {
		if row >= 9 {
			t.Fatalf("row %d is clickable outside the content area", row)
		}
	}
}

func TestFrameTinyPaneClipsToFooter(t *testing.T) {
	in := twoSessionInput()
	in.Height = 5 // alpha's block alone overruns it
	out := Frame(in)
	rows := frameRows(t, out.Body)
	if _, ok := rows[4]; !ok || !strings.Contains(rows[4], "palette") {
		t.Fatalf("the footer keeps the last row: %q", rows[4])
	}
	for row := range rows {
		if row > 4 {
			t.Fatalf("content must clip above the footer, got row %d: %q", row, rows[row])
		}
	}
}

func TestFrameAttentionAndFacts(t *testing.T) {
	in := twoSessionInput()
	in.Sessions[0].Windows[0].Attn = true
	in.Sessions[0].Windows[0].State = "attention"
	in.Fleet = []FleetEntry{
		{ID: "projbeta", Token: string(StateStale), Mode: "dev", Pending: 2, Name: "beta"},
		{ID: "cold-project", Token: string(StateNone), Mode: "release", Name: "coldname"},
	}
	in.Detail = []string{"dev 9766b8d8 · ▲2"}
	in.Width = 40 // the merged meta line needs more than the default fixture width
	out := Frame(in)
	rows := frameRows(t, out.Body)
	clicks := mapRows(t, out.Map)

	coral := fg(PaletteHex("coral"))
	if !strings.Contains(out.Body, coral+"●") {
		t.Fatal("attention dot must render coral")
	}
	// Self session (alpha): branch and engine facts share the ONE dim
	// meta line under the name.
	if !strings.Contains(rows[2], "⎇ main · dev 9766b8d8 · ▲2") || clicks[2] != "$1" {
		t.Fatalf("meta line misplaced: %q -> %q", rows[2], clicks[2])
	}
	// The other session gets the one-line dim facts summary.
	var facts string
	for _, content := range rows {
		if strings.Contains(content, "stale") {
			facts = content
		}
	}
	if !strings.Contains(facts, "◐ stale · ▲2 · dev") {
		t.Fatalf("facts line wrong: %q", facts)
	}
	// The cold registered project renders and claims the up-dispatching
	// target (2026-08-04, supersedes render-only).
	coldRow := -1
	for row, content := range rows {
		if strings.Contains(content, "coldname") {
			coldRow = row
		}
	}
	if coldRow == -1 {
		t.Fatal("cold project row missing")
	}
	if clicks[coldRow] != "cold-cold-project" {
		t.Fatalf("cold row click target = %q, want cold-cold-project", clicks[coldRow])
	}
}

func TestFrameMetaLineWrapsAtSegments(t *testing.T) {
	// Over budget the meta line wraps at segment boundaries onto
	// continuation rows instead of character-clipping (2026-07-29): the
	// ragged `dev …` cut hid exactly the engine facts the line exists
	// to show. The one-line common case stays held by
	// TestFrameAttentionAndFacts (width 40, everything fits).
	in := twoSessionInput()
	in.Fleet = []FleetEntry{{ID: "projalpha", Token: string(StateRunning), Churn: "+442 −144", Name: "alpha"}}
	in.Detail = []string{"dev 9766b8d8 · ▲2"}
	out := Frame(in)
	rows := frameRows(t, out.Body)
	clicks := mapRows(t, out.Map)
	if !strings.Contains(rows[2], "⎇ main  +442 −144") || strings.Contains(rows[2], "dev") {
		t.Fatalf("branch+churn should hold row 2 alone: %q", rows[2])
	}
	if !strings.Contains(rows[3], "dev 9766b8d8 · ▲2") {
		t.Fatalf("engine facts should wrap onto row 3: %q", rows[3])
	}
	if strings.Contains(rows[2], "…") || strings.Contains(rows[3], "…") {
		t.Fatalf("segment wrap must not character-clip: %q / %q", rows[2], rows[3])
	}
	for _, r := range []int{2, 3} {
		if clicks[r] != "$1" {
			t.Fatalf("meta row %d keeps the session click slop, got %q", r, clicks[r])
		}
	}
	// The roster follows the wrapped meta rows instead of overdrawing them.
	if !strings.Contains(rows[4], "codex:review") {
		t.Fatalf("roster should start after the meta rows: %q", rows[4])
	}
}

func TestFrameSpinCells(t *testing.T) {
	// Working dots report their drawn ANSI coordinates (1-based
	// ROW:COL) for the sidebar's sub-tick overlay; everything else
	// reports nothing (docs/tui-layout.md "The working spinner").
	in := twoSessionInput()
	out := Frame(in)
	if out.Spin != "9:5" {
		t.Fatalf("beta's working viewer should be the one spin cell: %q", out.Spin)
	}

	// Grouped block: connector rows indent the dot two further columns,
	// and a working CACHE row (viewer-less) spins too. Its tray ghost
	// cell carries the @vibe_spin format ref instead of a literal
	// glyph, so the animator drives it for free.
	in = twoSessionInput()
	in.Agents[2].State = "working"
	in.Agents = append(in.Agents, AgentEntry{
		Project: "projalpha", Session: "web", State: "running", Kind: AgentEntryKindService})
	out = Frame(in)
	if out.Spin != "7:7 12:5" {
		t.Fatalf("conn-row working dot must shift with the connector: %q", out.Spin)
	}
	if !strings.Contains(out.Ghosts, "#{@vibe_spin}") {
		t.Fatalf("a working ghost's dot should ride the animator option: %q", out.Ghosts)
	}

	// A working row clipped above the footer reports no cell — the
	// overlay must never paint outside the drawn frame.
	in = twoSessionInput()
	in.Height = 8 // beta's block falls below the clip
	out = Frame(in)
	if out.Spin != "" {
		t.Fatalf("clipped rows must not spin: %q", out.Spin)
	}
}

func TestWrapSegments(t *testing.T) {
	if got := wrapSegments([]string{"a", "b", "c"}, 10); len(got) != 1 || got[0] != "a · b · c" {
		t.Fatalf("fitting segments must join on one row: %+v", got)
	}
	got := wrapSegments([]string{"alpha", "beta", "gamma"}, 12)
	if len(got) != 2 || got[0] != "alpha · beta" || got[1] != "gamma" {
		t.Fatalf("overflow must wrap at a segment boundary: %+v", got)
	}
	// A single oversize segment keeps a row of its own — the draw-time
	// clip is the safety net, not the design.
	if got := wrapSegments([]string{"tiny", "way-over-the-budget"}, 8); len(got) != 2 || got[1] != "way-over-the-budget" {
		t.Fatalf("oversize segment keeps its row: %+v", got)
	}
}

func TestFrameSanitizesHostileNames(t *testing.T) {
	in := twoSessionInput()
	in.Sessions[0].Windows[0].Name = "evil\x1b]0;owned\x07name"
	out := Frame(in)
	if strings.Contains(out.Body, "\x1b]") {
		t.Fatal("a window name must not smuggle raw escape sequences into the frame")
	}
	if !strings.Contains(out.Body, "⟨U+001B⟩") {
		t.Fatal("hostile bytes should render escaped, not vanish silently")
	}
}

func TestFrameSanitizesAgentCacheRows(t *testing.T) {
	in := twoSessionInput()
	in.Agents = []AgentEntry{
		// A CLI carrying escapes and tmux format bytes, and a session
		// name outside the address charset: the row's text is encoded
		// for the body, and the ghost cell — a tmux FORMAT — drops what
		// it cannot render literally.
		{Project: "projalpha", Session: "agent-evil", State: "attention",
			CLI: "cli\x1b]0;owned\x07#{q:x},bold", Model: "m"},
		{Project: "projalpha", Session: "bad name; rm -rf", State: "idle", CLI: "nope"},
	}
	out := Frame(in)
	if strings.Contains(out.Body, "\x1b]") || !strings.Contains(out.Body, "⟨U+001B⟩") {
		t.Fatalf("cache-row text must reach the body encoded: %q", out.Body)
	}
	// Nothing that could open a format, close a style block early, or
	// end a cell survives the label allowlist.
	for _, bad := range []string{"\x1b", "#{", ",", ";", "rm -rf"} {
		if strings.Contains(out.Ghosts, bad) {
			t.Fatalf("ghost cells must not carry %q: %q", bad, out.Ghosts)
		}
	}
	if !strings.Contains(out.Ghosts, "range=user|ghost-0") || out.GhostMap != "agent-evil" {
		t.Fatalf("the sanitized ghost should still render, and the unaddressable session must be dropped from the map: %q / %q", out.Ghosts, out.GhostMap)
	}
}

func TestParseFleetRoundTrip(t *testing.T) {
	views := []ProjectView{
		{ID: "p1", Name: "alpha", Mode: "release", Version: "v2.0.0", Pending: 3},
		{ID: "p2", Name: "beta", Mode: "dev"},
	}
	entries := ParseFleet(Fleet(views, 40))
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	if entries[0].ID != "p1" || entries[0].Pending != 3 || entries[0].Name != "alpha" ||
		entries[0].Token != string(StateNone) {
		t.Fatalf("entry 0 mismatch: %+v", entries[0])
	}
	if got := ParseFleet([]string{"2\x1ffuture\x1fformat", "garbage", ""}); len(got) != 0 {
		t.Fatalf("unknown versions and garbage must drop, got %+v", got)
	}
}

func TestParseAgentsRoundTrip(t *testing.T) {
	entries := []AgentEntry{
		{Project: "p1", Session: "agent", State: "working", CLI: "claude", Model: "fable"},
		{Project: "p1", Session: "agent-cold", State: "exited(2)"},
		{Project: "p2", Session: "not a session name", State: "idle"}, // address charset: dropped
		{Session: "agent-orphan", State: "idle"},                      // no project: dropped
	}
	got := ParseAgents(Agents(entries, 40))
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d (%+v)", len(got), got)
	}
	if got[0] != entries[0] {
		t.Fatalf("entry 0 round trip: %+v", got[0])
	}
	if got[1].Session != "agent-cold" || got[1].State != "exited(2)" || got[1].CLI != "" {
		t.Fatalf("entry 1 mismatch: %+v", got[1])
	}
	if bad := ParseAgents([]string{"2\x1ffuture\x1fformat", "garbage", ""}); len(bad) != 0 {
		t.Fatalf("unknown versions and garbage must drop, got %+v", bad)
	}
}

func TestParseFrameData(t *testing.T) {
	us := "\x1f"
	data := strings.Join([]string{
		"G" + us + "42" + us + "20" + us + "$7",
		"S" + us + "$7" + us + "alpha" + us + "/work/a" + us + "projid",
		"S" + us + "$8" + us + "beta" + us + "/work/b" + us + "projid2" + us + "1", // six fields: folded services
		"W" + us + "$7" + us + "●" + us + "#9ece6a" + us + "1" + us + "@1" + us + "claude" + us + "1" + us + "opus" + us + "attention" + us + "agent" + us + "1700000042",
		// Empty trailing VALUES still parse (a plain window with no
		// state stamps) — only short SHAPES were pruned.
		"W" + us + "$7" + us + "●" + us + "#5c6b96" + us + "0" + us + "@4" + us + "old" + us + "0" + us + "" + us + "" + us + "" + us + "",
		"W" + us + "$7" + us + "●" + us + "x" + us + "0" + us + "@5" + us + "nine" + us + "0" + us + "",                                   // nine fields: pruned 2026-08-04, drops
		"W" + us + "$999" + us + "●" + us + "x" + us + "0" + us + "@2" + us + "orphan" + us + "0" + us + "" + us + "" + us + "" + us + "", // unknown session: dropped
		"bogus line",
		"",
	}, "\n")
	in := ParseFrameData(data)
	if in.Width != 42 || in.Height != 20 || in.SelfSession != "$7" {
		t.Fatalf("geometry: %+v", in)
	}
	if len(in.Sessions) != 2 || len(in.Sessions[0].Windows) != 2 {
		t.Fatalf("sessions: %+v", in.Sessions)
	}
	// The five-field S record (a pre-fold sidebar.sh) parses unfolded;
	// the six-field one carries the flag.
	if in.Sessions[0].SvcFold || !in.Sessions[1].SvcFold {
		t.Fatalf("svc_fold: %+v", in.Sessions)
	}
	w := in.Sessions[0].Windows[0]
	if w.Name != "claude" || !w.Attn || !w.Active || w.Model != "opus" || w.ID != "@1" ||
		w.State != "attention" || w.Session != "agent" || w.Epoch != 1700000042 {
		t.Fatalf("window: %+v", w)
	}
	// The short record still parses; its signal degrades to the glyph
	// and it has no epoch.
	if old := in.Sessions[0].Windows[1]; old.State != "" || old.Session != "" || old.Name != "old" || old.Epoch != 0 {
		t.Fatalf("nine-field window: %+v", old)
	}
}

func TestWindowSignal(t *testing.T) {
	for _, tc := range []struct {
		name string
		w    FrameWindow
		want bool
	}{
		{"recorded working", FrameWindow{Glyph: "●", State: "working"}, true},
		{"recorded idle", FrameWindow{Glyph: "●", State: "idle"}, false},
		{"recorded exit code", FrameWindow{Glyph: "✗", State: "exited(1)"}, true},
		{"stateless attention flag", FrameWindow{Glyph: "●", Attn: true}, true},
		{"stateless corpse glyph", FrameWindow{Glyph: "◌"}, true},
		{"stateless plain dot", FrameWindow{Glyph: "●"}, false},
	} {
		if got := windowSignal(tc.w); got != tc.want {
			t.Fatalf("%s: windowSignal = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestFrameDegenerateHeights(t *testing.T) {
	// A pane with no room for content must still produce a coherent
	// frame: no rows drawn past its edge, no click targets at all.
	for _, h := range []int{0, 1, 2} {
		in := twoSessionInput()
		in.Height = h
		out := Frame(in)
		for row := range frameRows(t, out.Body) {
			if row >= h {
				t.Fatalf("height %d drew row %d", h, row)
			}
		}
		for row := range mapRows(t, out.Map) {
			if row >= h-1 {
				t.Fatalf("height %d mapped a click on row %d", h, row)
			}
		}
	}
}

func TestFrameServiceRows(t *testing.T) {
	in := FrameInput{
		Width: 30, Height: 24, SelfSession: "$1",
		Sessions: []FrameSession{
			{ID: "$1", Name: "alpha", Path: "/a", Project: "projalpha"},
		},
		Agents: []AgentEntry{
			{Project: "projalpha", Session: "agent", State: "working", CLI: "claude", Model: "opus"},
			{Project: "projalpha", Session: "web", State: "running", Kind: AgentEntryKindService},
			{Project: "projalpha", Session: "blender", State: "exited(137)", Kind: AgentEntryKindService},
		},
	}
	out := Frame(in)
	rows := frameRows(t, out.Body)
	clicks := mapRows(t, out.Map)

	// With services present the block groups: dim unclickable `agents`
	// / `services` header rows, entries hanging off tree connectors,
	// and no per-row `svc` qualifier — the header says it once.
	if !strings.Contains(rows[2], "agents") || clicks[2] != "" {
		t.Fatalf("agents header row: %q click=%q", rows[2], clicks[2])
	}
	if !strings.Contains(rows[3], "└ ") || !strings.Contains(rows[3], "claude") {
		t.Fatalf("agent row closes its group: %q", rows[3])
	}
	// The services header carries the fold toggle — the one header
	// that clicks (sidebar.sh `:svcfold`); expanded it wears no ▸.
	if !strings.Contains(rows[4], "services") || clicks[4] != "$1:svcfold" {
		t.Fatalf("services header row: %q click=%q", rows[4], clicks[4])
	}
	if strings.Contains(rows[4], "▸") {
		t.Fatalf("an expanded services header must not wear the fold tell: %q", rows[4])
	}
	if !strings.Contains(rows[5], "├ ") || !strings.Contains(rows[5], "web") || strings.Contains(rows[5], "svc ") {
		t.Fatalf("running service row, no svc qualifier: %q", rows[5])
	}
	if clicks[5] != "$1:svc-web" {
		t.Fatalf("service targets carry the window name: %q", clicks[5])
	}
	// The dim rule inverts the agent signal set: running is nominal
	// (dim), a kept corpse is the glance that needs eyes (bright ✗,
	// svcx- marks deadness for the menu's dismiss label) — and the dead
	// row KEEPS its reach: the services session is alive and the corpse
	// window holds the crash log.
	dim, fgc := fg(PaletteHex("dim")), fg(PaletteHex("fg"))
	if !strings.Contains(out.Body, dim+"web") {
		t.Fatal("a running service whispers (dim name)")
	}
	if !strings.Contains(rows[6], "└ ") || !strings.Contains(rows[6], "blender") || !strings.Contains(rows[6], "✗") {
		t.Fatalf("dead service closes the tree: %q", rows[6])
	}
	if clicks[6] != "$1:svcx-blender" {
		t.Fatalf("dead service target: %q", clicks[6])
	}
	if !strings.Contains(out.Body, fgc+"blender") {
		t.Fatal("a dead service speaks (fg name)")
	}
	// Services never ghost: the tray keeps its agent-only contract.
	if !strings.Contains(out.GhostMap, "agent") || strings.Contains(out.GhostMap, "web") {
		t.Fatalf("ghost map must carry agents only: %q", out.GhostMap)
	}

	// With a stamped services viewer open, the target still carries the
	// window name — the shared viewer would strip the name the
	// right-click menu resolves verbs through (agent-open.sh owns the
	// jump-or-spawn instead).
	in.Sessions[0].Windows = []FrameWindow{
		{ID: "@7", Name: "services", Session: "services"},
	}
	out = Frame(in)
	clicks = mapRows(t, out.Map)
	found := false
	for _, target := range clicks {
		if target == "$1:svc-web" {
			found = true
		}
		if strings.HasSuffix(target, ":@7") && strings.Contains(target, "svc") {
			t.Fatalf("service rows must not degrade to the viewer window id: %q", target)
		}
	}
	if !found {
		t.Fatal("service row target must survive an open services viewer")
	}
}

func TestFrameServicesFold(t *testing.T) {
	in := FrameInput{
		Width: 30, Height: 24, SelfSession: "$1",
		Sessions: []FrameSession{
			{ID: "$1", Name: "alpha", Path: "/a", Project: "projalpha", SvcFold: true},
		},
		Agents: []AgentEntry{
			{Project: "projalpha", Session: "agent", State: "working", CLI: "claude", Model: "opus"},
			{Project: "projalpha", Session: "web", State: "running", Kind: AgentEntryKindService},
			{Project: "projalpha", Session: "db", State: "running", Kind: AgentEntryKindService},
		},
	}
	out := Frame(in)
	rows := frameRows(t, out.Body)
	clicks := mapRows(t, out.Map)

	// Folded, the group is exactly its counted header wearing the ▸
	// tell, still the toggle target — the way back is the way in. The
	// agents group is untouched.
	if !strings.Contains(rows[2], "agents · 1") || !strings.Contains(rows[3], "claude") {
		t.Fatalf("agents group must survive the fold: %q / %q", rows[2], rows[3])
	}
	if !strings.Contains(rows[4], "services · 2 ▸") || clicks[4] != "$1:svcfold" {
		t.Fatalf("folded services header: %q click=%q", rows[4], clicks[4])
	}
	for _, content := range rows {
		if strings.Contains(content, "web") || strings.Contains(content, "db") {
			t.Fatalf("folded service entries must not render: %q", content)
		}
	}
	// All-nominal entries fold quiet: the header keeps the dim look.
	if !strings.Contains(out.Body, fg(PaletteHex("dim"))+"services · 2 ▸") {
		t.Fatal("a fold hiding only nominal rows keeps the dim header")
	}

	// A fold never quiets the glance: hiding a SIGNAL row (a dead
	// service) renders the header bright.
	in.Agents[2].State = "exited(137)"
	out = Frame(in)
	if !strings.Contains(out.Body, fg(PaletteHex("fg"))+"services · 2 ▸") {
		t.Fatal("a fold hiding a signal row must render its header bright")
	}

	// Folded entries leave the overflow math too: the block that
	// overflowed expanded (TestFrameGroupedOverflowCountsEntries) fits
	// folded, and the agent rows come back.
	in = FrameInput{Width: 30, Height: 8, SelfSession: "$1",
		Sessions: []FrameSession{{ID: "$1", Name: "alpha", Path: "/a", Project: "p", SvcFold: true}},
		Agents: []AgentEntry{
			{Project: "p", Session: "agent", State: "working", CLI: "claude"},
			{Project: "p", Session: "s1", State: "running", Kind: AgentEntryKindService},
			{Project: "p", Session: "s2", State: "running", Kind: AgentEntryKindService},
			{Project: "p", Session: "s3", State: "running", Kind: AgentEntryKindService},
			{Project: "p", Session: "s4", State: "running", Kind: AgentEntryKindService},
		}}
	joined := ""
	for _, content := range frameRows(t, Frame(in).Body) {
		joined += content + "\n"
	}
	if strings.Contains(joined, "more") || !strings.Contains(joined, "claude") ||
		!strings.Contains(joined, "services · 4 ▸") {
		t.Fatalf("folding must reclaim the overflowed rows: %q", joined)
	}
}

func TestAgentsPorcelainServiceKind(t *testing.T) {
	lines := Agents([]AgentEntry{
		{Project: "p", Session: "agent-codex", State: "idle", CLI: "codex"},
		{Project: "p", Session: "web", State: "running", Kind: AgentEntryKindService},
		{Project: "p", Session: "dns", State: "stale", Kind: AgentEntryKindSidecar},
	}, 80)
	if len(lines) != 3 {
		t.Fatalf("lines: %v", lines)
	}
	back := ParseAgents(lines)
	if len(back) != 3 || back[0].Kind != "" || back[1].Kind != AgentEntryKindService ||
		back[1].Session != "web" || back[1].State != "running" {
		t.Fatalf("roundtrip: %+v", back)
	}
	if back[2].Kind != AgentEntryKindSidecar || back[2].Session != "dns" || back[2].State != "stale" {
		t.Fatalf("sidecar roundtrip: %+v", back[2])
	}
	// An unknown trailing kind drops the line like any shape error.
	if got := ParseAgents([]string{lines[1] + "x"}); len(got) != 0 {
		t.Fatalf("unknown kind must drop: %+v", got)
	}
}

// Engine sidecars ride the services group (2026-07-31): one row per
// sidecar container after the workspace services, dim qualifier slot
// while running, the state word taking the slot on ◐ stale / ○
// stopped. A manifest sidecar's click degrades to the project switch
// (nothing to attach to); the engine's own dns ledger wears `ledger`
// and clicks through to the egress popup (2026-08-04).
func TestFrameSidecarRows(t *testing.T) {
	in := FrameInput{
		Width: 34, Height: 24, SelfSession: "$1",
		Sessions: []FrameSession{
			{ID: "$1", Name: "alpha", Path: "/a", Project: "projalpha"},
		},
		Agents: []AgentEntry{
			{Project: "projalpha", Session: "web", State: "running", Kind: AgentEntryKindService},
			{Project: "projalpha", Session: "db", State: "running", Kind: AgentEntryKindSidecar},
			{Project: "projalpha", Session: "dns", State: "running", Kind: AgentEntryKindSidecar},
		},
	}
	out := Frame(in)
	rows := frameRows(t, out.Body)
	clicks := mapRows(t, out.Map)
	if !strings.Contains(rows[2], "services · 3") {
		t.Fatalf("header counts svc + sidecar rows: %q", rows[2])
	}
	if !strings.Contains(rows[3], "├ ") || !strings.Contains(rows[3], "web") {
		t.Fatalf("workspace service leads the group: %q", rows[3])
	}
	if !strings.Contains(rows[4], "db") || !strings.Contains(rows[4], "sidecar") {
		t.Fatalf("a manifest sidecar wears the generic qualifier: %q", rows[4])
	}
	if clicks[4] != "$1" {
		t.Fatalf("a manifest sidecar's click is the project switch: %q", clicks[4])
	}
	if !strings.Contains(rows[5], "└ ") || !strings.Contains(rows[5], "dns") ||
		!strings.Contains(rows[5], "ledger") || strings.Contains(rows[5], "sidecar") {
		t.Fatalf("the dns ledger says what it is: %q", rows[5])
	}
	if clicks[5] != "$1:egress" {
		t.Fatalf("the dns ledger clicks through to the egress popup: %q", clicks[5])
	}
	// The running ledger wears its looking glass (budgeted at the
	// glyph's true two cells); signal states drop the cosmetics.
	if !strings.Contains(rows[5], "🔭") {
		t.Fatalf("the running ledger wears the telescope: %q", rows[5])
	}
	if strings.Contains(rows[4], "🔭") {
		t.Fatalf("only the dns ledger wears it: %q", rows[4])
	}
	dim := fg(PaletteHex("dim"))
	if !strings.Contains(out.Body, dim+"dns") {
		t.Fatal("a running sidecar whispers (dim name)")
	}
	if strings.Contains(out.GhostMap, "dns") {
		t.Fatalf("sidecars never ghost: %q", out.GhostMap)
	}

	// Stale and stopped are the glances that need eyes: fg name, the
	// state word in the model slot, engine glyphs on the dot.
	in.Agents[2].State = "stale"
	out = Frame(in)
	rows = frameRows(t, out.Body)
	if !strings.Contains(rows[5], "◐") || !strings.Contains(rows[5], "stale") {
		t.Fatalf("stale sidecar row: %q", rows[5])
	}
	if strings.Contains(rows[5], "🔭") {
		t.Fatalf("a signal-state ledger drops the telescope: %q", rows[5])
	}
	if !strings.Contains(out.Body, fg(PaletteHex("fg"))+"dns") {
		t.Fatal("a stale sidecar speaks (fg name)")
	}
	in.Agents[2].State = "stopped"
	out = Frame(in)
	rows = frameRows(t, out.Body)
	if !strings.Contains(rows[5], "○") || !strings.Contains(rows[5], "stopped") {
		t.Fatalf("stopped sidecar row: %q", rows[5])
	}
	// An unknown state draws nothing rather than guessing.
	in.Agents[2].State = "meltdown"
	out = Frame(in)
	rows = frameRows(t, out.Body)
	for _, r := range rows {
		if strings.Contains(r, "dns") {
			t.Fatalf("unknown sidecar state must not render: %q", r)
		}
	}
}

func TestFrameRowAges(t *testing.T) {
	// Ages render at frame time from cached epochs: window rows from
	// @vibe_state_epoch, cache rows from the porcelain's epoch field —
	// right-aligned, meaning time IN STATE.
	in := twoSessionInput()
	now := int64(1_700_010_000)
	in.Now = now
	in.Sessions[1].Windows[0].Epoch = now - 2520 // the idle viewer: 42m
	in.Agents[1].Epoch = now - 3*3600            // the attention agent: 3h
	in.Sessions[0].Windows[0].Epoch = now - 19   // beta's working viewer: fresh
	out := Frame(in)
	rows := frameRows(t, out.Body)
	if !strings.Contains(rows[3], "codex:review") || !strings.HasSuffix(rows[3], "42m") {
		t.Fatalf("window row age missing or not right-aligned: %q", rows[3])
	}
	if !strings.HasSuffix(rows[4], "3h") {
		t.Fatalf("cache row age missing or not right-aligned: %q", rows[4])
	}
	// The two ages end on the same column — right-aligned on the shared
	// budget edge, not trailing the text.
	if len([]rune(rows[3])) != len([]rune(rows[4])) {
		t.Fatalf("ages must align on one edge: %q vs %q", rows[3], rows[4])
	}
	// No epoch, no age: the quiet idle row ends with its CLI name.
	if !strings.HasSuffix(rows[5], "codex") {
		t.Fatalf("epochless row grew a phantom age: %q", rows[5])
	}
	// Sub-minute ages floor at <1m: exact seconds shimmer on every
	// forced frame and overstate the 10s redraw cadence.
	if !strings.HasSuffix(rows[8], "<1m") {
		t.Fatalf("sub-minute age must floor at <1m: %q", rows[8])
	}
	// Ages render dim.
	if !strings.Contains(out.Body, fg(PaletteHex("dim"))+"42m") {
		t.Fatal("the age must render dim")
	}
}

func TestAgentLabelDropOrder(t *testing.T) {
	// Under the text budget the model slot goes first, the age
	// second-to-last, the dot+name never (tui-layout.md "Signal
	// density" budgets).
	strip := func(s string) string {
		return regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`).ReplaceAllString(s, "")
	}
	row := agentRow{name: "claude", model: "fable", age: "42m"}
	full := strip(agentLabel(row, 30, "", ""))
	if full != "claude  fable"+strings.Repeat(" ", 30-13-3)+"42m" {
		t.Fatalf("full label: %q", full)
	}
	// Too narrow for the model: it drops, the age stays aligned.
	noModel := strip(agentLabel(row, 12, "", ""))
	if noModel != "claude   42m" {
		t.Fatalf("model must drop first: %q", noModel)
	}
	// Too narrow even for the age: the name alone survives.
	if got := strip(agentLabel(row, 9, "", "")); got != "claude" {
		t.Fatalf("age must drop before the name: %q", got)
	}
}

func TestFrameGroupHeaderCounts(t *testing.T) {
	in := FrameInput{
		Width: 30, Height: 24, SelfSession: "$1",
		Sessions: []FrameSession{{ID: "$1", Name: "alpha", Path: "/a", Project: "p"}},
		Agents: []AgentEntry{
			{Project: "p", Session: "agent", State: "working", CLI: "claude"},
			{Project: "p", Session: "agent-codex", State: "idle", CLI: "codex"},
			{Project: "p", Session: "web", State: "running", Kind: AgentEntryKindService},
			{Project: "p", Session: "db", State: "running", Kind: AgentEntryKindService},
			{Project: "p", Session: "idler", State: "exited(1)", Kind: AgentEntryKindService},
		},
	}
	rows := frameRows(t, Frame(in).Body)
	if !strings.Contains(rows[2], "agents · 2") {
		t.Fatalf("agents header count: %q", rows[2])
	}
	if !strings.Contains(rows[5], "services · 3") {
		t.Fatalf("services header count: %q", rows[5])
	}
	// Dead-service forensics: exit code word in the model slot.
	var corpse string
	for _, content := range rows {
		if strings.Contains(content, "idler") {
			corpse = content
		}
	}
	if !strings.Contains(corpse, "✗") || !strings.Contains(corpse, "exit 1") {
		t.Fatalf("dead service row must carry the exit code: %q", corpse)
	}
}

func TestFrameOverflowFoldsIdleFirst(t *testing.T) {
	// Five agents, room for three roster rows: the fold hides DIM rows
	// first, so the late signal rows survive while early idle ones join
	// the `… +n` tally — a signal row is never the hidden one.
	in := FrameInput{Width: 30, Height: 7, SelfSession: "$1",
		Sessions: []FrameSession{{ID: "$1", Name: "p", Path: "/p", Project: "p"}},
		Agents: []AgentEntry{
			{Project: "p", Session: "agent-i1", State: "idle", CLI: "idle1"},
			{Project: "p", Session: "agent-i2", State: "idle", CLI: "idle2"},
			{Project: "p", Session: "agent-w1", State: "working", CLI: "work1"},
			{Project: "p", Session: "agent-i3", State: "idle", CLI: "idle3"},
			{Project: "p", Session: "agent-a1", State: "attention", CLI: "attn1"},
		}}
	rows := frameRows(t, Frame(in).Body)
	joined := ""
	for _, content := range rows {
		joined += content + "\n"
	}
	for _, want := range []string{"work1", "attn1", "… +3 more"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %q", want, joined)
		}
	}
	for _, hidden := range []string{"idle2", "idle3"} {
		if strings.Contains(joined, hidden) {
			t.Fatalf("idle row %q must fold before signal rows: %q", hidden, joined)
		}
	}
}

func TestFrameBranchChurn(t *testing.T) {
	in := twoSessionInput()
	in.Fleet = []FleetEntry{{ID: "projalpha", Token: string(StateRunning), Churn: "+128 −40", Name: "alpha"}}
	rows := frameRows(t, Frame(in).Body)
	if !strings.Contains(rows[2], "⎇ main  +128 −40") {
		t.Fatalf("churn must ride the branch segment: %q", rows[2])
	}
	// A clean tree (no churn) keeps the bare branch.
	in.Fleet[0].Churn = ""
	rows = frameRows(t, Frame(in).Body)
	if !strings.Contains(rows[2], "⎇ main") || strings.Contains(rows[2], "+") {
		t.Fatalf("clean tree must not grow churn: %q", rows[2])
	}
}

func TestParseAgentsEpochRoundTrip(t *testing.T) {
	entries := []AgentEntry{
		{Project: "p1", Session: "agent", State: "working", CLI: "claude", Model: "fable",
			Epoch: 1_700_000_000},
		{Project: "p1", Session: "web", State: "running", Epoch: 1_700_000_100, Kind: AgentEntryKindService},
	}
	got := ParseAgents(Agents(entries, 80))
	if len(got) != 2 || got[0] != entries[0] || got[1] != entries[1] {
		t.Fatalf("v3 round trip: %+v", got)
	}
	// The v2 grammar (with the retired detail field) parses one
	// generation across the binary swap — detail simply drops.
	v2 := []string{
		"2\x1fp1\x1fagent\x1fworking\x1fclaude\x1ffable\x1f1700000000\x1fdetached",
		"2\x1fp1\x1fweb\x1frunning\x1f\x1f\x1f1700000100\x1f\x1fsvc",
	}
	old := ParseAgents(v2)
	if len(old) != 2 || old[0].CLI != "claude" || old[0].Epoch != 1_700_000_000 ||
		old[1].Kind != AgentEntryKindService {
		t.Fatalf("v2 compat parse: %+v", old)
	}
	// v1 (pre-epoch, pruned 2026-08-04) drops like any unknown shape.
	v1 := []string{
		"1\x1fp1\x1fagent\x1fworking\x1fclaude\x1ffable",
		"1\x1fp1\x1fweb\x1frunning\x1f\x1f\x1fsvc",
	}
	if got := ParseAgents(v1); len(got) != 0 {
		t.Fatalf("pruned v1 must drop, got %+v", got)
	}
}

func TestParseFleetChurnAndCompat(t *testing.T) {
	v := ProjectView{ID: "p1", Name: "alpha", Churn: "+12 −3"}
	got := ParseFleet(Fleet([]ProjectView{v}, 80))
	if len(got) != 1 || got[0].Churn != "+12 −3" || got[0].Name != "alpha" {
		t.Fatalf("v3 round trip: %+v", got)
	}
	// v2 (with the retired engine-version field) parses one generation;
	// the version value simply drops.
	old := ParseFleet([]string{"2\x1fp1\x1f●\x1frelease\x1fv2.0.0\x1f3\x1f+1 −2\x1falpha"})
	if len(old) != 1 || old[0].Pending != 3 || old[0].Name != "alpha" || old[0].Churn != "+1 −2" {
		t.Fatalf("v2 compat parse: %+v", old)
	}
	// v1 (pre-churn, pruned 2026-08-04) drops like any unknown shape.
	if got := ParseFleet([]string{"1\x1fp1\x1f●\x1frelease\x1fv2.0.0\x1f3\x1falpha"}); len(got) != 0 {
		t.Fatalf("pruned v1 must drop, got %+v", got)
	}
}

func TestFrameGroupedOverflowCountsEntries(t *testing.T) {
	in := FrameInput{Width: 30, Height: 8, SelfSession: "$1",
		Sessions: []FrameSession{{ID: "$1", Name: "alpha", Path: "/a", Project: "p"}},
		Agents: []AgentEntry{
			{Project: "p", Session: "agent", State: "working", CLI: "claude"},
			{Project: "p", Session: "s1", State: "running", Kind: AgentEntryKindService},
			{Project: "p", Session: "s2", State: "running", Kind: AgentEntryKindService},
			{Project: "p", Session: "s3", State: "running", Kind: AgentEntryKindService},
			{Project: "p", Session: "s4", State: "running", Kind: AgentEntryKindService},
		}}
	rows := frameRows(t, Frame(in).Body)
	// The clipped tail is the services header plus four service
	// entries; the tally counts ENTRIES only — headers are layout,
	// not hidden services.
	found := false
	for _, content := range rows {
		if strings.Contains(content, "+4 more") {
			found = true
		}
		if strings.Contains(content, "+5 more") || strings.Contains(content, "+3 more") {
			t.Fatalf("overflow mis-counts headers: %v", rows)
		}
	}
	if !found {
		t.Fatalf("no overflow slot: %v", rows)
	}
}

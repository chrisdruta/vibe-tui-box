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
	// block: name, branch, the viewer-less attention row, slop.
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
	// nested agent row (3) claims the agent instead.
	for _, r := range []int{1, 2, 4} {
		if clicks[r] != "$1" {
			t.Fatalf("row %d should click to $1, got %q (map %q)", r, clicks[r], out.Map)
		}
	}
	if !strings.Contains(rows[5], "beta") || clicks[5] != "$2" {
		t.Fatalf("beta's name row misplaced: rows[5]=%q clicks[5]=%q", rows[5], clicks[5])
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

	// The signal filter: alpha's idle viewer collapses to its dot on the
	// name row (never a row of its own), while the viewer-less agent
	// wanting attention earns one — inside alpha's block, under the
	// branch row, with the attach-only spawn as its target.
	if !strings.Contains(rows[1], "●") {
		t.Fatalf("the idle agent's dot must ride the name row: %q", rows[1])
	}
	for _, content := range rows {
		if strings.Contains(content, "codex:review") {
			t.Fatalf("an idle agent must not also get a nested row: %q", content)
		}
	}
	if !strings.Contains(rows[3], "claude") || !strings.Contains(rows[3], "fable") {
		t.Fatalf("the viewer-less attention row should carry CLI + model: %q", rows[3])
	}
	if clicks[3] != "$1:agent-agent-ghost" {
		t.Fatalf("viewer-less rows spawn a viewer: %q", clicks[3])
	}
	// An idle agent with no viewer earns nothing here — presence lives
	// in the tray and `vibe ps`.
	for row, content := range rows {
		if strings.Contains(content, "codex") && row != 1 {
			t.Fatalf("row %d should not exist for the idle viewer-less agent: %q", row, content)
		}
	}
	// beta's working agent has a window, so its row jumps to it.
	if !strings.Contains(rows[6], "claude") || !strings.Contains(rows[6], "opus") {
		t.Fatalf("beta's working agent row: %q", rows[6])
	}
	if clicks[6] != "$2:@9" {
		t.Fatalf("viewer-backed rows jump to session+window: %q", clicks[6])
	}
	// The working agent is a row, so its dot must NOT also ride beta's
	// name row: no agent is drawn twice on one surface.
	if strings.Contains(rows[5], "●") {
		t.Fatalf("a signal agent must not leave a dot on the name row too: %q", rows[5])
	}
}

func TestFrameGutterBars(t *testing.T) {
	in := twoSessionInput()
	in.Fleet = []FleetEntry{{ID: "cold-project", Token: string(StateNone), Name: "coldname"}}
	out := Frame(in)
	rows := frameRows(t, out.Body)

	// The gutter is 2 cols: the bar spans its project's whole block —
	// coral for the own project, border-hex for another in use, none
	// for a cold row.
	for _, row := range []int{1, 2, 3, 4} {
		if !strings.HasPrefix(rows[row], " ▍") && rows[row] != "" {
			t.Fatalf("row %d should sit under the self gutter bar: %q", row, rows[row])
		}
	}
	for _, row := range []int{5, 6} {
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
	var cold string
	for _, content := range rows {
		if strings.Contains(content, "coldname") {
			cold = content
		}
	}
	if !strings.HasPrefix(cold, "  · ") {
		t.Fatalf("a cold project renders barless in the gutter: %q", cold)
	}
}

func TestFrameGhostCells(t *testing.T) {
	out := Frame(twoSessionInput())

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
	in := twoSessionInput()
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
	// it (no glyph), and vanishing from the roster would be worse.
	rows := frameRows(t, out.Body)
	found := false
	for _, content := range rows {
		if strings.Contains(content, "claude") && strings.Contains(content, "fable") {
			found = true
		}
	}
	if !found {
		t.Fatal("the glyphless-viewer session must keep its roster row")
	}
}

func TestFrameFooterHint(t *testing.T) {
	out := Frame(twoSessionInput())
	rows := frameRows(t, out.Body)
	clicks := mapRows(t, out.Map)
	// Height 24 → the footer hint owns row 23, render-only.
	if !strings.Contains(rows[23], "palette") {
		t.Fatalf("footer hint missing on the last row: %q", rows[23])
	}
	if _, ok := clicks[23]; ok {
		t.Fatal("the footer must not be clickable")
	}
}

func TestFrameLongNameNeverPushesDots(t *testing.T) {
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
	if strings.Count(name, "●") != 3 {
		t.Fatalf("all three idle dots must survive a long name: %q", name)
	}
	// The row must fit the pane: text budget is width-3, and the name
	// budget shrank by 2 per dot, so the row never wraps and the click
	// map below cannot skew.
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
		if strings.Contains(content, "agents") {
			overflow, overflowRow = content, row
		}
	}
	if !strings.Contains(overflow, "… +3 agents") || overflowRow != 7 {
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
	in.Detail = []string{"● dev · 9766b8d8", "▲ 2 pending"}
	out := Frame(in)
	rows := frameRows(t, out.Body)
	clicks := mapRows(t, out.Map)

	coral := fg(PaletteHex("coral"))
	if !strings.Contains(out.Body, coral+"●") {
		t.Fatal("attention dot must render coral")
	}
	// Self session (alpha) carries its detail block under the branch.
	if !strings.Contains(rows[3], "● dev · 9766b8d8") || clicks[3] != "$1" {
		t.Fatalf("detail block misplaced: %q -> %q", rows[3], clicks[3])
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
	// The cold registered project renders but is not clickable.
	coldRow := -1
	for row, content := range rows {
		if strings.Contains(content, "coldname") {
			coldRow = row
		}
	}
	if coldRow == -1 {
		t.Fatal("cold project row missing")
	}
	if _, ok := clicks[coldRow]; ok {
		t.Fatal("cold project rows must not be clickable")
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
		entries[0].Token != string(StateNone) || entries[0].Version != "v2.0.0" {
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
		"W" + us + "$7" + us + "●" + us + "#9ece6a" + us + "1" + us + "@1" + us + "claude" + us + "1" + us + "opus" + us + "attention" + us + "agent",
		"W" + us + "$7" + us + "●" + us + "#5c6b96" + us + "0" + us + "@4" + us + "old" + us + "0" + us + "", // nine fields: pre-state artifact
		"W" + us + "$999" + us + "●" + us + "x" + us + "0" + us + "@2" + us + "orphan" + us + "0" + us + "",  // unknown session: dropped
		"bogus line",
		"",
	}, "\n")
	in := ParseFrameData(data)
	if in.Width != 42 || in.Height != 20 || in.SelfSession != "$7" {
		t.Fatalf("geometry: %+v", in)
	}
	if len(in.Sessions) != 1 || len(in.Sessions[0].Windows) != 2 {
		t.Fatalf("sessions: %+v", in.Sessions)
	}
	w := in.Sessions[0].Windows[0]
	if w.Name != "claude" || !w.Attn || !w.Active || w.Model != "opus" || w.ID != "@1" ||
		w.State != "attention" || w.Session != "agent" {
		t.Fatalf("window: %+v", w)
	}
	// The short record still parses; its signal degrades to the glyph.
	if old := in.Sessions[0].Windows[1]; old.State != "" || old.Session != "" || old.Name != "old" {
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

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

func twoSessionInput() FrameInput {
	return FrameInput{
		Width: 30, Height: 24, SelfSession: "$1",
		Sessions: []FrameSession{
			{ID: "$2", Name: "beta", Path: "/b", Project: "projbeta", Windows: []FrameWindow{
				{ID: "@9", Name: "claude", Glyph: "●", DotHex: "#9ece6a", Model: "opus"},
			}},
			{ID: "$1", Name: "alpha", Path: "/a", Branch: "main", Project: "projalpha", Windows: []FrameWindow{
				{ID: "@3", Name: "codex:review", Glyph: "●", DotHex: "#5c6b96", Active: true},
			}},
		},
	}
}

func TestFrameClickMapAlignsWithRows(t *testing.T) {
	out := Frame(twoSessionInput())
	rows := frameRows(t, out.Body)
	clicks := mapRows(t, out.Map)

	// Row 0 stays blank; sessions sort by name, so alpha leads.
	if _, ok := rows[0]; ok {
		t.Fatalf("row 0 must stay blank, got %q", rows[0])
	}
	if !strings.Contains(rows[1], "alpha") {
		t.Fatalf("row 1 should carry the first session name, got %q", rows[1])
	}
	if !strings.Contains(rows[2], "⎇ main") {
		t.Fatalf("row 2 should carry alpha's branch, got %q", rows[2])
	}
	// Name, branch, and the blank slop row all claim the session.
	for _, r := range []int{1, 2, 3} {
		if clicks[r] != "$1" {
			t.Fatalf("row %d should click to $1, got %q (map %q)", r, clicks[r], out.Map)
		}
	}
	if !strings.Contains(rows[4], "beta") || clicks[4] != "$2" {
		t.Fatalf("beta's name row misplaced: rows[4]=%q clicks[4]=%q", rows[4], clicks[4])
	}
	// Every rendered session row has a map entry pointing at a session
	// or roster target — the frame and the click map cannot skew.
	for row, target := range clicks {
		if target != "$1" && target != "$2" && !strings.Contains(target, ":@") {
			t.Fatalf("row %d maps to unknown target %q", row, target)
		}
	}
}

func TestFrameRosterEntriesAndJumpTargets(t *testing.T) {
	out := Frame(twoSessionInput())
	rows := frameRows(t, out.Body)
	clicks := mapRows(t, out.Map)

	// Roster flows directly after the fleet section (alpha rows 1-3,
	// beta rows 4-5 → header at 6, the slop row separating) with the
	// ruled header, then two-line entries with a gap row; name and
	// detail share the SESSION:WINDOW jump target.
	if !strings.Contains(rows[6], "agents") {
		t.Fatalf("roster header missing after the fleet: %q", rows[6])
	}
	if _, ok := clicks[6]; ok {
		t.Fatal("the roster header must not be clickable")
	}
	if !strings.Contains(rows[7], "codex:review") {
		t.Fatalf("first roster entry should be the self session's agent, got %q", rows[7])
	}
	if clicks[7] != "$1:@3" || clicks[8] != "$1:@3" {
		t.Fatalf("roster rows must jump to session+window: %q / %q", clicks[7], clicks[8])
	}
	if !strings.Contains(rows[11], "opus · beta") {
		t.Fatalf("detail line should read model · project, got %q", rows[11])
	}
	if clicks[10] != "$2:@9" {
		t.Fatalf("second entry target: %q", clicks[10])
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
		FrameWindow{ID: "@4", Name: "w", Glyph: "●", DotHex: "#9ece6a"},
		FrameWindow{ID: "@5", Name: "w", Glyph: "✗", DotHex: "#f7768e"},
	)
	out := Frame(in)
	rows := frameRows(t, out.Body)
	// The fleet NAME row is the one carrying the dots; the roster's dim
	// detail row also contains the session name, so match on both.
	var name string
	for _, content := range rows {
		if strings.Contains(content, "verylongname") && strings.ContainsAny(content, "●✗") {
			name = content
		}
	}
	if !strings.Contains(name, "…") {
		t.Fatalf("long name must truncate: %q (rows %v)", name, rows)
	}
	if strings.Count(name, "●")+strings.Count(name, "✗") != 3 {
		t.Fatalf("all three dots must survive a long name: %q", name)
	}
	// The row must fit the pane: text budget is width-3, and the name
	// budget shrank by 2 per dot, so the row never wraps and the click
	// map below cannot skew.
	if n := len([]rune(name)); n > in.Width {
		t.Fatalf("name row overflows the pane: %d runes %q", n, name)
	}
}

func TestFrameRosterOverflowCount(t *testing.T) {
	in := FrameInput{Width: 30, Height: 12, SelfSession: "$1"}
	var wins []FrameWindow
	for i := range 6 {
		wins = append(wins, FrameWindow{
			ID: fmt.Sprintf("@%d", i), Name: fmt.Sprintf("agent%d", i), Glyph: "●", DotHex: "#9ece6a",
		})
	}
	in.Sessions = []FrameSession{{ID: "$1", Name: "p", Path: "/p", Windows: wins}}
	out := Frame(in)
	body := frameRows(t, out.Body)
	var overflow string
	for _, content := range body {
		if strings.Contains(content, "more") {
			overflow = content
		}
	}
	// name row 1 + slop row 2 → header at 3; height 12 reserves the
	// footer row, leaving avail 7 = two slots; slot two becomes the
	// count: 6 agents − 1 shown = 5 hidden.
	if !strings.Contains(overflow, "+5 more") {
		t.Fatalf("overflow slot wrong: %q (body %v)", overflow, body)
	}
}

func TestFrameLongFleetPushesRosterDown(t *testing.T) {
	in := FrameInput{Width: 30, Height: 18, SelfSession: "$1"}
	for i := range 4 {
		in.Sessions = append(in.Sessions, FrameSession{
			ID: fmt.Sprintf("$%d", i+1), Name: fmt.Sprintf("proj%d", i), Path: "/p", Branch: "main",
			Windows: []FrameWindow{{ID: "@1", Name: "a", Glyph: "●", DotHex: "#9ece6a"}},
		})
	}
	out := Frame(in)
	rows := frameRows(t, out.Body)
	headerRow := -1
	for row, content := range rows {
		if strings.Contains(content, "agents") {
			headerRow = row
		}
	}
	// 4 sessions × 3 rows each fill rows 1-12 (each ending in its slop
	// row); the header flows straight on at row 13, the last slop row
	// as the separator.
	if headerRow != 13 {
		t.Fatalf("roster header must flow after the fleet at row 13, got %d", headerRow)
	}
}

func TestFrameTinyPaneSkipsRoster(t *testing.T) {
	in := twoSessionInput()
	in.Height = 8 // fleet fills it; no room for header + one entry
	out := Frame(in)
	if strings.Contains(out.Body, "agents") {
		t.Fatal("a pane too short for one entry must skip the roster")
	}
}

func TestFrameAttentionAndFacts(t *testing.T) {
	in := twoSessionInput()
	in.Sessions[0].Windows[0].Attn = true
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
	in.Sessions[1].Windows[0].Name = "evil\x1b]0;owned\x07name"
	out := Frame(in)
	if strings.Contains(out.Body, "\x1b]") {
		t.Fatal("a window name must not smuggle raw escape sequences into the frame")
	}
	if !strings.Contains(out.Body, "⟨U+001B⟩") {
		t.Fatal("hostile bytes should render escaped, not vanish silently")
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

func TestParseFrameData(t *testing.T) {
	us := "\x1f"
	data := strings.Join([]string{
		"G" + us + "42" + us + "20" + us + "$7",
		"S" + us + "$7" + us + "alpha" + us + "/work/a" + us + "projid",
		"W" + us + "$7" + us + "●" + us + "#9ece6a" + us + "1" + us + "@1" + us + "claude" + us + "1" + us + "opus",
		"W" + us + "$999" + us + "●" + us + "x" + us + "0" + us + "@2" + us + "orphan" + us + "0" + us + "", // unknown session: dropped
		"bogus line",
		"",
	}, "\n")
	in := ParseFrameData(data)
	if in.Width != 42 || in.Height != 20 || in.SelfSession != "$7" {
		t.Fatalf("geometry: %+v", in)
	}
	if len(in.Sessions) != 1 || len(in.Sessions[0].Windows) != 1 {
		t.Fatalf("sessions: %+v", in.Sessions)
	}
	w := in.Sessions[0].Windows[0]
	if w.Name != "claude" || !w.Attn || !w.Active || w.Model != "opus" || w.ID != "@1" {
		t.Fatalf("window: %+v", w)
	}
}

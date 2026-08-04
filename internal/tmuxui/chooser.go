package tmuxui

import (
	"fmt"
	"regexp"
	"strings"
)

// The agents chooser — the tray `+` cell's menu (docs/tui-layout.md
// "Launch surfaces"): one entry per installed CLI, launch what's down,
// reach what's up. This file decides WHAT each entry does and renders
// the `vibe _chooser` porcelain; the host-side chooser.sh keeps the
// tmux mechanics (display-menu composition, the click dispatch) — the
// frame-renderer split, so the reach-vs-launch verdicts are
// table-tested in one place.

// ChooserWindow is one viewer window of the choosing session, joined
// against `vibe ps` truth by the @vibe_session stamp state-render.sh
// maintains.
type ChooserWindow struct {
	ID      string // tmux window id (@N)
	Session string // the container-side session it views
}

// ChooserInput is one chooser render: the manifest truth (installed
// CLIs, the default), the project-scoped `vibe ps` cache rows, and the
// choosing session's viewer windows.
type ChooserInput struct {
	Default string   // agent.cmd — the address grammar's bare `agent`
	Kinds   []string // image.agents order
	Agents  []AgentEntry
	Windows []ChooserWindow
}

var windowIDRe = regexp.MustCompile(`^@[0-9]+$`)

// ParseChooserWindows parses the window porcelain chooser.sh pipes in,
// one `W<US>window_id<US>session` record per line; malformed lines are
// dropped, never errors.
func ParseChooserWindows(data string) []ChooserWindow {
	var out []ChooserWindow
	for line := range strings.SplitSeq(data, "\n") {
		f := strings.Split(strings.TrimRight(line, "\r"), fleetSep)
		if len(f) != 3 || f[0] != "W" {
			continue
		}
		out = append(out, ChooserWindow{ID: f[1], Session: f[2]})
	}
	return out
}

// Chooser renders the `vibe _chooser` porcelain, one menu item per
// installed CLI (manifest default first) plus one `:cold` variant per
// CLI that supports it (after the warm block — 2026-07-28, Chris,
// superseding the "cold is the tray's business" call: the tray only
// REACHES cold sessions, launching one had no tui door at all):
//
//	1<US>label<US>key<US>verb<US>arg
//
// verb/arg is the click dispatch: `launch` runs `vibe agent` and
// `launcha` runs `vibe agent -a arg` (arg is the kind — also the new
// viewer window's name); `launchc`/`launchac` are their --cold twins;
// `attach` hands arg (a session address) to the attach-only spawn;
// `jump` selects arg (a window id) — an existing viewer is reused,
// never doubled. The glyph carries the recorded state (a dead session
// keeps its ✗ beside a `launch` note), and the note names what the
// click does, so "new" can never silently mean "second viewer on the
// same session".
//
// Entries are only as fresh as the fetch cache, and a stale verdict
// stays safe by construction: `vibe agent` reattaches (-A) rather than
// double-launching, and the attach-only spawn never starts a CLI.
func Chooser(in ChooserInput) []string {
	byAddr := make(map[string]AgentEntry, len(in.Agents))
	for _, ag := range in.Agents {
		if _, dup := byAddr[ag.Session]; !dup {
			byAddr[ag.Session] = ag
		}
	}
	viewer := make(map[string]string, len(in.Windows))
	for _, w := range in.Windows {
		if w.Session == "" || !windowIDRe.MatchString(w.ID) {
			continue
		}
		if _, dup := viewer[w.Session]; !dup {
			viewer[w.Session] = w.ID
		}
	}

	kinds := make([]string, 0, len(in.Kinds)+1)
	if in.Default != "" {
		kinds = append(kinds, in.Default)
	}
	for _, k := range in.Kinds {
		if k != in.Default {
			kinds = append(kinds, k)
		}
	}

	var lines []string
	idx := 0
	add := func(kind, name, addr, launchVerb string) {
		verb, arg, note, glyph := launchVerb, kind, "launch", string(StateStopped)
		if ag, ok := byAddr[addr]; ok {
			if g, _, styled := AgentStyle(ag.State); styled {
				glyph = g
			}
			if AgentLive(ag.State) {
				if wid, ok := viewer[addr]; ok {
					verb, arg, note = "jump", wid, "open"
				} else {
					verb, arg, note = "attach", addr, "attach"
				}
			}
		} else if wid, ok := viewer[addr]; ok {
			// No cache row (cold or missing fetch cache) but a stamped
			// viewer window exists: reach it. The launch fallback would
			// run `vibe agent`, whose -A semantics reattach — a second
			// viewer on the same session, the exact dup the chooser
			// exists to prevent. A recorded-dead session never lands
			// here (its row is in the cache), so dead still launches.
			verb, arg, note = "jump", wid, "open"
		}
		key := ""
		if idx < 9 {
			key = fmt.Sprintf("%d", idx+1)
		}
		idx++
		label := fmt.Sprintf("%s %s · %s", glyph, name, note)
		lines = append(lines, strings.Join([]string{"1", label, key, verb, arg}, fleetSep))
	}
	for _, kind := range kinds {
		// Kinds land in window names, argv, and menu commands: the
		// schema's closed set is already safe, but vet anyway — this
		// porcelain's consumers turn fields into shell words.
		if !SessionNameRe.MatchString(kind) {
			continue
		}
		addr, verb := "agent", "launch"
		if kind != in.Default {
			addr, verb = "agent-"+kind, "launcha"
		}
		add(kind, kind, addr, verb)
	}
	// The cold block: same reach-vs-launch verdicts on the -cold
	// address, only for CLIs agent-session.sh knows how to start
	// without repo instruction files.
	for _, kind := range kinds {
		if !coldKinds[kind] || !SessionNameRe.MatchString(kind) {
			continue
		}
		addr, verb := "agent-cold", "launchc"
		if kind != in.Default {
			addr, verb = "agent-"+kind+"-cold", "launchac"
		}
		add(kind, kind+":cold", addr, verb)
	}
	return lines
}

// coldKinds are the CLIs `vibe agent --cold` can start without repo
// instruction files — the other end of agent-session.sh's
// instruction-skip case (claude --safe-mode, codex
// project_doc_max_bytes=0); keep the two lists in lockstep, and keep
// grok OUT until that script learns its skip flag (it exits 2 today).
var coldKinds = map[string]bool{"claude": true, "codex": true}

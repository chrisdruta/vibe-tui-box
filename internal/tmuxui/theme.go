package tmuxui

import "strings"

// The one source of truth for every tui color and glyph. Two payload
// renderings derive from this file at generate time
// (internal/payload/gen): payload/host/scripts/theme.sh for the bash
// renderers, and the @thm_* block spliced into
// payload/host/tmux-tui.conf for tmux (which cannot source bash). Edit
// here, then `go generate ./internal/payload` — the manifest drift
// gate fails CI on a stale rendering.
//
// Two glyph vocabularies live here ON PURPOSE — they encode different
// things and only share the ● glyph:
//   - agent-session states (AgentStates): working/attention/idle/…,
//     fed by the container title channel and ps records, drawn as the
//     dots on tabs, borders, and the sidebar roster.
//   - container/project states (StateToken): running/stopped/stale/
//     none, computed from Docker truth vs the approved candidate,
//     drawn by the engine renderers (_state, _fleet).

// ThemeColor is one palette entry; Name is the suffix of both the
// bash VIBE_THM_* variable and the tmux @thm_* option.
type ThemeColor struct {
	Name string
	Hex  string
}

// Palette — navy/periwinkle with a coral accent. Ordered: renderings
// must be deterministic (the payload digest depends on them). bright
// is the high-emphasis foreground for accent-backed surfaces (the
// current tab, menu selection, copy mode) — it exists so no rendering
// hardcodes a literal white outside this file (2026-07-29, the polish
// pass: three conf sites carried #ffffff a palette shift could
// strand).
var Palette = []ThemeColor{
	{"bg", "#0e1421"},
	{"surface", "#1a2440"},
	{"border", "#2a3554"},
	{"fg", "#a9b6d8"},
	{"bright", "#ffffff"},
	{"dim", "#5c6b96"},
	{"blue", "#7aa2f7"},
	{"accent", "#3d59a1"},
	{"coral", "#e8735a"},
	{"green", "#9ece6a"},
	{"yellow", "#e0af68"},
	{"red", "#f7768e"},
}

// PaletteHex returns the hex for a palette name; empty when unknown.
func PaletteHex(name string) string {
	for _, c := range Palette {
		if c.Name == name {
			return c.Hex
		}
	}
	return ""
}

// AgentStateStyle is one arm of the agent-state map (theme.sh's
// vibe_state_style). Pattern is a bash case pattern verbatim — the
// state vocabulary is the callers' contract (see theme.sh's rendered
// comment); Color names a Palette entry.
type AgentStateStyle struct {
	Pattern string
	Glyph   string
	Color   string
}

// AgentStates renders into vibe_state_style's case arms, in order.
var AgentStates = []AgentStateStyle{
	{"working", "●", "green"},
	{"attention", "●", "coral"},
	{"idle", "●", "dim"},
	{"running", "●", "blue"},
	{"exited*", "✗", "red"},
	{"gone | frontend-dead", "◌", "dim"},
}

// AgentStyle resolves an agent-session state to its dot glyph and hex
// color — the Go twin of theme.sh's vibe_state_style, for the surfaces
// the ENGINE draws itself from `vibe ps` truth (the sidebar's
// viewer-less rows, the tray's ghost cells) rather than from the
// per-window options state-render.sh already stamped. Unknown states
// report false; callers draw nothing rather than guess.
func AgentStyle(state string) (glyph, hex string, ok bool) {
	for _, s := range AgentStates {
		// The patterns are bash case arms: " | " alternation, one
		// trailing `*` (exited(RC) carries its code).
		for pat := range strings.SplitSeq(s.Pattern, " | ") {
			if prefix, star := strings.CutSuffix(pat, "*"); star {
				if strings.HasPrefix(state, prefix) {
					return s.Glyph, PaletteHex(s.Color), true
				}
			} else if pat == state {
				return s.Glyph, PaletteHex(s.Color), true
			}
		}
	}
	return "", "", false
}

// AgentSignal reports whether a state asks something of the operator —
// the sidebar's row STYLING (docs/tui-layout.md "The roster" —
// 2026-07-26: every live agent is a row; signal no longer hides,
// it styles). `idle` is presence, not signal: its roster row renders
// dim instead of fg. An unrecognized state is nothing to show.
func AgentSignal(state string) bool {
	switch state {
	case "working", "running", "attention", "gone", "frontend-dead":
		return true
	}
	return strings.HasPrefix(state, "exited")
}

// AgentLive reports whether a state means the inner tmux session is
// alive to attach to — the chooser's reach-vs-launch pivot. exited*
// and gone are dead by definition (their next click launches afresh);
// unknown states also fall to launch, where `vibe agent`'s -A
// semantics keep a wrong verdict safe.
func AgentLive(state string) bool {
	switch state {
	case "working", "running", "attention", "idle":
		return true
	}
	return false
}

// StateToken is the compact one-glyph container/project state for the
// engine renderers.
type StateToken string

const (
	StateRunning StateToken = "●"
	StateStopped StateToken = "○"
	StateStale   StateToken = "◐"
	StateNone    StateToken = "·"
)

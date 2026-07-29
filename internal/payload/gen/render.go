package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chrisdruta/vibe-tui-box/internal/tmuxui"
)

const (
	themeBegin   = "# >>> generated: theme"
	themeEnd     = "# <<< generated: theme"
	winlistBegin = "# >>> generated: winlist"
	winlistEnd   = "# <<< generated: winlist"
	borderBegin  = "# >>> generated: bar-border"
	borderEnd    = "# <<< generated: bar-border"
)

// renderTheme materializes the generated payload pieces: the whole of
// host/scripts/theme.sh from the palette/glyph source of truth
// (internal/tmuxui/theme.go), the marker-delimited blocks inside
// host/tmux-tui.conf (the @thm palette, the tray winlist composed
// around tmux's stock W: construct, the bar border rule), and the
// review-stack theme renderings (container/nvim/lua/vibe/theme.lua,
// container/lazygit/config.yml) — the TUI and the editor read as one
// product. It runs before the manifest walk so the digests capture
// the rendered bytes — the CI drift gate then covers drift with zero
// new machinery.
func renderTheme(root string) error {
	for _, s := range tmuxui.AgentStates {
		if tmuxui.PaletteHex(s.Color) == "" {
			return fmt.Errorf("agent state %q names unknown palette color %q", s.Pattern, s.Color)
		}
	}
	shPath := filepath.Join(root, "host", "scripts", "theme.sh")
	if err := os.WriteFile(shPath, []byte(themeSH()), 0o644); err != nil {
		return err
	}
	luaPath := filepath.Join(root, "container", "nvim", "lua", "vibe", "theme.lua")
	if err := os.MkdirAll(filepath.Dir(luaPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(luaPath, []byte(themeLua()), 0o644); err != nil {
		return err
	}
	ymlPath := filepath.Join(root, "container", "lazygit", "config.yml")
	if err := os.MkdirAll(filepath.Dir(ymlPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(ymlPath, []byte(lazygitYML()), 0o644); err != nil {
		return err
	}
	confPath := filepath.Join(root, "host", "tmux-tui.conf")
	src, err := os.ReadFile(confPath)
	if err != nil {
		return err
	}
	spliced := string(src)
	for _, block := range []struct {
		begin, end, content string
	}{
		{themeBegin, themeEnd, themeBlock()},
		{winlistBegin, winlistEnd, `set -g @vibe_winlist "` + winlist() + "\"\n"},
		{borderBegin, borderEnd, `set -g status-format[0] "#[fg=#{@thm_border}]` + strings.Repeat("─", barBorderWidth) + "\"\n"},
	} {
		spliced, err = spliceBlock(spliced, block.begin, block.end, block.content)
		if err != nil {
			return fmt.Errorf("%s: %w", confPath, err)
		}
	}
	return os.WriteFile(confPath, []byte(spliced), 0o644)
}

// spliceBlock rewrites only the marker-delimited block; everything
// outside stays hand-edited. Missing markers are an error, not a
// silent append — the conf's structure is deliberate.
func spliceBlock(src, begin, end, content string) (string, error) {
	b := strings.Index(src, begin)
	e := strings.Index(src, end)
	if b == -1 || e == -1 || e < b {
		return "", fmt.Errorf("markers missing or reordered (%s … %s)", begin, end)
	}
	return src[:b] + begin + "\n" + content + src[e:], nil
}

func themeBlock() string {
	var b strings.Builder
	for _, c := range tmuxui.Palette {
		fmt.Fprintf(&b, "set -g @thm_%-8s%q\n", c.Name, c.Hex)
	}
	return b.String()
}

// barBorderWidth is the fixed length of the status-format[0] rule —
// wide enough for any plausible terminal; tmux clips the excess.
const barBorderWidth = 1000

// tmux 3.7's stock window-list cells, verbatim from
// `tmux show -g status-format` — the two halves of the `W:` construct.
// On a tmux bump, diff these constants against the new stock output;
// the vibe-specific chrome around them never needs re-reading.
const (
	stockWindowCell = `#[range=window|#{window_index} #{E:window-status-style}` +
		`#{?#{&&:#{window_last_flag},#{!=:#{E:window-status-last-style},default}}, #{E:window-status-last-style},}` +
		`#{?#{&&:#{window_bell_flag},#{!=:#{E:window-status-bell-style},default}}, #{E:window-status-bell-style},` +
		`#{?#{&&:#{||:#{window_activity_flag},#{window_silence_flag}},#{!=:#{E:window-status-activity-style},default}}, #{E:window-status-activity-style},}}]` +
		`#[push-default]#{T:window-status-format}#[pop-default]` +
		`#[norange default]#{?loop_last_flag,,#{E:window-status-separator}}`
	stockCurrentWindowCell = `#[range=window|#{window_index} list=focus ` +
		`#{?#{!=:#{E:window-status-current-style},default},#{E:window-status-current-style},#{E:window-status-style}}` +
		`#{?#{&&:#{window_last_flag},#{!=:#{E:window-status-last-style},default}}, #{E:window-status-last-style},}` +
		`#{?#{&&:#{window_bell_flag},#{!=:#{E:window-status-bell-style},default}}, #{E:window-status-bell-style},` +
		`#{?#{&&:#{||:#{window_activity_flag},#{window_silence_flag}},#{!=:#{E:window-status-activity-style},default}}, #{E:window-status-activity-style},}}]` +
		`#[push-default]#{T:window-status-current-format}#[pop-default]` +
		`#[norange list=on default]#{?loop_last_flag,,#{E:window-status-separator}}`
)

// winlist composes the tray middle: list scaffolding, the stock window
// list, the agent-truth ghost cells, and the clickable new-window
// cell. (The ▤ dock cell moved OUT to the bar's left cluster beside
// the brand — 2026-07-29 polish pass: spliced here it floated with the
// absolute-centred tabs, a global chrome control reading as part of
// the window list, while the layout doc's mockup always drew it at
// left.) Cells deliberately avoid commas — one attribute per #[...]
// block — so the construct survives #{?…} comma-parsing wherever it
// is spliced.
//
// The ghost cells are a whole rendered fragment, not a construct: the
// sidebar's frame renderer joins `vibe ps` truth against this server's
// windows and publishes the cells as the session's @vibe_ghosts
// (internal/tmuxui/frame.go ghostCells). #{E:} expands it here the same
// way the tray swaps in the cheatsheet — so container-side sessions
// with no window are one click away without a second #() engine splice
// in the conf.
func winlist() string {
	return `#[list=on align=#{status-justify}]` +
		`#[list=left-marker]<#[list=right-marker]>#[list=on]` +
		`#{W:` + stockWindowCell + `,` + stockCurrentWindowCell + `}` +
		`#{E:@vibe_ghosts}` +
		// One default-background space before the + cell: ghost cells
		// and the + share the surface color, and without the gap the
		// last ghost and the button fuse into one block.
		` #[range=user|newwin]#[fg=#{@thm_dim}]#[bg=#{@thm_surface}] + #[norange]#[default]`
}

// themeSH renders the whole of theme.sh: palette variables and the
// state map from theme.go, plus the verbatim vibe_fg helper and the
// state-vocabulary contract. Must stay bash-3.2-pure (stock macOS) and
// ShellCheck-clean.
// themeLua renders the palette as a Lua module for the container nvim
// config (payload/container/nvim/init.lua requires 'vibe.theme' and
// feeds it to tokyonight's on_colors).
func themeLua() string {
	var b strings.Builder
	b.WriteString(`-- GENERATED from internal/tmuxui/theme.go — do not edit. Change the
-- Go source and run ` + "`go generate ./internal/payload`" + `; CI fails on a
-- stale rendering (payload manifest drift). theme.sh and the conf's
-- @thm block render from the same source.
return {
`)
	for _, c := range tmuxui.Palette {
		fmt.Fprintf(&b, "  %s = '%s',\n", c.Name, c.Hex)
	}
	b.WriteString("}\n")
	return b.String()
}

// lazygitYML renders the container lazygit config: the engine palette
// plus the ascii-safe posture (no nerd fonts, no icons) the review
// stack pledges. edit.sh points XDG_CONFIG_HOME at
// /vibe/payload/container so lazygit finds this beside the nvim tree.
func lazygitYML() string {
	hex := tmuxui.PaletteHex
	var b strings.Builder
	b.WriteString(`# GENERATED from internal/tmuxui/theme.go — do not edit. Change the
# Go source and run ` + "`go generate ./internal/payload`" + `; CI fails on a
# stale rendering (payload manifest drift).
gui:
  nerdFontsVersion: ""
  showFileIcons: false
  border: rounded
  theme:
`)
	for _, line := range []struct{ key, val string }{
		{"activeBorderColor", "['" + hex("blue") + "', bold]"},
		{"inactiveBorderColor", "['" + hex("border") + "']"},
		{"searchingActiveBorderColor", "['" + hex("yellow") + "', bold]"},
		{"optionsTextColor", "['" + hex("dim") + "']"},
		{"selectedLineBgColor", "['" + hex("surface") + "']"},
		{"inactiveViewSelectedLineBgColor", "['" + hex("surface") + "']"},
		{"unstagedChangesColor", "['" + hex("red") + "']"},
		{"defaultFgColor", "['" + hex("fg") + "']"},
	} {
		fmt.Fprintf(&b, "    %s: %s\n", line.key, line.val)
	}
	b.WriteString(`promptToReturnFromSubprocess: false
`)
	return b.String()
}

func themeSH() string {
	var b strings.Builder
	b.WriteString(`# shellcheck shell=bash
#
# GENERATED from internal/tmuxui/theme.go — do not edit. Change the Go
# source and run ` + "`go generate ./internal/payload`" + `; CI fails on
# a stale rendering (payload manifest drift). The @thm_* block in
# tmux-tui.conf renders from the same source.
#
# vibe theme — the ONE palette + state map for every script renderer:
# sidebar.sh (fleet + agent roster), state-render.sh (dot + state
# colors), spin.sh (the working-dot spinner).
#
# Sourced, never executed. Pure definitions: no subprocesses, no output,
# no set-option mutation; bash-3.2-safe (host + container). Callers may
# run under set -e.

# shellcheck disable=SC2034  # consumed by sourcing scripts
`)
	for _, c := range tmuxui.Palette {
		fmt.Fprintf(&b, "VIBE_THM_%s=%q\n", strings.ToUpper(c.Name), c.Hex)
	}
	fmt.Fprintf(&b, `
# The working dot's animation frames (docs/tui-layout.md "The working
# spinner") — presentation of the working state, never a state glyph;
# space-separated so bash-3.2 callers word-split into an array.
VIBE_SPIN_FRAMES=%q
`, strings.Join(tmuxui.SpinnerFrames, " "))
	b.WriteString(`
# hex (#rrggbb) -> truecolor foreground escape, on stdout.
vibe_fg() {
  local h="${1#\#}"
  printf '\033[38;2;%d;%d;%dm' \
    "0x$(printf '%.2s' "$h")" "0x$(printf '%.2s' "${h#??}")" "0x$(printf '%.2s' "${h#????}")"
}

# The one state -> glyph + color map. Sets vibe_glyph and vibe_state_hex;
# returns 1 on an unknown state (callers pick their own fallback: the host
# renderer drops the event, ps.sh renders a dim dot). The full vocabulary —
# which channel carries which state is the caller's contract:
#   working        agent is doing something          (title channel + records)
#   attention      agent wants a human               (title channel + records)
#   idle           agent alive, nothing pending      (title channel + records)
#   exited*        recorded exit; ps.sh suffixes the code
#   running        alive is all we know (hookless/pre-identity runs; ps.sh)
#   gone           no live carrier, no recorded exit — killed too hard (ps.sh)
#   frontend-dead  the docker-exec viewer died; the run may live (host UI)
vibe_state_style() {
  case "$1" in
`)
	for _, s := range tmuxui.AgentStates {
		fmt.Fprintf(&b, "    %s) vibe_glyph=%q vibe_state_hex=\"$VIBE_THM_%s\" ;;\n",
			s.Pattern, s.Glyph, strings.ToUpper(s.Color))
	}
	b.WriteString(`    *) return 1 ;;
  esac
}
`)
	return b.String()
}

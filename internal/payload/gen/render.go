package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chrisdruta/vibe-tui-box/internal/tmuxui"
)

const (
	themeBegin = "# >>> generated: theme"
	themeEnd   = "# <<< generated: theme"
)

// renderTheme materializes the palette/glyph source of truth
// (internal/tmuxui/theme.go) into its two payload renderings: the whole
// of host/scripts/theme.sh, and the marker-delimited @thm block inside
// host/tmux-tui.conf. It runs before the manifest walk so the digests
// capture the rendered bytes — the CI drift gate then covers palette
// drift with zero new machinery.
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
	confPath := filepath.Join(root, "host", "tmux-tui.conf")
	src, err := os.ReadFile(confPath)
	if err != nil {
		return err
	}
	spliced, err := spliceTheme(string(src))
	if err != nil {
		return fmt.Errorf("%s: %w", confPath, err)
	}
	return os.WriteFile(confPath, []byte(spliced), 0o644)
}

// spliceTheme rewrites only the marker-delimited block; everything
// outside stays hand-edited. Missing markers are an error, not a
// silent append — the conf's structure is deliberate.
func spliceTheme(src string) (string, error) {
	begin := strings.Index(src, themeBegin)
	end := strings.Index(src, themeEnd)
	if begin == -1 || end == -1 || end < begin {
		return "", fmt.Errorf("theme markers missing or reordered (%s … %s)", themeBegin, themeEnd)
	}
	var b strings.Builder
	b.WriteString(src[:begin])
	b.WriteString(themeBegin + "\n")
	for _, c := range tmuxui.Palette {
		fmt.Fprintf(&b, "set -g @thm_%-8s%q\n", c.Name, c.Hex)
	}
	b.WriteString(src[end:])
	return b.String(), nil
}

// themeSH renders the whole of theme.sh: palette variables and the
// state map from theme.go, plus the verbatim vibe_fg helper and the
// state-vocabulary contract. Must stay bash-3.2-pure (stock macOS) and
// ShellCheck-clean.
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
# today sidebar.sh (fleet + agent roster); state-render.sh and ps.sh
# rejoin it when their feeders return (BACKLOG).
#
# Sourced, never executed. Pure definitions: no subprocesses, no output,
# no set-option mutation; bash-3.2-safe (host + container). Callers may
# run under set -e.

# shellcheck disable=SC2034  # consumed by sourcing scripts
`)
	for _, c := range tmuxui.Palette {
		fmt.Fprintf(&b, "VIBE_THM_%s=%q\n", strings.ToUpper(c.Name), c.Hex)
	}
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

# TUI layout spec

The BACKLOG's layout pass demanded a written spec before any wiring —
layout arithmetic is where the sidebar bugs have lived (click-map skew,
dot wrapping, resize ballooning). This is that spec: the decided shape
of the tui chrome, its customization surface, and the resize rules.
Wiring follows it; disagreements edit this file first.

Floor: tmux ≥ 3.4 (styles containing formats, user mouse ranges);
`status-format[0]` is regenerated against the stock 3.7 format.

## Decisions

### Bar: bottom, one line — the system tray (supersedes "top", 2026-07-25)

The bar moved to the bottom and became the tray: branding start button,
window cells, engine state, clock. The original top call ("the dock
owns the bottom edge") is superseded by operator decision — with the
terminal app's own tabs at the top of the screen, a top bar stacked two
tab strips; at the bottom the collapsed dock strip and the bar read as
one chrome band, and the taskbar muscle memory is worth more than the
strip-stacking concern. Still single-line: the keybind cheatsheet swaps
*into* the middle while the prefix is held (`#{client_prefix}` over
`#{E:@vibe_cheat}` / `#{E:@vibe_winlist}` — option indirection keeps
the stock `W:` construct out of the conditional's comma parsing), so
hints never cost a row. Top-preferrers set `status-position top` in the
user conf.

### Segment inventory (left → right)

| Segment | Content | Source |
| --- | --- | --- |
| branding | `🥡 vibe` start button — click opens the palette | `status-format[0]` user range `brand` (conf) |
| tabs | per-window `dot name` (index dropped; `M-1..9` still navigate), attention flash | window-status formats via `@vibe_winlist` (conf) |
| `+` cell | clickable — opens the palette (the "new" chooser) | `@vibe_winlist` user range `newwin` (conf) |
| cheatsheet | key hints, shown only while prefix held (replaces tabs) | `@vibe_cheat` (conf) |
| prefix/copy | `⌨` / `copy` indicators | stamped `status-right` (`vibe tui`) |
| engine state | state glyph, `▲n` only when pending > 0; click opens the request list | `#(vibe _state)` splice in user range `req` |
| clock | `%H:%M` | stamped `status-right` |

The bar never carries project identity: the sidebar and the OS window
title (`@vibe_name`) own it, and the ID-derived session name appears in
no chrome. The palette itself lives in `scripts/palette.sh` — one
definition serving `prefix+Space` and both clickable cells.

Change from the as-built state: **`vibe _state` output becomes display
form** — the leading protocol version and the always-present pending
integer (`1 ● 2`) were leaking into the bar; the splice is verbatim, so
the fix lives in the renderer: glyph, then `▲n` only when n > 0.
`_state`'s consumer is a display surface and nothing else; there is no
compatibility to keep.

No new `#(...)` engine splices in the conf — the one `_state` splice at
`status-interval 5` is the budget. Everything richer belongs to the
sidebar's cached engine layer.

### Knobs

All knobs are tmux user options, defaulted with `set -goq` so a
`prefix+R` reload never clobbers a live value. Documented set:

| Option | Default | Consumer | Meaning |
| --- | --- | --- | --- |
| `@vibe_sidebar_on` | `1` | sidebar.sh | global sidebar toggle |
| `@vibe_sidebar_w` | `30` | sidebar.sh (+fit hook) | sidebar chrome width, cols |
| `@vibe_dock_size` | `30%` | dock.sh | expanded dock height (rows or `%`) |
| `@vibe_engine_refresh` | `30` | sidebar.sh | unprompted engine refetch, seconds |

Deliberately **not** knobs: bar position, segment order, theme accents.
`status-position` cannot take a format, so an option can't drive it
from the payload conf — and a per-property option surface would grow
without bound. Instead:

### The user conf hook

`vibe tui` (materializeTuiConf) appends, after the payload conf body:

    source-file -q ~/.config/vibe/tui.conf

That file is the sanctioned customization point — the full tmux
language (bar position, accent overrides, extra binds), applied last so
it wins. The store-owned conf is never forked, re-materialization never
eats user edits, and `-q` keeps a missing file silent. Anything a knob
would micro-manage lives here instead.

### Default arrangement

As built, now written down: agent pane dominant; sidebar far left at
`@vibe_sidebar_w` fixed cols, one per window kept in lockstep; dock
parked collapsed (1 row) on session create, expanding to
`@vibe_dock_size`; pane borders on top with role-gated dot + title.

### Resize policy: chrome snaps, content stretches

tmux stretches panes proportionally on window resize; chrome must not
inherit that. The `window-resized` hook snaps the sidebar to
`@vibe_sidebar_w` and a collapsed dock to 1 row (already true).
Expanded docks and content panes stretch proportionally and are never
fought after a manual border drag (border drags don't resize the
window — the trigger distinction is the mechanism, keep it).

New rule: sidebar truncation widths **derive from pane width** — the
fixed 12/11-char window-name cut assumes 30 cols; a widened sidebar
must actually show more. Budget: window names get up to a third of the
text budget (min 12), project suffixes the remainder.

### Non-goals

Per-window status bars, a segment-reordering DSL, multiple sidebar
positions, engine calls in format strings. Recorded so the knob
surface stays honest.

## Verification

- `_state` display form: `views_test.go` pins glyph-only and `▲n`
  rendering; status bar shows no bare integers.
- User conf hook: Go test asserts the materialized conf ends with the
  `source-file -q` line; live check — `status-position bottom` in
  `~/.config/vibe/tui.conf` survives `prefix+R`.
- Dock knob: `@vibe_dock_size 20%` honored on first toggle and on
  expand-after-collapse.
- Truncation: at `@vibe_sidebar_w 44`, window names show > 12 chars;
  at 30, exactly today's shape. Click every row type after resizing —
  the skew regression class.

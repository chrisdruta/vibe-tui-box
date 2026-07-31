# vibe-tui-box brand — working notes

Status (2026-07-31): finals shipped. Name `vibe-tui-box` 🥡, CLI stays
`vibe`. The 2026-07-20 Gemini drafts are deleted from the tree (git
history keeps them).

## Shipped assets

- `assets/logo.svg` — primary mark, hand-built vector (not generated):
  line-art takeout box in periwinkle, side panel rendering the TRUE
  cockpit anatomy top to bottom — sidebar (coral gutter bar, project
  name, dim roster rows) beside the coral-bordered agent pane, the
  full-width host dock strip, then the tray (coral dot + `vibe` brand
  cell, centered `● claude` tab pill, dim clock) — chopsticks with
  coral tips, lowercase monospace wordmark. Transparent bg; embedded
  `prefers-color-scheme` swaps the periwinkle line work to accent
  blue on light schemes. Supersedes the 07-20 prompt's v1-era panel
  (top tab strip, left-large/right-narrow split): the whole point of
  the pane-content defect was "mirror the real default", and the real
  default is the 2026-07-31 layout.
- `assets/mark.svg` — small-size variant: box + split geometry only,
  no text; the favicon/social-avatar candidate.
- `hero.png` (HERE, parked — 2026-07-31, retired from the README the
  same day it landed: the operator cooled on the render): the 3D
  kraft-box art, cropped from the 2026-07-20 draft's right half with
  the genAI-typo tagline band cut and the generator watermark patched
  out with the background gradient. Kept as the social-preview
  candidate; its remaining nit is invented screen content
  ("$ monitor", `Prefix: C-b`), fixable only by regenerating (prompt
  below). The README's right frame belongs to the TUI recording now
  (next section).
- `assets/header.svg` (old apple/window/container concept) deleted —
  superseded by the takeout brand.

Palette = the product's own (`internal/tmuxui/theme.go`, the one
source): bg `#0e1421`, periwinkle `#7aa2f7`, accent `#3d59a1`, coral
`#e8735a`, fg `#a9b6d8`.

## TUI recording — the README's right frame (decided 2026-07-31)

Format decision: **animated GIF** (APNG also fine), committed as
`assets/tui.gif` — the README already carries the float slot as a
TODO-marked `<img align="right" width="220">` (currently showing
mark.svg). GitHub constraint that forced this: MP4/WebM render a
player ONLY when uploaded through github.com's web editor, always as
a full-width block — `<video>` in README HTML is sanitized away, so
video can never float. `<img>` is the only floatable element, and it
takes GIF/APNG.

Capture recipe (host-side, WSL):

```sh
# terminal at a modest size first (~110x30) — geometry IS file size
asciinema rec -i 2 tui.cast     # -i 2 caps idle gaps at 2s
#   demo beats, ~20-30s total: vibe tui → sidebar glance →
#   click an agent row → prefix+g lazygit popup → prefix+t dock → detach
agg --font-size 14 \
  --theme 0e1421,a9b6d8,1a2440,f7768e,9ece6a,e0af68,7aa2f7,e8735a,7dcfff,a9b6d8,5c6b96,f7768e,9ece6a,e0af68,7aa2f7,e8735a,7dcfff,ffffff \
  tui.cast tui.gif
gifsicle -O3 --lossy=80 -o assets/tui.gif tui.gif
```

The agg theme is the product palette (bg/fg + a harmonized 16-color
ramp); the TUI itself emits truecolor, so the ramp only styles the
host prompt. Keep the result under ~4–5 MB (trim beats or bump
--lossy before shrinking geometry). Swap the README image src and
delete the TODO comment when it lands.

## Still open

- **ASCII splash easter egg**: hand-build a REAL ANSI/block-character
  splash in theme colors for `vibe tui` first-launch / palette header
  (variation C of the deleted draft sheet was the reference vibe).
  Never generate an image of ASCII.
- **Hero regen (optional polish)**: to fix the invented pane content,
  re-run with: kraft takeout box on `#0e1421`, red script "VIBE TUI",
  terminal screen rising as steam with glyph wisps, screen showing tab
  strip `1·main`, NARROW LEFT sidebar pane + LARGE agent pane, coral
  `#e8735a` active border, thin full-width dock strip at screen
  bottom, NO readable text inside panes, chopsticks beside, NO caption
  text, clean space below.
- **GitHub side (host-only)**: social-preview upload + `gh repo edit
  --homepage`; the container token lacks admin scope.

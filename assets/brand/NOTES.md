# vibe-tui-box brand — working notes (parked 2026-07-21)

Status: name settled (`vibe-tui-box` 🥡, CLI stays `vibe`), two draft
rounds from Gemini saved beside this file. Direction settled, final
assets NOT settled — pick up here.

## Direction (decided)

Two zoom levels of one brand, sharing the box + tmux-panel DNA:

- **Primary mark** (from `2026-07-20-refined-pair.png`, LEFT): line-art
  takeout box whose side panel is a tmux layout; chopsticks garnish;
  lowercase monospace wordmark. Jobs: README badge, favicon, sticker,
  small sizes.
- **Hero art** (RIGHT): 3D kraft takeout box, red "VIBE TUI"
  takeout-script, terminal screen rising as steam with digital-rain
  glyphs. Jobs: README header, social preview. Detail is a feature here.
- **ASCII splash** (variation C in `2026-07-20-logo-variations.png`):
  not a logo — an in-product easter egg. Later: hand-build a REAL
  ANSI/block-character version in theme colors for `vibe tui`
  first-launch / palette header, don't generate an image of ASCII.
- **Rejected**: variation D (generic shipping-carton badge — lost the
  takeout story); virtue-adjective naming (see BACKLOG rename record).

Palette = the product's own (theme block in `payload/host/tmux-tui.conf`,
v1 path was `src/config/tmux-tui.conf`):
bg `#0e1421`, periwinkle `#7aa2f7`, coral `#e8735a`, green `#9ece6a`,
fg `#a9b6d8`.

## Known defects in the current drafts (fix on pickup)

Refined pair, both images:
1. **Hero tagline has genAI typos**: "Vibe coding in box: riceed tmux
   TUI on an isolated devontainer chassis" — missing "a", "riceed",
   "devontainer". Render the tagline in real type over the art, never
   generated text. Correct line: "Vibe coding in a box — riced tmux TUI
   on a secure, isolated devcontainer chassis."
2. **Pane content is invented**: "$ monitor / MEM 70% / CPU 25%" and
   "Tmux Keys Prefix: C-b" — wrong prefix (ours is C-Space / C-a) and
   not our layout. Should mirror the real default: tab strip `1·main`,
   large left pane (agent), narrow right pane (host), coral active-pane
   border, coral status dot + session name left of the tabs.
3. **No small-size variant**: the mark needs a simplified version with
   NO text in the panes (just the split geometry) that survives
   16–32 px; favicon candidates.
4. **No transparent/light variants**: need transparent-bg exports and a
   light-mode check (GitHub READMEs render on both).

## Ready-to-paste iteration prompts

Primary mark:
> Refine this line-art logo (attached, left image): a takeout box drawn
> in periwinkle #7aa2f7 line work on transparent background, its side
> panel rendered as a tmux terminal layout matching: tab strip reading
> "1·main" with one active tab highlighted in muted blue #3d59a1, one
> large left pane and one narrow right pane, active pane border in
> coral #e8735a, a small coral dot + "vibe" session label at the top
> left. No other text inside panes. Chopsticks with coral tips leaning
> bottom-right. Below, lowercase monospace wordmark "vibe-tui-box" in
> #a9b6d8. Also produce a simplified small-size variant: same box, pane
> splits as plain lines, no tab strip, no wordmark.

Hero art:
> Refine this 3D hero image (attached, right image): kraft-cardboard
> Chinese takeout box on dark navy #0e1421, hand-painted red script
> "VIBE TUI" on the front, a dark terminal screen rising out of the box
> like steam, wisps of steam becoming falling glyphs. The screen shows a
> tmux layout: tab strip "1·main", large left pane, narrow right pane,
> coral #e8735a active border — no readable text inside panes.
> Chopsticks beside the box. NO caption or tagline text in the image;
> leave clean space below the box (tagline gets typeset separately).

## When finalized

Drop finals in `assets/` (`logo.svg`/`logo.png`, `header` replacement,
favicon sizes), update the README header + `<img>` alt text, delete the
draft PNGs here (git history keeps them), and consider `gh repo edit
--homepage` / social-preview upload (host-side; container token lacks
admin scope).

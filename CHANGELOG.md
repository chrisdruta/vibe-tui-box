# Changelog

Consumers pin a commit; tags mark intentional upgrade points
(see [docs/updating.md](docs/updating.md)).

## Unreleased

- **`vibe tui` conf ownership is now first-owner-authoritative (host-side,
  no rebuild).** The UI server is styled by whichever project starts it
  (tmux applies `-f` at server start only), but every later launch used to
  overwrite the server-global `VIBE_TUI_CONF`, so prefix+R could reload
  project A's pinned conf over project B's sessions. The launcher now
  adopts the variable only when unset (or when the owner's conf file has
  vanished — self-heal); a later project whose pinned conf is
  content-identical joins silently (same-pin projects never warn), and
  real skew prints a warning naming the owner's conf and the
  `kill-server` handover path instead of silently taking over.
- **Agent-state dots now track background sessions too.** Background/daemon
  fork-sessions of an agent (e.g. Claude background jobs) inherit the
  identity env but not `$TMUX`, so their hook events updated the state
  file (`vibe ps` said `working`) while the tui tab dot tracked only the
  foreground session — the known v1 fidelity gap from the title-channel
  smoke. agent-entry.sh now mints `VIBE_AGENT_CARRIER=tmux|none` alongside
  the identity (same single cmd-array env prefix, computed from the same
  condition that picks the tmux branch), and the hook's title-channel
  guard accepts `$TMUX` *or* an inherited `carrier=tmux`. `DEV_AGENT_TMUX=0`
  runs stay `carrier=none`, so they still can't stomp the title of an
  unrelated same-named tmux session. Live from the checkout; no rebuild,
  no settings re-merge (same hook script, same registrations). Takes
  effect per agent on its next relaunch (running agents keep their
  pre-carrier env).
- **New: revdiff — read-only diff review (rebuild required).** The
  2026-07-21 yazi re-evaluation split the review story in two: yazi stays
  the general read-only browser (and image surface), and
  [revdiff](https://github.com/umputun/revdiff) — a purpose-built,
  read-only-by-construction diff-review TUI — becomes the "review what the
  agent changed" surface. `v` toggles between the final file text and its
  diff; annotations made during review print to stdout on quit — a
  ready-made channel for handing review notes back to an agent. Pinned
  goreleaser binary (amd64+arm64, upstream checksums verified) baked into
  the image; palette entry "review diff (revdiff)" on `r` (runs with
  `--untracked` so brand-new agent files show), or `vibe exec revdiff`
  from any shell; doctor checks the binary. Deliberately NOT a top-level
  `vibe` command while it's a trial — the command surface is ABI, and
  revdiff gets a verb only if it earns harness logic of its own (e.g.
  annotation capture feeding the agent). If the trial holds, its
  annotations may eventually absorb the vibe review verdict flow.
- **`vibe review` / the yazi surface is now locked read-only.** It was
  always meant as a viewing/reviewing surface; now the config enforces it:
  the harness keymap unbinds shell escape and every file operation
  (remove, create, rename, cut, paste — `noop` also hides them from the
  help panel; yazi has no command console, so unbound means unreachable),
  and the openers are replaced wholesale so Enter/`o` view through
  `less -R` — `$EDITOR` and system openers do not exist on this surface.
  Approve/reject (A/R) and all navigation are untouched. Projects keep
  the escape hatch by design: their keymap entries merge in front and can
  deliberately re-bind an operation; a project-owned yazi.toml should
  keep the `[opener]`/`[open]` block to keep the lock. Live from the
  checkout immediately for `vibe review`; the baked copy behind the
  tmux `prefix+i` preview window updates on rebuild.
  `require_command` ran as a bare command under `set -e`, so a detected
  lockfile whose tool wasn't installed aborted bootstrap regardless of
  strictness — the documented warn-and-continue path was unreachable. The
  tool preflight now lives inside `run_step`: strict=1 errors as before,
  strict=0 warns and skips the step. (2026-07 external review.)
- **Fix: a failing project post-start hook now fails `vibe up`.**
  post-start.sh warned and exited 0 unconditionally, so `vibe up` reported
  a ready environment even when the project's own hook said otherwise.
  Under `DEV_BOOTSTRAP_STRICT=1` (the default) the hook's failure now
  propagates; `0` keeps warn-and-continue. Doctor stays advisory in both
  modes — its MISSes describe the environment, they don't mean the start
  failed. (2026-07 external review.)
- **Fix: `.gitmodules` migration residue.** This repo's own dogfood pin
  carried the devcontainer-era section name (`submodule
  ".devcontainer/harness"` with `path = .vibe/harness`) and an SSH URL —
  a fresh public clone + `submodule update` demanded GitHub SSH
  credentials for no reason. Renamed to `".vibe/harness"` with the HTTPS
  URL. `install.sh --force` now also removes the legacy-named section, so
  a forced reinstall on a migrated repo can't leave a stale registration.
  (2026-07 external review.)
- **Fix: per-checkout project identity — same-named checkouts no longer
  share a compose namespace.** The project name was derived from the
  workspace basename alone, so `~/dev/a/app` and `~/dev/b/app` collided on
  the ENTIRE compose project — containers, sidecars, network, image tags —
  and `vibe up`/`vibe down` in one could recreate or tear down the other
  (the docs only ever admitted the agent-state volume overlap). The
  identity is now `vibe-<basename>-<8-hex suffix>`, seeded from the
  canonical checkout path into `.vibe/.project-id` on first use (per
  checkout, auto-ignored via `.git/info/exclude`, worktree-friendly). The
  FILE is the identity: moving a checkout keeps its containers; deleting
  the file regenerates the same suffix in place. Checkouts that already
  ran under the unsuffixed name adopt it automatically (probed via
  compose's own project + working-dir labels, so a *different* same-named
  repo can never trigger adoption), and nothing is persisted when the
  docker daemon is unreachable, so a bad moment can't mint a wrong
  identity. The agent-state volume name intentionally still derives from
  the bare basename — that sharing is documented ABI
  (docs/agent-state.md). verify.sh now covers the full branch matrix
  through the real launcher with a stubbed docker; the in-container
  command dispatch gained a `VIBE_SKIP_CONTAINER_DISPATCH` dev escape
  hatch to make that possible.
- **tmux 3.7b + chafa 1.18.2, built from source (rebuild required).** Debian
  stable pins tmux 3.5a and chafa 1.14.5; both moved from apt to a pinned,
  checksummed source-build stage in the Dockerfile (`TMUX_VERSION`/
  `CHAFA_VERSION` ARGs, `--enable-sixel`). Why: a 2026-07-21 dogfood spike
  showed 3.7b retains sixel images while an agent TUI redraws in the
  adjacent split — the exact case 3.5a degraded to placeholders, and the
  reason the review window is a full window instead of a split (a resize
  still clears images; that reflow behavior is upstream). chafa 1.18.2
  brings `--probe` (yazi's cell-art fallback — doctor's NOTE about it now
  passes) and the newer kitty/passthrough work; source-building also ends
  the missing-upstream-arm64-static problem. Behavior is otherwise
  unchanged: `vibe show` keeps `--passthrough tmux` inside tmux until the
  native-ingest path is validated in-image (see BACKLOG); if it holds, the
  full-window review workaround can be revisited.
- **New: `vibe-svc` — the services session is real now.** The docs promised
  "a services session your post-start hook stands up, `vibe attach` is the
  door in" with no machinery behind it. `vibe-svc NAME COMMAND...` (baked
  into the image; rebuild once after crossing this release) idempotently
  runs a workspace process as window NAME in the shared services tmux
  session — safe on every container start, logs in the scrollback, crashed
  services restart on the next run. It deliberately does NOT load `.env`
  (wrap with `env-run.sh` when a service needs secrets). `vibe attach`'s
  no-argument default changes `main` → `services` so the door and the
  populater agree (the old `main` was always empty — nothing populated it).
  New [docs/services.md](docs/services.md) is the chooser: compose sidecars
  for independent daemons, `vibe-svc` for workspace processes, host-program
  patterns for the rest; the roblox Rojo example now uses it.
- **Sidecar services are first-class: `vibe down` and `vibe status` are
  compose-native.** A project service added to `.vibe/compose.yaml` (a
  database, a cache) always STARTED with `vibe up`, but was invisible to
  `status` and orphaned by `down` — both filtered on the `vibe.project`
  label only the dev service carries. `down` now runs
  `compose down --remove-orphans` (named volumes — agent state and sidecar
  data — survive), degrading to label cleanup when the compose files don't
  parse (a half-migrated repo can never strand containers), and still
  removes legacy devcontainer-era containers. `status` lists every project
  service with a SERVICE column via the compose-project label. Also fixes a
  real bug: the old `status` passed both label filters to one `docker ps`,
  which ANDs them — the table was always empty.
- **Seeded compose: every `INSTALL_*` toggle is now a live rendered line.**
  The template previously mixed three mechanisms — bun/rokit rendered,
  codex/grok/node as commented lines install.sh sed-uncommented, playwright
  as a seeded extension. Now all seven toggles (including the previously
  implicit `INSTALL_CLAUDE_CODE: "true"`) render with their actual values;
  flipping one is edit-in-place + `vibe rebuild`. The codex⇒Node implication
  is noted inline (the Dockerfile auto-installs Node for the npm-distributed
  Codex CLI — `INSTALL_CODEX: "true"` alone always worked). `enable_arg`
  (the comment-uncommenting sed) is gone; extras set render values instead.
  Existing seeded files keep working — this changes what NEW installs seed.
- **Image hooks retargeted: agent images reach the reviewer you actually
  watch.** The Claude Code hook now delivers to a live `vibe review`
  first — `review.sh` registers its yazi (pid + DDS id; `exec` keeps the
  pid, so liveness is a `/proc` check) and the vibe plugin subscribes to
  a `vibe-reveal` DDS kind. Delivery there is a toast plus a remembered
  path with a new `g i` keybind to jump on demand — deliberately no
  auto-reveal, so nothing yanks the cursor/cwd while you browse. The
  tmux `preview` window remains the fallback (no live reviewer) with its
  auto-reveal behavior unchanged; the hook also no longer requires tmux
  when a reviewer is up (works with `DEV_AGENT_TMUX=0`).
- **New: `vibe open [LAYOUT]` — the workspace as native terminal panes.**
  Windows Terminal adapter (prototype): `default` opens agent (70%) |
  shell/review, `agents` opens claude | codex, `tabs` gives the agent a
  whole tab with shell/review stacked in a second tab (portrait monitors;
  Ctrl+Tab toggles). Every pane runs one stable `vibe` command and attaches
  to its own per-variant tmux session, so the terminal owns layout and
  rendering while tmux keeps persistence (close the window, lose nothing).
  Panes adopt the WT profile named after the WSL distro so distro color
  schemes survive (`VIBE_OPEN_PROFILE` overrides; without `-p`, wt paints
  commandline panes with the default profile's looks). Without `wt.exe` it
  prints the per-pane commands — the intended degraded mode and the macOS
  story for now. `VIBE_OPEN_LAYOUT` (host env) sets the no-argument
  default layout; an explicit argument wins. Layouts are hardcoded;
  config-driven layouts are on the backlog.
- **BREAKING: the host `GH_TOKEN` passthrough is gone.** The base compose
  no longer forwards `GH_TOKEN` into the container environment — container
  env is baked at create time and visible to every process, the wrong
  place for a credential. GitHub auth is `gh auth login` inside the
  container (fine-grained PAT pasted once; persists in the agent-state
  volume). If you keep a reference PAT in `.env`, use a neutral name —
  `GH_TOKEN`/`GITHUB_TOKEN` there would override the stored login in every
  `vibe agent`/`vibe run` process (docs/configuration.md). Crossing note
  in docs/updating.md.
- The compose-migration guide in docs/updating.md got fixes earned by
  dogfooding it: seed `.vibe/compose.yaml` from the pre-rendered
  `examples/<preset>/` (the raw template's `@PLACEHOLDER@`s only
  install.sh renders), reseed `.vibe/AGENTS.md` (the old seeded copy
  gives agents retired instructions), guard the root symlink against an
  existing real `vibe` file, macOS-safe `sed -i.bak`, and `./vibe down`
  before the first `./vibe up` (dual-container hazard).
- BACKLOG.md now carries the post-review direction: `vibe open` (host
  terminal-layout adapter, Windows Terminal first) is the first feature
  after v1.0, worktree productization follows, the repository rename is
  scheduled pre-v1.0, and decision records reject the session-backend
  abstraction and defer version-lock machinery.
- **BREAKING: the devcontainer engine is gone — `vibe` drives docker
  compose + docker exec directly.** Host requirements drop to git + docker
  (no Node, no `@devcontainers/cli`, no npx fallback). The container is
  defined by the harness base compose file (`compose/base.yaml`: workspace
  mount, agent-state volume, hardening, environment, `vibe.project` label)
  with the project-owned `.vibe/compose.yaml` merged on top via `-f`
  stacking; the new `vibe config` prints the merged result. `vibe up` runs
  compose and then the lifecycle itself: `post-create.sh` once per
  container (marker at `/var/tmp/.vibe-post-created`), `post-start.sh` on
  every actual start. Everything exec'd runs through `docker exec` (with a
  real pty when the caller has one — something `devcontainer exec` never
  offered).
  - **The consumer layout is now `.vibe/`** (compose.yaml, config.env,
    vibe wrapper, AGENTS.md, project/ hooks, yazi/, harness submodule) and
    install.sh links a root-level `./vibe` symlink — the everyday spelling
    is `./vibe up`. `devcontainer.json` is retired; ports are honest
    compose `ports:` entries (loopback-only policy unchanged), extra env
    and mounts are compose keys, and the `updateRemoteUserUID` behavior
    became an explicit `USER_UID` build arg (WSL's 1000 is the no-op
    default).
  - **Migration is one commit** — docs/updating.md → "Migrating to the
    compose engine": `git mv .devcontainer .vibe`, seed compose.yaml from
    the template, port customizations over, fix hook paths, `vibe up`.
    Agent logins survive: the state-volume name is unchanged (and now
    documented as an ABI). `vibe update`, `repo-root.sh`, `lib.sh`, and
    the review/config walks all still recognize the legacy layout so an
    old project can pull this release and migrate from inside it;
    `vibe status`/`down` also match the old devcontainer-CLI container
    label for cleanup.
  - **Retired**: VS Code `customizations` blocks and per-preset extension
    lists (the harness no longer involves VS Code; WSL Remote/local
    editing work as before), the `features/` directory — playwright-deps
    is now the `INSTALL_PLAYWRIGHT_DEPS` build arg (+ optional
    `PLAYWRIGHT_VERSION` pin), the `GH_TOKEN` passthrough (removed
    entirely — dedicated entry below), and the
    `DEVCONTAINER_CLI_SPEC` override.
  - CI's image-build job uses `./vibe build` (compose) instead of
    installing the devcontainer CLI.
  - **New: project image extensions replace Dockerfile flag creep.** The
    compose base now has a build-only `base` service producing the shared
    image (`${VIBE_PROJECT_NAME}-base`); `dev` runs that tag. A project
    needing system-level tooling chains its own `.vibe/Dockerfile`
    (`FROM ${VIBE_BASE_IMAGE}`, root work at build time, ends
    `USER vscode`) and declares `image: ${VIBE_PROJECT_NAME}-dev` + a
    `build:` block — the launcher sequences base → extension builds
    (`vibe up` builds when images are missing; `vibe rebuild` always
    rebuilds both, cache-honoring). Contract and rationale:
    docs/extending.md; worked examples in `examples/extensions/`
    (playwright, blender — Debian package, amd64+arm64). Consequences:
    `INSTALL_PLAYWRIGHT_DEPS`/`PLAYWRIGHT_VERSION` are **removed** from
    the shared Dockerfile (playwright is the first extension;
    `install.sh --extras playwright` now seeds `.vibe/Dockerfile` +
    `.vibe/.dockerignore` and appends the dev build block), project build
    args move under `services.base.build.args`, and runtime hardening
    (`user: vscode`, cap-drop, no-new-privileges) stays compose-side so no
    extension image can weaken the running container. Compose-native build
    chaining (`additional_contexts: service:`) was evaluated and rejected
    for now: needs compose ≥ 2.33 + opt-in bake and has open ordering/
    profile bugs; the launcher sequencing uses only ancient compose
    features and the on-disk contract can adopt it later unchanged.
  - **New install UX: submodule-first + interactive.** The recommended
    install is now two git-native commands from the project root —
    `git submodule add <url> .vibe/harness` then `.vibe/harness/install.sh`
    — no scaffold clone, no `curl | sh`, no npx: the submodule is the
    delivery mechanism, so everything arrives over git and is pinnable and
    diffable (`./vibe update vX.Y.Z` pins a release after). install.sh
    detects it is running from a project's `.vibe/harness` (target implied,
    submodule step skipped or absorbed, rerun refuses and points at
    `vibe update`). With no arguments on a terminal it interviews: preset,
    optional extras, confirm — any argument keeps exact flag behavior for
    scripts/CI. New `--extras codex,grok,node,playwright` enables those
    build args in the seeded compose.yaml (`playwright` implies `node`);
    the scaffold-clone flow is unchanged for multi-project/development use.
    The onboarding agent prompt now uses the submodule-first flow.
  - **Repo reorganized under `src/`** (same breaking release, so the path
    change costs consumers nothing extra): `Dockerfile`, `compose/`,
    `config/`, `scripts/`, `templates/` moved to `src/*`; entry points
    (`vibe`, `install.sh`, `verify.sh`) stay at the root. Seeded consumer
    references are `.vibe/harness/src/...` accordingly, and the compose
    build context is `.vibe/harness/src` so the Dockerfile's COPY paths
    are unchanged. New top-level **`examples/`**: the exact files each
    preset seeds, rendered by `examples/render.sh` and kept in lockstep
    with `src/templates/` by verify.sh (it diffs them against a real
    install of every preset).
- **Changed: image review is now [yazi](https://yazi-rs.github.io/).** The
  homegrown viewer (`preview-viewer.sh`, ~500 lines of tmux/sixel handling)
  is deleted — dogfooding judged it clunky, and yazi is the same class of
  solution (a compiled program that owns decoding and terminal protocols)
  maintained upstream. Pinned by version + checksum per arch in the
  Dockerfile, with `file(1)` for mime detection. **Rebuild required.**
  - `vibe review [DIR]` opens yazi in the invoking terminal;
    prefix+`i` opens it as the dedicated tmux `preview` window
    (`scripts/review.sh`, baked as `vibe-preview` — same fixed name, so old
    tmux configs keep working).
  - **Review is a first-party yazi plugin (`vibe.yazi`) with status**:
    `A` approves, `R` rejects with an optional note via yazi's input box
    (both unbound in yazi's defaults — `a`/`r` keep create/rename), each
    confirmed by a toast, and judged files carry a persistent ✓/✗ badge
    column (the `verdict` linemode; existing verdicts load as soon as a
    directory is entered, including at startup).
    Verdicts append via the baked `vibe-verdict` helper to
    `.review-decisions.jsonl` beside the reviewed images — a dotfile, so it
    never steals the newest-first hover (`VIBE_REVIEW_DECISIONS` overrides
    the target; `VIBE_PREVIEW_DECISIONS` is gone).
  - **Config is layered**: the harness carries the machinery (plugin, review
    keymap, badge linemode) and updates with the pin; the seeded
    project-owned `.devcontainer/yazi/` overrides `yazi.toml`/`theme.toml`
    wholesale, merges its `keymap.toml` entries in front (project wins on
    conflict), and its `init.lua` runs after the harness's. Existing
    projects adopt the seed during a pin-update reconcile; without it,
    review still works on the harness defaults.
  - The Claude Code hook now reveals images in the running yazi over DDS
    (`ya emit-to`) instead of a flock-guarded queue file.
  - tmux.conf adds `update-environment TERM`/`TERM_PROGRAM` (yazi's
    protocol detection); `vibe show` and its img2sixel/chafa pixel-exact
    one-shot path are unchanged.
- **New: `vibe update [TAG]`.** The recommended update flow from
  docs/updating.md as one command: fetch tags, print the CHANGELOG sections
  between the two pins and the diff stat, check out the newest (or given)
  tag, and **stage** the pin move — never commits, never rebuilds. Reports
  whether a rebuild is required (`Dockerfile` changed) or recommended (baked
  script/config copies changed), and flags `templates/` changes for
  project-owned-file reconciliation. Rolling back is the same command with an
  older tag. Works identically inside the container — agents run
  `bash .devcontainer/harness/scripts/update.sh` (now referenced in the
  seeded `AGENTS.md`) and hand the printed rebuild step to the host.
- **New: `vibe doctor` reports harness pin freshness** — a non-failing `NOTE`
  when the pin is behind the newest already-fetched tag. Offline by design:
  doctor never touches the network; `vibe update` is what fetches.
- **Changed: `vibe agent` / `vibe attach` logic moved container-side** into
  `scripts/agent-entry.sh`. The launcher no longer inlines single-quoted
  `bash -lc` payloads with positional smuggling — the entry script receives
  real argv via `devcontainer exec`. Behavior is unchanged (`--cold`, `-a`,
  tmux sessions, `.env`-in-pane-only all as before); the flags are now
  parsed in-container.
- **New: git-hook wiring is loud.** `DEV_AUTO_GIT_HOOKS` still defaults on
  (it wires only the consuming repo's own `.githooks/`), but the boundary
  crossing is now visible every run: doctor emits a `NOTE` whenever
  `core.hooksPath` is set (the hooks also run host-side via the shared
  mount — see docs/security.md), and post-create logs the wiring when it
  does it.
- **Changed: one repo-root walk for host tools.** The `$PWD` ancestor
  discovery is now shared (`scripts/repo-root.sh`, sourced by `vibe` and
  `update.sh`) instead of duplicated; `lib.sh` documents why lifecycle
  scripts deliberately anchor differently. `verify.sh`'s bash-3.2 gate now
  also covers `update.sh` and the new helper.
- **Changed: the preview hook derives its image-extension regexes from
  `VIBE_IMAGE_EXTS`** in `preview-lib.sh` (sed-extracted, never sourced —
  hook stdout must stay empty) instead of three hardcoded copies.
- **Docs: positioning explicitly owns the terminal affordances**
  (`clip`/`show`/`review`) as part of "the environment"; driving agents
  remains a non-goal. `BACKLOG.md` now carries the real roadmap: the
  reduced-trust profile and the recorded Go-migration triggers for the
  preview subsystem.
- **Fixed: the baked preview lib/config was unreadable by the container
  user** (since v0.7.3) — `COPY --chmod=0644` also applies to the parent
  directory it implicitly creates, so `/usr/local/lib/vibe` ended up 0644
  and untraversable: the baked `vibe-preview` (tmux `prefix+i`) silently
  launched stock yazi with no review keys, and `vibe show`'s baked-lib
  fallback failed. The Dockerfile now normalizes the tree (**rebuild
  required**), and `vibe doctor` reports the readability (MISS on broken
  images) plus a NOTE on the chafa/yazi pairing: chafa < 1.16 lacks
  `--probe`, so yazi's cell-art fallback errors out in terminals without a
  graphics protocol (e.g. the VS Code terminal); sixel-capable terminals
  are unaffected. Debian trixie ships chafa 1.14.5 — upgrading it is an
  open decision (upstream static builds have no arm64; source build adds
  weight).
- Removed the legacy `.devcontainer/dev` exec-bit self-heal (pre-v0.4.0
  wrapper name).

## v0.7.3 — 2026-07-19

- **Changed: image previews render actual pixels where possible.** Small
  `png`/`jpeg`/`gif`/`bmp` images now render through `img2sixel` with
  integer nearest-neighbor upscaling — crisp pixels instead of smooth
  blending, which was exactly wrong for small textures and icons; images
  larger than the pane downscale with lanczos3. `webp`/`avif`/`svg`/`tiff`
  stay on `chafa`. Applies to the preview window, `vibe review` (which now
  probes the host terminal for sixel support and real cell metrics), and
  `vibe show`.
- **Fixed: silent blank previews from lying extensions.** The real format is
  sniffed from magic bytes, never the file name — generated assets are often
  webp bytes named `.jpg`, which previously routed to the wrong decoder and
  rendered nothing.
- **New: render diagnostics.** `vibe show --diag PATH` and the viewer's `d`
  key report sniffed format vs extension, native size, renderer choice, exit
  code, and the renderer's stderr; every render attempt also logs one line
  to a self-truncating debug log
  (`$XDG_RUNTIME_DIR/.vibe-preview-debug.log`).
- **New: shared `preview-lib.sh`** (sniffing / rendering / diagnostics),
  sourced by the viewer and `vibe show`, baked at
  `/usr/local/lib/vibe/preview-lib.sh`. `vibe doctor` now checks
  `chafa`/`img2sixel` and the tmux client's sixel support.
- Default `VIBE_PREVIEW_GLOB` widened to `*.gif *.bmp *.avif` (the glob only
  filters watching; rendering trusts the sniffed format).
- **Rebuild required** (`vibe rebuild`) to bake the lib and the new viewer.

## v0.7.2 — 2026-07-19

- **New: `vibe status` / `vibe down`.** Host-side container lifecycle without
  raw docker incantations: `status` lists this project's container(s) (name,
  state, image, ports); `down` stops & removes the container while leaving
  named volumes (agent state) untouched — `vibe up` recreates it. Both match
  by the devcontainer CLI's `devcontainer.local_folder` label and need a
  docker client on the host.
- **New: `vibe attach [SESSION]`.** Attach (or create) an arbitrary tmux
  session in the container — the door into a long-lived services session a
  project's `project/post-start.sh` stands up (dev servers, watchers, …).
  Session name resolves argument > new `DEV_ATTACH_TMUX_SESSION` config.env
  key (seeded commented-out) > `main`. Replaces per-project attach scripts.

## v0.7.1 — 2026-07-18

- **Changed: the image viewer is passive by default.** Verdict keys and the
  per-image verdict label now exist only when a decisions target is
  configured; without one the viewer just views — the right behavior for the
  everyday case of glancing at a `vibe clip` capture or a prompt paste, where
  "undecided" demanded a decision nobody owed. Review mode activates via
  `VIBE_PREVIEW_DECISIONS` in `config.env` (every instance, including the
  `prefix+i` window) or the new per-batch form below. Rebuild required to
  bake the new viewer.
- **New: `vibe review DIR`** reviews one directory as a batch: watches `DIR`
  (workspace-relative), records verdicts to `DIR/vibe-decisions.jsonl`. Built
  for staged generation pipelines — one directory and one `vibe review` per
  approval gate; stage semantics (regenerate vs refine on reject) stay in the
  project's agent skills.
- **New: reject notes.** In review mode `n`/`x` prompts for an optional
  one-line reason (Enter skips) recorded as a `"note"` field in the verdict
  JSONL — turns reject-and-redo loops from rerolling into steering.
- **Changed: agent onboarding prompt clones fresh.** The paste-prompt in
  [docs/onboarding.md](docs/onboarding.md) no longer looks for (or reuses) a
  local `~/dev` scaffold clone; it always shallow-clones the latest harness
  to a throwaway `/tmp` directory.

## v0.7.0 — 2026-07-18

- **New: image review — `vibe review` and the tmux `preview` window.**
  `scripts/preview-viewer.sh` watches a directory for image batches
  (`VIBE_PREVIEW_DIR` / `VIBE_PREVIEW_GLOB` in `config.env`), renders them
  newest-first with single-key navigation, and appends approve/reject
  verdicts to a JSONL file (`VIBE_PREVIEW_DECISIONS`; append-only, last line
  per path wins) for a pipeline or agent to consume. Run it as `vibe review`
  in any host terminal — chafa renders straight to it, no tmux in the pixel
  path (the reliable mode) — or as a dedicated `preview` tmux window via
  `prefix + i`. Baked into the image as `/usr/local/bin/vibe-preview` —
  rebuild required.
- **Changed: Claude Code image hooks feed the review window** instead of
  popping preview splits — transient splits cannot reliably hold a sixel
  render on tmux 3.5a (client redraws replace images with placeholders;
  passthrough smears next to a busy TUI). The hook ensures the window
  exists (detached, never steals focus) and enqueues the path; the window
  name lights up via `monitor-activity` when unfocused. A prompt paste the
  TUI converts to an `[Image #N]` attachment carries no path in the hook
  payload; the hook falls back to the newest `/tmp/clip-*.png` under 10
  minutes old. `VIBE_PREVIEW_SECONDS` and the 30s debounce are retired.
- **Changed: in-tmux sixel rendering hardened** — the viewer sizes images by
  measuring the emitted sixel raster (chafa's captured-output cell metrics
  are unreliable), centers with margins so the header stays visible, ships
  the image as a self-positioning anchored passthrough envelope, and heals
  redraw-wiped pixels flicker-free a tick later. `vibe show` with no
  argument now also considers the watch directory.

## v0.6.0 — 2026-07-18

- **New: auto image preview in Claude Code sessions** — hooks in
  `templates/claude-settings.json` (`UserPromptSubmit` + `PostToolUse: Read`
  → `scripts/preview-image-hook.sh`) pop a self-closing tmux split whenever
  an image path appears in your prompt or the agent reads an image file
  (focused only for the instant the sixel renders, then focus returns). Tune the duration with `VIBE_PREVIEW_SECONDS` in `config.env`.
  Existing projects adopt the hooks by merging the template block at their
  next pin update.
- **New: `vibe show [PATH]`** — sixel image preview in the terminal, the
  companion to `vibe clip`: with no argument it renders the newest
  `/tmp/clip-*.png` so you can see what an agent is about to look at (agent
  TUIs only show `[Image 1]` placeholders). Also `prefix + i` inside the agent
  tmux session opens the same preview in a transient split pane. Adds `chafa`
  and `libsixel-bin` to the image — rebuild required.

## v0.5.2 — 2026-07-17

- **Fix: `vibe clip` broken on WSL** (v0.5.1 regression) — WSL only shares
  environment variables listed in `WSLENV` with Windows processes, so
  `CLIP_WIN_PATH` (introduced by v0.5.1's injection hardening) was `$null`
  inside `powershell.exe` and the clipboard save crashed — then falsely
  reported success, cascading into a missing-file error. The variable is now
  forwarded via `WSLENV`, the PowerShell step only reports `SAVED` after an
  actual save (real errors are surfaced instead of "No image on the
  clipboard"), and the script verifies the file exists before streaming it
  into the container.

## v0.5.1 — 2026-07-17

- **Security fixes from a code review** (host-boundary hardening):
  - `clip-image.sh` no longer interpolates the destination path into the
    PowerShell or AppleScript it runs — a path containing a quote could break
    out into **host** command execution. The path now travels as an
    environment variable (PowerShell) / run-handler argument (AppleScript).
  - `clip-image.sh` confines workspace-mode writes: the destination is
    resolved with `pwd -P` and rejected if it escapes the real repo root
    (defeating a repo-planted symlink like `.captures -> ../../.ssh`), and an
    existing symlink at the target file is refused.
  - `vibe clip DIR` (workspace mode) no longer auto-starts the container — it
    writes straight to the bind mount, so nothing needs to be running.
  - The agent-command split (`DEV_AGENT_CMD`, `-a`) runs under `set -f`, so a
    value containing `*` can no longer glob-expand repo filenames into agent
    arguments.
  - Launcher symlink resolution replaces GNU-only `readlink -f` with a portable
    loop (restores the stock-macOS bash-3.2 host invariant).
  - post-start's GitHub rewrite now also covers `ssh://git@github.com/` remotes,
    set idempotently (unset-all then add) so restarts don't accumulate values.
  - The `npx @devcontainers/cli` fallback is version-pinned (`@0.87.0`) instead
    of resolving mutable `latest` on the host; override per run with
    `DEVCONTAINER_CLI_SPEC`.
- **Agent-driven update prompt** in [updating.md](docs/updating.md): paste-ready
  prompt that moves the pin, reads the changelog between versions, reconciles
  the project-owned seeded files against the new templates (project values win
  on conflict), and reports what needs a human decision. Companion to the
  onboarding prompt; linked from the README.

## v0.5.0 — 2026-07-17

- **`dev` back-compat shim removed**: `harness/dev` is gone and the seeded
  wrapper execs `harness/vibe` directly. Pre-v0.4.0 installs must replace
  their `.devcontainer/dev` wrapper in the same commit that moves the pin to
  ≥ v0.5.0 — see [updating.md](docs/updating.md) → Crossing the v0.4.0 rename.
- **Login-gated GitHub git wiring**: when (and only when) `gh` is logged in,
  `post-start.sh` wires gh as git's credential helper and rewrites
  `git@github.com:` remotes to HTTPS inside the container — restoring the
  container-local `~/.gitconfig` after every rebuild, so an SSH-cloned repo
  shared with the host stays pushable in-container. The `gh auth login` is the
  opt-in; never logging in leaves git untouched. `vibe doctor` reports the
  state (logged in + wired / not wired / not logged in). configuration.md
  gains a fine-grained-PAT permission quick reference, install.sh prints the
  permission set in its next-steps output, and updating.md documents crossing
  v0.4.0 from older installs (GH_CONFIG_DIR, settings merge, wrapper rename).
  Also: post-start's
  exec-bit self-heal now covers the renamed `vibe` wrapper.

## v0.4.0 — 2026-07-17

- **Per-project `gh` logins**: `GH_CONFIG_DIR` now points into the agent-state
  volume, so `gh auth login` (recommended: paste a per-project fine-grained
  PAT — single repo, Contents read/write, no `workflow` scope) persists across
  rebuilds and stays compartmentalized per project. Host-level `GH_TOKEN`
  forwarding is unchanged but documented as the one-token-everywhere trade;
  `gh auth login` refuses while it is set. See configuration.md → GitHub access.
- **Seeded Claude settings deny `.env` reads**: `Read(./.env)` /
  `Read(./.env.*)` join the sudo/su denies in the seeded
  `.claude/settings.json` — an agent-level guardrail against prompt-injected
  secret reads, not a boundary (see security.md, which also documents the
  `/dev/null`-over-secret-file mount recipe for project secrets agents never
  need). Existing projects keep their own settings file; merge manually.
- **The launcher is now `vibe`** (was `dev`): seeded as `.devcontainer/vibe`,
  real script at `harness/vibe`. `harness/dev` remains as a back-compat shim so
  existing consumer wrappers keep working across a pin bump, and the seeded
  wrapper tries `vibe` then `dev` so it also works against older pins.
  Entries below predate the rename; read their `dev` commands as `vibe`.
- **Global launcher**: `vibe` resolves the target project by walking up from
  the current directory to the nearest `.devcontainer/devcontainer.json`
  (falling back to the project the script lives in) and survives being
  symlinked (`readlink -f`) — one `~/.local/bin/vibe` symlink now serves every
  harness project from any subdirectory. The previously documented host-wide
  symlink was broken.
- **Auto-up**: container commands (`agent`, `shell`, `run`, `exec`, `doctor`,
  `bootstrap`, `clip`) start the container when it isn't running (detected via
  the devcontainer CLI's `devcontainer.local_folder` label, or an exec probe
  when no docker client is present). Start-up progress goes to stderr so
  `vibe run` stdout stays pipeable; a cold `vibe agent` is the whole morning
  routine.
- **Docs: [positioning.md](docs/positioning.md)** — the layer this harness
  occupies vs. agent loops and orchestrator UIs, its principles and non-goals,
  and the recorded decision to keep auth agent-native and per-project (no
  centralized credential store); cross-linked from agent-state and security
  docs.
- **`dev agent --cold`**: fresh-perspective agent session without repo instruction
  files — Claude via `--safe-mode`, Codex via `-c project_doc_max_bytes=0`; agents
  without a known skip mechanism refuse. Cold runs get their own tmux session
  (`<session>-cold`) so they never reattach to a warm one.
- **`dev agent -a/--agent CMD`**: per-invocation agent override (e.g.
  `dev agent -a codex`, composable with `--cold`) without touching
  `DEV_AGENT_CMD`; each override gets its own tmux session (`<session>-codex`).
- **`dev clip [DIR]`**: save the host clipboard image into the container's `/tmp`
  (or a workspace-relative `DIR` to keep it) and put the container path on the
  clipboard — the workaround for Ctrl-V image paste being unreachable from
  inside the container. WSL (PowerShell) and macOS (AppleScript); new host
  helper `scripts/host/clip-image.sh`.
- **Codex plugin for Claude Code auto-installed** when `INSTALL_CODEX=true`:
  post-create adds [openai/codex-plugin-cc](https://github.com/openai/codex-plugin-cc)
  (user scope, persisted in the agent-state volume), giving Claude sessions
  `/codex:review`, `/codex:rescue`, and friends without switching panes. Warns
  instead of failing bootstrap when offline.

## v0.3.0 — 2026-07-12

- **playwright-deps Dev Container Feature** + [browser-automation recipe](docs/browser-automation.md):
  headless Chromium for shell-driven agent browsing (`@playwright/cli`). Feature
  option `version` pins the playwright release that resolves the apt dependency list.
- **`dev agent` can run in a persistent tmux session** (`DEV_AGENT_TMUX`, seeded on
  for new installs; unset = previous behavior). Rerunning attaches; detaching keeps
  the agent alive.
- **Onboarding scaffolding**: seeded `.devcontainer/AGENTS.md` (container rules for
  agents; import via `@.devcontainer/AGENTS.md`), [docs/onboarding.md](docs/onboarding.md)
  with an agent reconcile prompt.
- **Default Claude statusline**: harness-shipped `scripts/statusline.sh` /
  `scripts/subagent-statusline.sh`; `install.sh` seeds `.claude/settings.json`
  (statusline + sudo/su deny) when the project has none.
- **Hardening**: Dockerfile builds with `pipefail` and asserts installed binaries
  (a failed `curl | bash` previously produced a cached layer with the tool
  missing); launcher exec bits are recorded in the git index and self-healed at
  post-start (survives `core.fileMode=false` checkouts).
- verify.sh covers `features/`, seeded files, and index modes; CI workflow added.

## v0.2.0 — 2026-07-12

- macOS support: cross-platform Ollama host helper, arm64-verified image, docs.

## v0.1.0 — 2026-07-12

- Initial release: generic agent dev-container harness (hardened non-root image,
  preset installer, lifecycle scripts, agent-state volume, docs).

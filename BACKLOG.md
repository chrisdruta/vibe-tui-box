# Backlog

Ideas accepted but not scheduled. Items graduate into a release when they get
designed; entries here are one paragraph of intent, not a spec.

- **RESOLVED (2026-07, both phases at once): the devcontainer-engine
  exit.** Implemented as the pre-v1.0 engine swap: `vibe` drives docker
  compose + docker exec directly, the consumer layout is `.vibe/` with a
  root `./vibe` symlink, and the devcontainer CLI/Node host dependency is
  gone (see CHANGELOG Unreleased). Remaining from this item: renaming the
  REPOSITORY itself (the `-devcontainer-` in `vibe-devcontainer-submodule`
  no longer describes it) — GitHub redirects old URLs, but seeded docs and
  the onboarding prompt embed the name, so do it as its own release.

- **RESOLVED (2026-07): interactive installer.** Implemented alongside the
  submodule-first install flow: `install.sh` with no arguments on a tty
  interviews (preset, extras via the new `--extras`
  codex/grok/node/playwright, confirm); any argument or no tty keeps exact
  flag behavior for scripted/CI use.

- **RESOLVED (2026-07): auto-symlink `vibe` into the project root.**
  install.sh now seeds a committed `vibe -> .vibe/vibe` symlink (skipped if
  a real file is in the way); existing projects get it during the compose
  migration. Harmless on Linux/WSL/macOS checkouts; Windows-native
  checkouts see a text file, which was accepted.

- **Reduced-trust profile for unattended runs.** Promised in
  docs/security.md ("planned but not implemented"): a config.env posture for
  long autonomous tasks — no `GH_TOKEN` passthrough (or a minimum-permission
  fine-grained token), `DEV_AUTO_GIT_HOOKS=0` / `DEV_AUTO_INSTALL=0`, and a
  doctor mode that verifies the reduced posture instead of the interactive
  one. Review and push stay host-side.

- **RESOLVED (2026-07, differently): the "rewrite the preview subsystem in
  Go" item.** The trigger fired early — dogfooding judged the homegrown
  viewer clunky — and the resolution beat writing our own binary: the
  review viewer is now yazi (pinned release binary in the Dockerfile),
  which is the same class of solution maintained upstream. The remaining
  harness-owned render code is the small `vibe show` one-shot path
  (preview-lib.sh); if THAT grows or breaks repeatedly, fold it into yazi
  usage or revisit. The host launcher, installer, and lifecycle scripts
  stay bash regardless — they are the bootstrap and must run on stock
  macOS bash 3.2 with nothing installed.

- **RESOLVED (2026-07): reorganize `scripts/` into subdirectories.**
  Superseded by the `src/` reorg that rode along with the devcontainer-engine
  exit (the breaking release made the path moves free): everything
  harness-internal lives under `src/`, entry points stay at the root, and
  `examples/` carries rendered per-preset seeds verified against real
  installs. `src/*` paths are the new public interface (AGENTS.md).

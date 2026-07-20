# Container rules for agents

This project runs inside a hardened container (the vibe harness). Constraints
that will bite you if ignored:

- **No root, no sudo.** The container runs as `vscode` with all capabilities
  dropped. `apt-get install` cannot work at runtime — OS packages go in the
  image via `.vibe/compose.yaml` build args (see the harness base for the
  full set), followed by a rebuild (`./vibe rebuild`, run on the HOST, not
  in here).
- **Never source `.env` into a shell.** Secrets load per-process only:
  `./vibe agent` / `vibe run CMD` on the host, or
  `.vibe/harness/src/scripts/env-run.sh CMD` inside the container.
- **`.vibe/harness/` is a pinned git submodule — never edit it.**
  Project-specific behavior belongs in the project-owned files next to it:
  `compose.yaml` (image build args, mounts, ports), `config.env` (behavior
  toggles), `project/post-create.sh` and `project/post-start.sh` (lifecycle
  hooks), `yazi/` (image-review preferences).
- **Edits to `compose.yaml` or the Dockerfile do nothing until
  `vibe rebuild`** — and that must run on the host. Say so instead of
  retrying in-container.
- **`~/.agents` is a persistent named volume** (agent logins, browser
  binaries); everything else in `$HOME` resets on rebuild. Install user-level
  tools to `~/.local/bin`.
- **Harness updates**: `bash .vibe/harness/src/scripts/update.sh [TAG]`
  (what `vibe update` wraps) fetches tags, prints the CHANGELOG delta, checks
  out the newest (or given) release, and STAGES the pin move — it never
  commits; that stays with the user. Report the staged diff, and when it
  flags a rebuild, ask the user to run that on the host.
- **Diagnostics**: `.vibe/harness/src/scripts/doctor.sh` checks the
  environment; its output from the last start is in `/tmp/dev-doctor.log`.

Reference docs live in `.vibe/harness/docs/` (configuration, usage,
security model, recipes).

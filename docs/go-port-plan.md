# Go port plan — host engine to a compiled `vibe`, tmux stays the UI

Status: SUPERSEDED 2026-07-23 by [architecture-v2.md](architecture-v2.md) —
the strangler/migration sequencing below is retired in favor of a
clean-slate rewrite (no back-compat, no differential period). Step 0
(Go toolchain in the image, module skeleton, CI) shipped and carries over;
the technology decisions (Go, prebuilt attested binaries, tmux stays,
container scripts stay bash) also carry over. Kept for the rationale.

## Why (and why now)

At ~6,650 lines of shell the total is not the problem — the distribution is.
The host-side, security-critical core (`store.sh` 1,097 + `vibe` 606 +
`install.sh` 487 + `verify.sh` 484 + `tui.sh`/`sidebar.sh` 714) is ~2,700
lines of the hardest bash in the repo, pinned to bash-3.2 for stock macOS.
Eight of the fifteen commits before this plan were security fixes for
shell-inherent bug classes (quoting, path resolution, compose-source
scanning). That tax recurs in exactly the code where bugs matter most; a
compiled language removes the bug classes structurally.

The container-side lifecycle scripts are ordinary glue on bash 5 in a
controlled environment and have needed near-zero fixes — they stay shell.

## Decisions (settled here; revisable like everything else)

- **Hybrid, not rewrite.** The Go binary is the engine; tmux remains the UI
  (multiplexing, persistence, attach are free; a bespoke TUI would badly
  reimplement a terminal multiplexer to host the agent's own TUI).
  Container lifecycle scripts remain bash indefinitely (step 5 triggers).
- **Go over Rust.** Cross-compiles the whole matrix from one machine with
  `CGO_ENABLED=0`, no runtime deps, `embed.FS` for templates, the Docker
  ecosystem is Go-native, and compile speed suits a solo iterating dev.
- **Build matrix, not "one universal binary".** macOS cannot run Linux ELF:
  `linux-amd64`, `linux-arm64`, `darwin-arm64` (WSL2 primary, Mac real).
- **Prebuilt release binaries** via goreleaser + SHA256SUMS + GitHub
  attestation from the first release. The store records the binary's sha256
  per version; dev mode builds from the checkout instead. The root-of-trust
  invariant is unchanged: the host never executes container-writable bytes.
- **Submodule retires as the delivery mechanism** (step 3). The read-only
  overmount already means the checkout's submodule content is not what
  runs — the submodule is purely a pin. A `.vibe/harness-version` pin file
  does the same job without submodule UX; `.vibe/` becomes pure config.

## Step 0 — Go toolchain in the image (in progress)

- `INSTALL_GO=false` + `GO_VERSION` pinned with per-arch sha256 in
  `src/Dockerfile`, same pattern as yazi/revdiff; extracted to
  `/usr/local/go`, `PATH` gains `/usr/local/go/bin` and `~/go/bin`.
- Dogfood flips it on in `.vibe/compose.yaml`; `go` joins
  `DEV_REQUIRED_COMMANDS` (doctor shows MISS until the host `vibe rebuild`).
- After the rebuild: root `go.mod`, `cmd/vibe/`,
  `internal/{store,compose,registry,tmuxui}`; CI gains Go build + test +
  golangci-lint beside shellcheck. `src/` stays shell-only.

Exit: `go build ./...` inside the container; CI green.

## Step 1 — CLI skeleton + port the trust core (the big one)

Replaces `store.sh`, the host half of `update.sh`, `self-install.sh`,
`install.sh --self`, and finally the `vibe` dispatcher.

- **Same command surface, same `~/.vibe` layout** (`versions/<sha>/` +
  manifests, `repo.git` mirror, `state/projects/`, lockdirs) so bash and Go
  interoperate throughout the port — strangler, not flag day.
- Shim execs the binary when present, falls back to the script otherwise.
  Port inside-out: env sanitization → manifest verify → atomic materialize
  → mirror/fetch → update flow → first-contact/drift prompts.
- **Differential testing:** verify.sh host suites run against both
  implementations on the same fixtures until the bash side is deleted. The
  `VIBE_SKIP_CONTAINER_DISPATCH` stubbed-docker seam becomes a Go
  `DockerClient` interface with a fake.
- goreleaser release pipeline lands here (matrix above, checksums,
  attestation). `docs/security.md` root-of-trust section is rewritten in
  the same change: verified unit = attested binary + materialized tree.

Exit: shim boots the binary; verify.sh green under both; bash trust core
deleted; tagged (v0.8 material).

## Step 2 — Compose inversion + registry

Today the store *scans* project compose source for host-read/exec bypasses
— an unwinnable blocklist game (three sol rounds). Invert it:

- `.vibe/compose.yaml` stays the project surface, but the binary parses it
  with a real YAML library, validates against an **allowlist schema**
  (build args, declared mount shapes, ports, constrained sidecars), and
  **generates** the effective compose handed to docker. Unknown key = hard
  error pointing at docs. Existing projects already fit the legal subset.
- Delete the bash scanner; bypass hunting becomes schema violation by
  construction.
- `state/projects/` records move to binary-owned JSON (one-time migration
  from strict k=v).

Exit: `vibe config` byte-diffable against the old merge for all presets +
real projects; scanner deleted.

## Step 3 — `vibe init` + submodule → version pin

- `vibe init [--preset minimal|python|bun|roblox]` from any repo: templates
  embedded in the binary, writes `.vibe/`, registers the project, builds;
  `--tui` drops straight into the agent pane at the Claude login.
  Interactive interview keeps parity with today's `install.sh` no-arg flow.
- `.vibe/harness-version` (tag or sha) replaces the submodule pin; the
  store materializes that version from its mirror; `vibe update` keeps its
  exact UX (changelog delta + diff; commit stays yours).
- `vibe migrate` converts submodule projects; the submodule layout stays
  recognized (like the legacy `.devcontainer` path) for a deprecation
  window.

Exit: bare repo → coding agent in under a minute, one command; new projects
carry no submodule.

## Step 4 — Fleet manager + rebuild broker

- **Fleet:** `vibe tui` becomes cwd-independent (registry-backed); project
  picker as a tmux popup fed by `vibe _fleet`; open/close sessions from the
  sidebar. `sidebar.sh`/`state-render.sh`/`statusline.sh` become
  `vibe _sidebar`/`_state` subcommands — the bash-3.2 constraint dies for
  UI code here too.
- **Rebuild broker:** the in-container agent writes a request file in the
  workspace (compose-diff summary + reason); the host state renderer flips
  an attention state on that project's tab/sidebar; a palette action shows
  the diff and the human approves with a keystroke; the host rebuilds and
  relaunches the agent with `--continue` (agent state rides the named
  volume). The request is untrusted *data* — displayed, never executed;
  approval gates everything. The `agents.md` template teaches agents the
  protocol instead of dead-ending at "ask the human to rebuild".

Exit: "add Playwright to this project" typed at the agent inside the TUI
produces an approval prompt on the sidebar and a rebuilt container you are
dropped back into. v1.0 story.

## Step 5 — Container-side absorption (opportunistic, no deadline)

`post-create`/`post-start`/`doctor`/preview stack stay bash. Absorb a
script into the binary only when it trips a trigger: needs real JSON
manipulation, exceeds ~200 lines, or takes a security finding. The Linux
binary is already in the image by then, so absorption is free when earned.

## Cross-cutting (every step)

- verify.sh green before merge; Go tests land with the code they cover.
- Docs (`security.md`, `architecture.md`, `updating.md`) updated in the
  same commit as the behavior; CHANGELOG discipline as today.
- Rough effort split: step 1 ≈ half the total; 0 and 3 are the quick wins;
  2 and 4 are medium.

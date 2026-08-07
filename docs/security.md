# Security model

**Authority: design.** This page is normative — the engine disagreeing
with a claim here is a defect (or a deliberate revision recorded in
BACKLOG.md), never something to paper over by editing this file to
match. The as-built map is [architecture.md](architecture.md); this is
the operator's view of what the engine does and does not protect, and
the single home of the container policy other docs point at.

## The boundary

One container per project. The workload inside — agent CLIs, your code,
anything they install — is untrusted by the host. Every managed
container drops all capabilities and sets `no-new-privileges`; the dev
container additionally runs as non-root `vscode` (sidecars keep their
image's own user under the same closed policy). One narrow,
engine-owned exception: the dns ledger sidecar runs as root with
exactly `NET_BIND_SERVICE` re-granted — the pinned CoreDNS binary
carries file capabilities, and a binary whose file capabilities exceed
the process's permitted set cannot exec under this policy at all. The
grant is inexpressible from `vibe.yaml` and plan validation rejects it
on any other container; `no-new-privileges` stays on. No Docker
socket, no host home, no SSH agent, no host network. Published ports bind loopback
only. The only *writable* host path in the container is the registered
project root at `/workspace`; the engine's own mounts (payload, broker
results) are read-only.

## The host never executes container-writable bytes

Everything the host reads from the workspace — `vibe.yaml`, the env
file, import sources, `.vibe/Dockerfile` — is treated as data: parsed
by bounded, strict parsers (size, depth, node, and entry limits;
unknown fields rejected), then **frozen into an immutable
content-addressed snapshot** before use. Request JSON is the one
workspace input read in place (bounded, data-only) — its security
decision binds to the immutable candidate digest, never to the file
(below). Project lifecycle hooks (`.vibe/hooks/`) execute only *inside*
the container, as workload code; the host never reads or runs them.
Later stages read the snapshot, never the workspace, so what you
approved cannot change under you. Snapshotting itself is
symlink-rejecting and FD-confined to the project root, and aborts on
concurrent mutation.

Engine code and the container payload come from digest-addressed
artifacts under `~/.vibe`, verified against a per-file manifest at
extraction and install. Release downloads are hashed while streaming
and checked against the release's `checksums.txt`; archives may contain
only known entry types at known locations. Know the limit: checksums
from the same origin are corruption detection, not publisher
authentication — native provenance verification (Sigstore, fail-closed)
is a release blocker on the [roadmap](../ROADMAP.md).

## Environment values

`env_file` entries are opaque container data and exec-scoped: they are
injected only into processes started with `vibe run` or the agent CLI,
never baked into the container's ambient environment (a stray shell,
hook, or service window does not inherit them), never merged into a
host process environment, never logged, and never part of the canonical
plan or its digest. Manifest `runtime.env` values are planned
configuration — they *are* container-ambient and in the plan digest;
put secrets in the env file, not the manifest. `vibe exec` passes only
what you give it with `-e`, plus your `TERM` on TTY execs so
full-screen tools render (docs/usage.md).

## Agent-initiated changes

An agent cannot alter its own container. It can write a request file;
polling (`vibe request list`) binds that request to an immutable
candidate built from the *current* frozen inputs, `vibe request show`
renders the agent's text beside the engine's own plan diff, and
approval addresses the candidate digest — not the request file, which
the agent could rewrite after you looked at it. Request text (and any other agent-authored string the
engine displays) is rendered through an encoder that makes control
characters, ANSI escapes, and bidi overrides visible, and interface
chrome is kept structurally separate from that content.

## Inner agent sandboxes are off by design

A consequence of `cap_drop ALL` + `no-new-privileges`: the container
permits no unprivileged user namespaces, so namespace-based sandboxes
cannot start inside it. `bwrap: … Operation not permitted` is this
policy working, not a bug (the trust-layer diagram at the top of
[architecture.md](architecture.md) is the picture).

When the CLIs actually try: **codex sandboxes every command it
executes by default** — any codex invocation at all (interactive,
`codex exec`, and every thread the Claude codex-companion plugin
spawns) attempts bwrap+seccomp unless configured not to, which is why
the seeding below exists at all; **Claude Code** tries only when
`/sandbox` is enabled; Chromium brings its own. The container is the
isolation boundary; a second layer inside it is off-by-design, not
broken-by-surprise:

- **Codex** — a best-effort, detached post-create pass seeds
  `sandbox_mode = "danger-full-access"` into `$CODEX_HOME/config.toml`,
  only when the key is absent (your own setting always wins) — it may
  still be running when a fresh container's first thread starts. Codex
  documents the mode as intended for environments that are externally
  sandboxed — this container is that environment. The pass logs every
  action and no-op to `/var/tmp/vibe-agent-plugins.log`, so "is the
  patch applied?" is a `tail`, not a guess. (Mechanics and the
  companion plugin patch: [configuration.md](configuration.md).)
- **Claude Code** — the payload settings ship
  `enableWeakerNestedSandbox` + `failIfUnavailable: false`, so a
  `/sandbox` enable degrades to the permission-rules fallback instead
  of hard-failing.

Do not weaken the outer container to make an inner sandbox start:
added capabilities are root-shaped, and a userns-permissive profile
exposes the kernel's user-namespace attack surface. If you need an
inner sandbox, run that workload outside vibe.

## Image extensions widen the boundary

`extension: true` sends a project-authored Dockerfile to the Docker
builder. That expands the trusted surface, so the engine narrows it
first — all content must come from the engine-pinned base or the frozen
context ([extending.md](extending.md) has the contract) — and then
requires explicit operator approval of the frozen Dockerfile, per
content digest. The build context is a restricted copy containing only
the Dockerfile and `.vibe/build/`; the env file and manifest never
reach it.

## Terminal passthrough (decision record, 2026-08-06; clipboard part
superseded 2026-08-07)

The UI server runs with `allow-passthrough on` — sixel image preview
(`show-image.sh`) needs raw escape sequences to reach the outer
terminal, and that is a deliberate trade: a passthrough-wrapped escape
sequence authored inside a container can reach your terminal emulator,
and what it can do there is decided by the emulator's own settings,
not by vibe. The one concrete abuse with a tmux-side answer is
clipboard writing (OSC 52 paste-jacking: the agent silently replaces
what you next paste into a host shell). The 2026-08-06 record closed
it with `set-clipboard external` — tmux forwards its own copy-mode
yanks but refuses OSC 52 from pane applications. That lasted one day
in practice: `external` also refuses *legitimate* pane-application
copies, and an account-login flow whose link was only deliverable via
OSC 52 became uncopyable, forcing a live revert to complete the
login. The UI server now sets `set-clipboard on`: container-authored
OSC 52 reaches the system clipboard, and the paste-jacking exposure
is accepted knowingly — the operator judged the usability cost of
`external` (dead pane-copy paths, container nvim yanks, login-link
flows) higher than the risk. The remaining lock is your terminal
emulator's own clipboard-write setting; disable it there if you want
the door shut. Turning `allow-passthrough` off entirely would still
close the wrapped-escape class at the cost of image preview; revisit
if sixel leaves the picture.

## What is *not* protected

- **Egress.** The project network reaches the internet. An agent can
  exfiltrate anything it can read — which includes `/workspace` and the
  env values you configured. Give containers the minimum secrets that
  make the project work. What the engine does provide is *visibility*,
  not enforcement: the per-project DNS ledger (the engine-generated dns
  sidecar's query log, `vibe logs dns`) and the palette's "network
  egress" view with a live-socket sample. Direct-to-IP connections and
  DoH bypass the ledger — the sampler still shows those IPs — and the
  sampler attributes only the container user's processes.
- **The workspace.** The agent can modify your code, including files
  that influence *you* (hooks, scripts you might run on the host).
  Review diffs; untrusted projects belong in disposable checkouts with
  minimal credentials.
- **Docker itself.** The engine trusts the daemon and the images you
  name in `vibe.yaml`. Release attestation covers the engine, not your
  base image or Dockerfile instructions.
- **Approved extensions.** After you approve a Dockerfile, its `RUN`
  lines execute in the builder with network access. Approval is the
  security decision; the engine only makes sure you decide on exactly
  what will run.

# Security model

The full architecture is in
[architecture.md](architecture.md); this is the operator's view of
what the engine does and does not protect.

## The boundary

One container per project. The workload inside — agent CLIs, your code,
anything they install — is untrusted by the host. The container runs as
non-root `vscode` with all capabilities dropped and `no-new-privileges`;
no Docker socket, no host home, no SSH agent, no host network. Published
ports bind loopback only. The only live host path in the container is
the registered project root at `/workspace`.

## The host never executes container-writable bytes

Everything the host reads from the workspace — `vibe.yaml`, the env
file, import sources, `.vibe/Dockerfile`, request JSON — is treated as
data: parsed by bounded, strict parsers (size, depth, node, and entry
limits; unknown fields rejected), then **frozen into an immutable
content-addressed snapshot** before validation or use. Later stages read
the snapshot, never the workspace, so what you approved cannot change
under you. Snapshotting itself is symlink-rejecting and FD-confined to
the project root, and aborts on concurrent mutation.

Engine code and the container payload come from digest-addressed
artifacts under `~/.vibe`, verified against a per-file manifest at
extraction and install. Release downloads are hashed while streaming
and checked against the release's `checksums.txt`; archives may contain
only known entry types at known locations. Know the limit: checksums
from the same origin are corruption detection, not publisher
authentication — native provenance verification (Sigstore, fail-closed)
is a release blocker on the [roadmap](../ROADMAP.md).

## Environment values

`env_file` entries and `runtime.env` values are opaque container data.
They are assigned through Docker API fields and are never merged into a
host process environment, never logged, and never included in the
canonical plan or its digest. `vibe exec` passes only what you give it
with `-e`.

## Agent-initiated changes

An agent cannot alter its own container. It can write a request file;
`vibe request list` binds that request to an immutable candidate built
from the *current* frozen inputs, and approval addresses the candidate
digest — not the request file, which the agent could rewrite after you
looked at it. Request text (and any other agent-authored string the
engine displays) is rendered through an encoder that makes control
characters, ANSI escapes, and bidi overrides visible, and interface
chrome is kept structurally separate from that content.

## Image extensions widen the boundary

`extension: true` sends a project-authored Dockerfile to the Docker
builder. That expands the trusted surface, so the engine narrows it
first — single `FROM ${VIBE_BASE_IMAGE}` (digest-pinned by the engine),
no custom BuildKit frontends, no ADD/ONBUILD/multi-stage, must end as
`vscode` — and then requires explicit operator approval of the frozen
Dockerfile, per content digest. The build context is a restricted copy
containing only the Dockerfile and `.vibe/build/`; the env file and
manifest never reach it.

## What is *not* protected

- **Egress.** The project network reaches the internet. An agent can
  exfiltrate anything it can read — which includes `/workspace` and the
  env values you configured. Give containers the minimum secrets that
  make the project work.
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

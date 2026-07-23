# Usage

Every command runs from anywhere inside a project (the engine walks up
to `.vibe/vibe.yaml`), takes `--json` for a versioned machine-readable
result, and exits with a stable code: 0 ok, 1 failure, 2 usage,
3 invalid configuration, 4 not registered / not found, 5 conflict,
6 external dependency unavailable, 130 interrupted.

## Project lifecycle

| Command | Effect |
| --- | --- |
| `vibe init [--preset NAME]` | Seed `.vibe/` from an embedded preset, register, pin the newest artifact |
| `vibe register [--name NAME]` | Register an existing project |
| `vibe up` | Freeze inputs → compile candidate → reconcile containers → mark approved |
| `vibe rebuild` | Same, but recreate containers even when already in sync |
| `vibe down [--volumes]` | Stop and remove containers and network; volumes survive unless asked |
| `vibe status` | Containers vs the approved candidate (running / stopped / stale) |
| `vibe config` | Print the canonical plan JSON compiled from current inputs |
| `vibe ps` | All registered projects |
| `vibe forget` | Remove the registration; the workspace is untouched |

`up` is idempotent: unchanged inputs produce the identical candidate
digest and touch nothing. A changed candidate replaces containers;
a failed `up` never moves the approved-candidate pointer.

## Working inside the container

| Command | Environment |
| --- | --- |
| `vibe exec [-u USER] [-w DIR] [-e K=V]… -- CMD ARGS…` | explicit `-e` entries only |
| `vibe run -- CMD ARGS…` | the env file frozen in the approved candidate, then `-e` |
| `vibe agent` | the manifest's agent CLI, with the frozen env file |
| `vibe shell` | first of zsh/bash/sh found in the container, as a login shell |
| `vibe attach` | the container's main process |
| `vibe bootstrap` | verify `bootstrap.required` tools exist in the container |

Argv is preserved exactly — there is no shell-string form. The container
process's exit code becomes `vibe`'s exit code. Interactive sessions get
a raw TTY with resize forwarding.

## The TUI

`vibe tui` opens (or joins) the project's tmux session with `vibe agent`
in the main window and the engine state in the status line: `●` running,
`◐` running but stale candidate, `○` stopped, plus a pending-request
count. Sessions are named from the project ID, so display renames never
strand a session.

## Rebuild requests (the broker)

Agents cannot change the container they run in. Instead they write
`.vibe/requests/<id>.json`:

```json
{"format": 1, "id": "add-port", "kind": "rebuild",
 "reason": "rojo needs 34872", "summary": "add 127.0.0.1:34872:34872"}
```

On the host:

```sh
vibe request list             # poll; each new request is bound to an
                              # immutable candidate built from current inputs
vibe request show add-port    # sanitized reason/summary + candidate digest
vibe request approve sha256:… [--yes]
vibe request reject  sha256:… [-m "why"]
```

Approval addresses the candidate digest — what was frozen at poll time —
never a filename an agent could rewrite afterwards. Decisions land in a
read-only results mount at `/vibe/results` inside the container. Request
text is untrusted: it renders through the control-character-escaping
encoder everywhere.

## Releases and health

| Command | Effect |
| --- | --- |
| `vibe provision` | Install this binary + embedded payload as an artifact; pin the project |
| `vibe update --version vX.Y.Z` | Download, verify, install a release; swap the host binary |
| `vibe doctor` | Layout, registration, artifact integrity, daemon, containers, lifecycle marker, tmux |
| `vibe version` | Engine version |

## Dev mode (hacking on the engine itself)

Inside an engine checkout:

```sh
vibe dev on       # snapshot allowlisted sources, build in a pinned
                  # golang container, install as a dev artifact, pin
                  # THIS project to it
vibe dev status   # provenance: source, builder, and output digests
vibe dev sync     # rebuild after edits
vibe dev off      # back to the newest release artifact
```

Dev mode is per-project; release-mode projects never see dev artifacts.

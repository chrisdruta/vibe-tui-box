# Installation

The engine is one static binary per platform (`linux-amd64`,
`linux-arm64`, `darwin-arm64`) with the container payload embedded.
Host requirements: git, docker (a reachable daemon), and tmux for
`vibe tui`.

> **Pre-release note:** no release has been tagged yet
> ([ROADMAP](../ROADMAP.md)). Until then, install from source:
>
> ```sh
> go build -o bin/vibe ./cmd/vibe
> bin/vibe provision        # installs binary + payload under ~/.vibe
> ```
>
> and put `~/.vibe/bin/vibe` on your PATH. The release flow below is
> what ships at v1.0.

## From a release

Download the archive for your platform from the releases page, unpack,
and put `vibe` on your PATH. Then, from anywhere:

```sh
vibe provision
```

`provision` installs the running binary and its embedded payload as an
immutable, digest-addressed artifact under `~/.vibe`. Run inside a
registered project it also pins that project to the artifact. Everything
the engine writes lives under `~/.vibe`:

```text
~/.vibe/
├── bin/          # installed engine binaries; `vibe` symlink = current
├── artifacts/    # immutable release artifacts by digest (+ records)
└── state/        # registry, candidates, snapshots, broker, locks
```

## Updating

```sh
vibe update --version v2.1.0
```

`update` downloads the release archive, verifies it against the
release's `checksums.txt` while streaming, extracts only known entry
types, validates the embedded payload manifest file-by-file, publishes
the artifact by digest, pins the current project, and atomically
repoints `~/.vibe/bin/vibe` — the next invocation runs the new release.
Run `vibe rebuild` in a project to move its containers onto the new
payload. Other projects keep their pinned artifact until you update
them; artifacts are immutable, so versions coexist.

## First project

```sh
cd my-project
vibe init            # seeds .vibe/vibe.yaml (preset: minimal), registers,
                     # and pins the newest installed artifact
vibe up
vibe doctor          # verify the result
```

An existing `.vibe/vibe.yaml` (e.g. cloned with the repo) just needs
`vibe register`.

## Coming from v1

There is no migration. Remove the old `.vibe/harness` submodule and
v1 files (`compose.yaml`, `config.env`, hooks), delete the old
`~/.vibe` if you like, then `vibe init` and re-tell `vibe.yaml` what
the compose file used to say. Agent logins live in Docker volumes named
by project ID now, so agents log in fresh once.

# Extending the image (project layers)

The shared harness image carries agents and preset toolchains — the things
every project wants. Everything else (apt packages, Blender, browser
libraries, CUDA userlands, …) is a **project image extension**: an ordinary
Dockerfile the project owns, chained onto the shared image. The shared
Dockerfile never grows another flag for it.

```text
src/Dockerfile ──build──► <project>-base ──FROM──► .vibe/Dockerfile ──build──► <project>-dev
   (harness-owned,            (shared image,           (project-owned,            (what `dev` runs)
    INSTALL_* args)            always built)            optional)
```

`vibe` sequences the two builds (base first, then the extension when one is
declared), so the extension always chains onto the current base — after a
pin update that changes the harness Dockerfile, `vibe rebuild` rebuilds the
base and re-chains your extension automatically.

## Adding an extension

1. Create `.vibe/Dockerfile`:

   ```dockerfile
   ARG VIBE_BASE_IMAGE
   FROM ${VIBE_BASE_IMAGE}

   USER root          # root work happens HERE, at build time only
   RUN apt-get update \
       && apt-get install -y --no-install-recommends blender \
       && apt-get clean && rm -rf /var/lib/apt/lists/*
   USER vscode        # extension images must end non-root
   ```

2. Seed `.vibe/.dockerignore` from
   `src/templates/extensions/dockerignore` (keeps the harness submodule out
   of the build context and out of cache-key churn).

3. Declare the build in `.vibe/compose.yaml` — `image:` and `build:` go
   together (without `image:` the extension would overwrite the base tag):

   ```yaml
   services:
     dev:
       image: ${VIBE_PROJECT_NAME}-dev
       build:
         context: ./.vibe
         args:
           VIBE_BASE_IMAGE: ${VIBE_PROJECT_NAME}-base
   ```

4. `./vibe rebuild`.

Worked examples live in [`examples/extensions/`](../examples/extensions/)
(playwright, blender); `install.sh --extras playwright` performs these steps
for you at install time.

## The contract

- **Start** `ARG VIBE_BASE_IMAGE` / `FROM ${VIBE_BASE_IMAGE}` — the launcher
  passes the base tag in; never hardcode an image name.
- **Root only at build time, end `USER vscode`.** Belt and braces: the
  compose base also forces `user: vscode`, `cap_drop: [ALL]`, and
  `no-new-privileges` at runtime, so image content cannot weaken the
  running container — but a well-formed extension ends non-root anyway.
- **Multi-arch**: the image must build on amd64 and arm64 (Apple Silicon).
  Prefer distro packages (like the Blender example) or arch-aware
  installers; a bare x86_64 tarball breaks half the hosts.
- **State**: anything for an agent CLI must survive the `~/.agents` volume
  mount — binaries to `~/.local/bin`, never under `~/.agents`.
- The base image's `SHELL` (bash + pipefail), `ENV`, and `PATH` are
  inherited — `RUN curl | bash` pipelines fail loudly like they do in the
  harness Dockerfile.

## Image tags and rebuild semantics

Per project (name derived from the workspace folder, sanitized):

| Tag                       | Built from          | When                                  |
| ------------------------- | ------------------- | ------------------------------------- |
| `vibe-<name>-base`        | `src/Dockerfile`    | always (build-only `base` service)    |
| `vibe-<name>-dev`         | `.vibe/Dockerfile`  | only when the project declares it     |

- `vibe up` — builds only when an image is missing; otherwise fast no-op.
  Editing a Dockerfile does **not** rebuild on `up` (same as before).
- `vibe rebuild` — always rebuilds base then extension (cache-honoring:
  unchanged layers are instant), then recreates the container. This is the
  command after editing `.vibe/Dockerfile`, `.vibe/compose.yaml` build args,
  or moving the harness pin.
- `vibe build` — both builds, no container churn.
- Old image layers accumulate like any docker workflow; `docker image prune`
  reclaims them.

## What still belongs in the shared image

Agent CLIs (`INSTALL_CLAUDE_CODE`, `INSTALL_CODEX`, `INSTALL_GROK`) and
preset toolchains (`INSTALL_NODE`, `INSTALL_BUN`, `INSTALL_ROKIT`) stay
build args on the `base` service — they are the presets' identity and are
version-pinned centrally. If an extension turns out to be universal, it can
graduate into the shared Dockerfile; the default is that it doesn't.

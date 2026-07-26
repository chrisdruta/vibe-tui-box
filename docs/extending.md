# Extending the image

System-level additions the base image doesn't carry (apt packages,
Blender, browser libraries, …) go in a project-owned Dockerfile built on
top of the pinned base.

## Setup

1. In `.vibe/vibe.yaml`, set `image.extension: true`.
2. Create `.vibe/Dockerfile`:

```dockerfile
ARG VIBE_BASE_IMAGE
FROM ${VIBE_BASE_IMAGE}

USER root
RUN apt-get update && apt-get install -y --no-install-recommends \
      libgl1 blender \
    && rm -rf /var/lib/apt/lists/*
USER vscode
```

3. `vibe up`. The engine freezes the Dockerfile (plus `.vibe/build/` if
   present — that directory is the only extra COPY source), shows it to
   you with its content digest, and asks for approval. The build runs
   only after you approve; the resulting image is pinned by digest into
   the candidate.

Approval is **per changed digest**, not a standing trust in the file
path: edit the Dockerfile and the next `up` asks again. Unchanged
content never re-prompts.

## The contract

The intent: every byte in the image comes from the engine-pinned base
or the frozen build context — the Dockerfile cannot reach anywhere
else. The enforced rules (authoritative list: the doc comment on
`builder.ValidateDockerfile`): no custom `# syntax` frontends; exactly
one `FROM`, and it must be `${VIBE_BASE_IMAGE}` (declare
`ARG VIBE_BASE_IMAGE` first); no `ADD`, `ONBUILD`, `COPY --from`, or
extra build stages; any final `USER` must return to `vscode`. The
engine supplies `VIBE_BASE_IMAGE` digest-pinned — the Dockerfile cannot
choose its own base. When the manifest declares
`image.agents`/`image.toolchains`, that base is the engine-generated
install image, so the extension layers on top of the baked tools.

`RUN` lines execute in Docker's builder with network access once
approved — that is what the approval step is for; see
[security.md](security.md).

## Choosing base vs extension

Toolchains with schema toggles (`image.toolchains`) belong in
`vibe.yaml`. The extension is for everything else — one-off system
dependencies a single project needs. Keep it small: a big extension is
usually a sign the project wants a different `image.base`.

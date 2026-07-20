# syntax=docker/dockerfile:1
# Project image extension: headless Blender for render pipelines. Chained
# onto the shared harness image by .vibe/compose.yaml (build.context:
# ./.vibe). Contract: docs/extending.md.
#
# Debian's blender package builds for BOTH amd64 and arm64 — which is why
# it is used here instead of a blender.org tarball (x86_64-only); pin a
# specific upstream version only if your pipeline needs one, and handle
# aarch64 if you do.
ARG VIBE_BASE_IMAGE
FROM ${VIBE_BASE_IMAGE}

# apt needs root, and only HERE at build time: the running container has
# no sudo and drops all capabilities.
USER root
RUN apt-get update \
    && apt-get install -y --no-install-recommends blender \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

# Extension images must end non-root.
USER vscode

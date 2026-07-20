# syntax=docker/dockerfile:1
# Project image extension: system libraries for headless Chromium
# (@playwright/cli — see the harness docs/browser-automation.md for the
# full recipe). Chained onto the shared harness image by .vibe/compose.yaml
# (build.context: ./.vibe); inherits the base image's SHELL (bash +
# pipefail), ENV, and PATH. Contract: docs/extending.md.
ARG VIBE_BASE_IMAGE
FROM ${VIBE_BASE_IMAGE}

# apt needs root, and only HERE at build time: the running container has
# no sudo and drops all capabilities (runtime hardening lives in the
# harness compose base, untouched by anything this file does).
USER root

# Playwright resolves the correct apt package list for the distro, so we
# don't hand-maintain one. Requires Node in the base image
# (INSTALL_NODE/INSTALL_CODEX). Empty = latest (the deliberately mutable
# input); set X.Y.Z to freeze the resolved list.
ARG PLAYWRIGHT_VERSION=""
RUN DEBIAN_FRONTEND=noninteractive \
      npx -y "playwright@${PLAYWRIGHT_VERSION:-latest}" install-deps chromium \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/* /root/.npm

# Extension images must end non-root (the compose base also forces
# user: vscode at runtime, so a forgotten USER cannot weaken the container).
USER vscode

# syntax=docker/dockerfile:1

ARG UV_VERSION=0.11.28
ARG BASE_IMAGE=mcr.microsoft.com/devcontainers/base:debian

FROM ghcr.io/astral-sh/uv:${UV_VERSION} AS uv
FROM ${BASE_IMAGE}

# Installer pipelines (curl | bash) must fail the build, not silently no-op
# when the download fails — the default sh -c has no pipefail, and a poisoned
# layer then persists in cache with the tool missing.
SHELL ["/bin/bash", "-o", "pipefail", "-c"]

ARG INSTALL_CLAUDE_CODE=true
# "stable" is the one consciously mutable component; set a concrete version to freeze it.
ARG CLAUDE_CODE_VERSION=stable
ARG INSTALL_CODEX=false
ARG CODEX_VERSION=0.144.1
ARG INSTALL_GROK=false
# Empty means the installer's latest stable, mirroring Claude's "stable" policy;
# set X.Y.Z to freeze it.
ARG GROK_VERSION=""
ARG INSTALL_NODE=false
ARG NODE_MAJOR=22
ARG INSTALL_BUN=false
ARG BUN_VERSION=1.3.14
ARG INSTALL_ROKIT=false
ARG ROKIT_VERSION=1.2.0

ENV LANG=C.UTF-8 \
    PATH=/home/vscode/.local/bin:/home/vscode/.bun/bin:/home/vscode/.rokit/bin:${PATH}

USER root

RUN rm -f /etc/apt/sources.list.d/yarn.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
        build-essential \
        ca-certificates \
        curl \
        fd-find \
        gh \
        git \
        git-lfs \
        jq \
        less \
        procps \
        ripgrep \
        shellcheck \
        shfmt \
        tmux \
        unzip \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/* \
    && ln -sf /usr/bin/fdfind /usr/local/bin/fd \
    && rm -f /etc/sudoers.d/vscode

COPY --from=uv /uv /uvx /usr/local/bin/
COPY tmux.conf /etc/tmux.conf

# Node is required by the npm-distributed Codex CLI and available standalone.
RUN if [ "${INSTALL_NODE}" = "true" ] || [ "${INSTALL_CODEX}" = "true" ]; then \
        curl -fsSL "https://deb.nodesource.com/setup_${NODE_MAJOR}.x" | bash - \
        && apt-get install -y --no-install-recommends nodejs \
        && apt-get clean \
        && rm -rf /var/lib/apt/lists/*; \
    fi

USER vscode

# The shared agent-state named volume mounts here (subdir per agent CLI). The directory
# must already exist owned by vscode: with sudo removed and all capabilities dropped,
# nothing in the running container can fix a root-owned volume after the fact.
RUN mkdir -p /home/vscode/.agents

# Claude Code is the only agent installed by default. The other CLIs are opt-in.
RUN if [ "${INSTALL_CLAUDE_CODE}" = "true" ]; then \
        curl -fsSL https://claude.ai/install.sh | bash -s -- "${CLAUDE_CODE_VERSION}" \
        && test -x /home/vscode/.local/bin/claude; \
    fi

RUN if [ "${INSTALL_CODEX}" = "true" ]; then \
        npm install -g --prefix /home/vscode/.local "@openai/codex@${CODEX_VERSION}"; \
    fi

# Grok Build (xAI official). Its state (auth.json, config.toml) lives in ~/.grok with no
# env override, so ~/.grok is symlinked into the state volume BEFORE install. The installer
# symlinks grok/agent into GROK_BIN_DIR pointing at ~/.grok/downloads/, which the runtime
# volume mount would shadow — so the real binary is materialized into ~/.local/bin instead.
RUN if [ "${INSTALL_GROK}" = "true" ]; then \
        mkdir -p /home/vscode/.agents/grok \
        && ln -s /home/vscode/.agents/grok /home/vscode/.grok \
        && curl -fsSL https://x.ai/cli/install.sh \
            | GROK_BIN_DIR=/home/vscode/.local/bin bash -s -- ${GROK_VERSION} \
        && bin="$(readlink -f /home/vscode/.local/bin/grok)" \
        && rm -f /home/vscode/.local/bin/grok /home/vscode/.local/bin/agent \
        && cp "$bin" /home/vscode/.local/bin/grok \
        && ln -s grok /home/vscode/.local/bin/agent \
        && rm -rf /home/vscode/.agents/grok/downloads; \
    fi

# Optional ecosystem bootstraps. Project-specific tool versions remain declared by the project.
RUN if [ "${INSTALL_BUN}" = "true" ]; then \
        curl -fsSL https://bun.sh/install | bash -s -- "bun-v${BUN_VERSION}" \
        && test -x /home/vscode/.bun/bin/bun; \
    fi

RUN if [ "${INSTALL_ROKIT}" = "true" ]; then \
        tmp="$(mktemp -d)" && cd "$tmp" \
        && curl -fsSL -o rokit.zip \
            "https://github.com/rojo-rbx/rokit/releases/download/v${ROKIT_VERSION}/rokit-${ROKIT_VERSION}-linux-$(uname -m).zip" \
        && unzip -q rokit.zip \
        && ./rokit self-install \
        && rm -rf "$tmp"; \
    fi

# Keep the ambient container user non-root. Explicit host maintenance can still use:
# docker exec -u root -it <container> bash
USER vscode

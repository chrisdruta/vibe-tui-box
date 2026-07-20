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
# Match the host user's UID so bind-mount writes stay owned by the host
# user (the devcontainer CLI used to sync this silently). WSL's default is
# 1000 — a no-op; macOS file sharing does not need it.
ARG USER_UID=1000
# yazi: the image-review file manager (vibe review / the preview window).
# Release binary pinned by version + checksum per arch, like rokit/uv.
ARG YAZI_VERSION=26.5.6
ARG YAZI_SHA256_AMD64=1c9096f0a83b8102c194385f644cdeff93cc8269426163c9d033041ebd537bd2
ARG YAZI_SHA256_ARM64=c38b07961e7fc4c76503fd0f4a1b4bd0b379a99835b818cd899b0315c728e1e1

ENV LANG=C.UTF-8 \
    PATH=/home/vscode/.local/bin:/home/vscode/.bun/bin:/home/vscode/.rokit/bin:${PATH}

USER root

RUN rm -f /etc/apt/sources.list.d/yarn.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
        build-essential \
        ca-certificates \
        chafa \
        curl \
        fd-find \
        file \
        gh \
        git \
        git-lfs \
        jq \
        less \
        libsixel-bin \
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
COPY config/tmux.conf /etc/tmux.conf
# Baked under fixed names so tmux.conf bindings and yazi keymaps can call
# them — the per-project harness path isn't known at image build time (and
# yazi shell commands run from whatever directory is being browsed).
COPY --chmod=0755 scripts/show-image.sh /usr/local/bin/vibe-show-image
COPY --chmod=0755 scripts/review.sh /usr/local/bin/vibe-preview
COPY --chmod=0755 scripts/review-verdict.sh /usr/local/bin/vibe-verdict
COPY --chmod=0644 scripts/preview-lib.sh /usr/local/lib/vibe/preview-lib.sh
COPY config/yazi /usr/local/lib/vibe/yazi
# COPY --chmod also applies to the parent directories it implicitly creates —
# the 0644 above left /usr/local/lib/vibe untraversable for non-root, hiding
# the baked config from the vscode user. Normalize: dirs a+rx, files a+r.
RUN chmod -R a+rX /usr/local/lib/vibe

# yazi renders the images itself (sixel/kitty/iTerm2 detection built in);
# file(1) is its mime-detection dependency. `ya` is its remote control — the
# Claude Code hook uses it to reveal images in the running review instance.
RUN tmp="$(mktemp -d)" \
    && case "$(uname -m)" in \
         x86_64)  arch=x86_64;  sha="${YAZI_SHA256_AMD64}" ;; \
         aarch64) arch=aarch64; sha="${YAZI_SHA256_ARM64}" ;; \
         *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;; \
       esac \
    && curl -fsSL -o "$tmp/yazi.zip" \
        "https://github.com/sxyazi/yazi/releases/download/v${YAZI_VERSION}/yazi-${arch}-unknown-linux-gnu.zip" \
    && echo "${sha}  $tmp/yazi.zip" | sha256sum -c - \
    && unzip -q "$tmp/yazi.zip" -d "$tmp" \
    && install -m 0755 "$tmp/yazi-${arch}-unknown-linux-gnu/yazi" \
        "$tmp/yazi-${arch}-unknown-linux-gnu/ya" /usr/local/bin/ \
    && rm -rf "$tmp"

# Node is required by the npm-distributed Codex CLI and available standalone.
RUN if [ "${INSTALL_NODE}" = "true" ] || [ "${INSTALL_CODEX}" = "true" ]; then \
        curl -fsSL "https://deb.nodesource.com/setup_${NODE_MAJOR}.x" | bash - \
        && apt-get install -y --no-install-recommends nodejs \
        && apt-get clean \
        && rm -rf /var/lib/apt/lists/*; \
    fi

# Keep bind-mount file ownership aligned with the host user when it isn't
# uid 1000 (Linux hosts; see the USER_UID arg above).
RUN if [ "${USER_UID}" != "1000" ]; then \
        usermod -u "${USER_UID}" vscode \
        && chown -R "${USER_UID}" /home/vscode; \
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

# syntax=docker/dockerfile:1

ARG UV_VERSION=0.11.28
ARG BASE_IMAGE=mcr.microsoft.com/devcontainers/base:debian

FROM ghcr.io/astral-sh/uv:${UV_VERSION} AS uv

# tmux + chafa are built from source, not apt: Debian stable ships tmux 3.5a,
# which drops sixel images whenever a pane redraws (upstream #4499/#4639) —
# 3.7b retains them across adjacent-pane TUI redraws (verified by dogfood
# spike 2026-07-21; a resize still clears images, that reflow behavior is
# upstream). Debian's chafa 1.14.5 predates --probe (yazi's cell-art
# fallback) and the newer kitty/passthrough work; building from source also
# sidesteps the missing upstream arm64 static binaries. Staged into
# /opt/stage and copied into the final image; runtime libs are apt-installed
# there explicitly.
FROM ${BASE_IMAGE} AS term-build
ARG TMUX_VERSION=3.7b
ARG TMUX_SHA256=87f2e99e3b685973f2ca002ffd6ed7e51a5744f7009daae5a15670b6d532db96
ARG CHAFA_VERSION=1.18.2
ARG CHAFA_SHA256=0b8d9ba9f347e8b6c0c71878217c9b0e478b4a42aa4babea0bf20840567239c2
SHELL ["/bin/bash", "-o", "pipefail", "-c"]
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        build-essential \
        byacc \
        ca-certificates \
        curl \
        libevent-dev \
        libfreetype6-dev \
        libglib2.0-dev \
        libjpeg62-turbo-dev \
        libncurses-dev \
        libpng-dev \
        libwebp-dev \
        pkg-config \
        xz-utils
RUN tmp="$(mktemp -d)" && cd "$tmp" \
    && curl -fsSL -o tmux.tar.gz \
        "https://github.com/tmux/tmux/releases/download/${TMUX_VERSION}/tmux-${TMUX_VERSION}.tar.gz" \
    && echo "${TMUX_SHA256}  tmux.tar.gz" | sha256sum -c - \
    && tar xzf tmux.tar.gz && cd "tmux-${TMUX_VERSION}" \
    && ./configure --prefix=/usr/local --enable-sixel \
    && make -j"$(nproc)" \
    && make DESTDIR=/opt/stage install \
    && rm -rf "$tmp"
RUN tmp="$(mktemp -d)" && cd "$tmp" \
    && curl -fsSL -o chafa.tar.xz \
        "https://hpjansson.org/chafa/releases/chafa-${CHAFA_VERSION}.tar.xz" \
    && echo "${CHAFA_SHA256}  chafa.tar.xz" | sha256sum -c - \
    && tar xJf chafa.tar.xz && cd "chafa-${CHAFA_VERSION}" \
    && ./configure --prefix=/usr/local \
    && make -j"$(nproc)" \
    && make DESTDIR=/opt/stage install \
    && rm -rf "$tmp"

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
ARG INSTALL_GO=false
ARG GO_VERSION=1.26.5
ARG GO_SHA256_AMD64=5c2c3b16caefa1d968a94c1daca04a7ca301a496d9b086e17ad77bb81393f053
ARG GO_SHA256_ARM64=fe4789e92b1f33358680864bbe8704289e7bb5fc207d80623c308935bd696d49
# Match the host user's UID so bind-mount writes stay owned by the host
# user (the devcontainer CLI used to sync this silently). WSL's default is
# 1000 — a no-op; macOS file sharing does not need it.
ARG USER_UID=1000
# yazi: the image-review file manager (vibe review / the preview window).
# Release binary pinned by version + checksum per arch, like rokit/uv.
ARG YAZI_VERSION=26.5.6
ARG YAZI_SHA256_AMD64=1c9096f0a83b8102c194385f644cdeff93cc8269426163c9d033041ebd537bd2
ARG YAZI_SHA256_ARM64=c38b07961e7fc4c76503fd0f4a1b4bd0b379a99835b818cd899b0315c728e1e1
# revdiff: the read-only diff-review TUI (`vibe exec revdiff`; tmux palette
# "review diff" — no top-level verb: trial tools earn one only with harness
# logic). Reviews what an agent wrote without being able to touch it;
# annotations print to stdout on quit.
ARG REVDIFF_VERSION=1.11.1
ARG REVDIFF_SHA256_AMD64=7958a6bef723267fed18b0be96476bb767636afe966338ed7e370c0a506fd54c
ARG REVDIFF_SHA256_ARM64=87c522516e2d0ef113b74ff729a1e9072aad9958f1aeb6836b90d2b8fa2e0762

ENV LANG=C.UTF-8 \
    PATH=/home/vscode/.local/bin:/home/vscode/.bun/bin:/home/vscode/.rokit/bin:/usr/local/go/bin:/home/vscode/go/bin:${PATH}

USER root

RUN rm -f /etc/apt/sources.list.d/yarn.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
        build-essential \
        ca-certificates \
        curl \
        fd-find \
        file \
        gh \
        git \
        git-lfs \
        jq \
        less \
        libevent-core-2.1-7t64 \
        libfreetype6 \
        libglib2.0-0t64 \
        libjpeg62-turbo \
        libncursesw6 \
        libpng16-16t64 \
        libsixel-bin \
        libwebp7 \
        libwebpdemux2 \
        procps \
        ripgrep \
        shellcheck \
        shfmt \
        unzip \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/* \
    && ln -sf /usr/bin/fdfind /usr/local/bin/fd \
    && rm -f /etc/sudoers.d/vscode

# Source-built tmux (sixel-enabled) + chafa from the term-build stage; the
# libevent/freetype/glib/jpeg/png/webp(+demux)/ncursesw runtime packages
# above are their dynamic dependencies — keep them in lockstep with what the
# term-build stage compiles against (`ldd /usr/local/bin/chafa` after a
# rebuild must show no "not found"). ldconfig registers libchafa in
# /usr/local/lib.
COPY --from=term-build /opt/stage/usr/local/ /usr/local/
RUN ldconfig

COPY --from=uv /uv /uvx /usr/local/bin/
COPY config/tmux.conf /etc/tmux.conf
# Baked under fixed names so tmux.conf bindings and yazi keymaps can call
# them — the per-project harness path isn't known at image build time (and
# yazi shell commands run from whatever directory is being browsed).
COPY --chmod=0755 scripts/show-image.sh /usr/local/bin/vibe-show-image
COPY --chmod=0755 scripts/review.sh /usr/local/bin/vibe-preview
COPY --chmod=0755 scripts/review-verdict.sh /usr/local/bin/vibe-verdict
COPY --chmod=0755 scripts/svc.sh /usr/local/bin/vibe-svc
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

# revdiff (vibe exec revdiff): goreleaser tarball, binary at the archive root.
RUN tmp="$(mktemp -d)" \
    && case "$(uname -m)" in \
         x86_64)  arch=amd64; sha="${REVDIFF_SHA256_AMD64}" ;; \
         aarch64) arch=arm64; sha="${REVDIFF_SHA256_ARM64}" ;; \
         *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;; \
       esac \
    && curl -fsSL -o "$tmp/revdiff.tar.gz" \
        "https://github.com/umputun/revdiff/releases/download/v${REVDIFF_VERSION}/revdiff_${REVDIFF_VERSION}_linux_${arch}.tar.gz" \
    && echo "${sha}  $tmp/revdiff.tar.gz" | sha256sum -c - \
    && tar -xzf "$tmp/revdiff.tar.gz" -C "$tmp" \
    && install -m 0755 "$tmp/revdiff" /usr/local/bin/ \
    && rm -rf "$tmp"

# Go toolchain (official tarball, pinned by version + per-arch checksum,
# upstream layout under /usr/local/go — PATH above already carries
# /usr/local/go/bin and ~/go/bin). Powers harness development (the host
# engine port, docs/go-port-plan.md) and Go projects alike.
RUN if [ "${INSTALL_GO}" = "true" ]; then \
        tmp="$(mktemp -d)" \
        && case "$(uname -m)" in \
             x86_64)  arch=amd64; sha="${GO_SHA256_AMD64}" ;; \
             aarch64) arch=arm64; sha="${GO_SHA256_ARM64}" ;; \
             *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;; \
           esac \
        && curl -fsSL -o "$tmp/go.tar.gz" \
            "https://go.dev/dl/go${GO_VERSION}.linux-${arch}.tar.gz" \
        && echo "${sha}  $tmp/go.tar.gz" | sha256sum -c - \
        && tar -C /usr/local -xzf "$tmp/go.tar.gz" \
        && rm -rf "$tmp"; \
    fi

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

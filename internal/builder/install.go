package builder

import (
	"strconv"
	"strings"

	"github.com/chrisdruta/vibe-tui-box/internal/schema"
)

// install.go generates the engine-authored Dockerfile that bakes
// image.agents and image.toolchains into a project's tools image. The
// recipes are the v2 continuation of the v1 image's install ARGs; they
// are engine code, never project input, so tools builds need no
// per-digest operator approval — the manifest only selects from this
// closed set.

// Engine-owned install pins. Agent CLIs follow the manifest: an
// unversioned image.agents entry tracks its installer's LATEST channel
// below and re-pulls on every rebuild (the refresh path — "no version
// given" means "keep me current", and claude's stable channel lags
// latest by design, which is exactly the staleness the refresh exists
// to kill); an exact pin ("claude@2.1.220") installs that version in a
// plain cached layer and never refreshes; a named channel
// ("claude@stable") installs that channel and refreshes like
// unversioned. The system toolchains move only with engine releases.
const (
	claudeChannel = "latest"
	codexChannel  = "latest" // the npm dist-tag unversioned codex tracks
	nodeMajor     = "22"
	bunVersion    = "1.3.14"
	rokitVersion  = "1.2.0"
	goVersion     = "1.26.5"
	goSHA256AMD64 = "5c2c3b16caefa1d968a94c1daca04a7ca301a496d9b086e17ad77bb81393f053"
	goSHA256ARM64 = "fe4789e92b1f33358680864bbe8704289e7bb5fc207d80623c308935bd696d49"
	// tmux is pinned and built from source — the lesson the v1 line
	// already recorded (its install-tmux.sh/Dockerfile carried the SAME
	// version + checksum): distro tmux drops sixel images on pane redraw
	// (bookworm ships 3.3a; upstream #4499/#4639 fixed by 3.7) and skews
	// per base image, while host conf and container scripts are written
	// against one version's semantics. The v2 cutover regressed this to
	// apt tmux; re-pinned 2026-07-25.
	tmuxVersion = "3.7b"
	tmuxSHA256  = "87f2e99e3b685973f2ca002ffd6ed7e51a5744f7009daae5a15670b6d532db96"
	// The bundled review stack (docs/tui-layout.md "Editor surfaces",
	// second call): nvim + lazygit ride every agent image like tmux —
	// the container carries the review surface because a cold host is
	// the norm (stock WSL ships neither). Pinned release artifacts,
	// per-arch checksums; the nvim asset arch names differ from Go's
	// (x86_64/arm64), the constants keep the repo's AMD64/ARM64 names.
	nvimVersion        = "0.12.4"
	nvimSHA256AMD64    = "012bf3fcac5ade43914df3f174668bf64d05e049a4f032a388c027b1ebd78628"
	nvimSHA256ARM64    = "ceb7e88c6b681f0515d135dcdfad54f5eb4373b25ce6172197cd9a69c758063f"
	lazygitVersion     = "0.63.1"
	lazygitSHA256AMD64 = "8e033bc78c8e192dee9510e951f6c9e154289b7198d22c924ed1d0a951b0dac1"
	lazygitSHA256ARM64 = "555dbc9a8efcf2e33bc24e7fbd9463e9fa375e3c5e23cc270763733c38eeae36"
	// nvim-treesitter's main branch requires the tree-sitter CLI
	// (>= 0.26.1) for the build-time parser compile. Built from the
	// crates.io source with a PINNED Rust toolchain, not the release
	// binaries: every 0.26.x prebuilt links glibc 2.39 (Ubuntu 24.04
	// runners) and the bookworm-era bases carry 2.36 — the first
	// rebuild proved it (2026-07-26). The toolchain lives in the RUN's
	// scratch dir and is deleted in the same layer; only the CLI
	// binary lands in the image.
	treesitterCLIVersion = "0.26.11"
	rustVersion          = "1.97.1"
)

// reviewPlugins are cloned into the root-owned native packpath
// (/opt/vibe/nvim/pack/vibe/start) at exactly these commits — no
// plugin manager, no runtime network, no plugin bytes on volumes (the
// marketplace-install rejection record). These SHAs are executable
// Lua: bump them deliberately, with a look at the upstream diff, never
// by reflex. diffview pins the maintained fork — upstream sindrets is
// dormant since mid-2024.
var reviewPlugins = []struct{ Repo, SHA string }{
	{"echasnovski/mini.nvim", "946ae64e0ee807ae3c41f382f0114b4ed4915b2c"},
	{"dlyongemallo/diffview.nvim", "62dc5adf4e77489a2a6d3bf36ef6e4ac5738b634"},
	{"lewis6991/gitsigns.nvim", "31d6fb2d618bca1482b9f274751ead5f03461408"},
	{"folke/tokyonight.nvim", "cdc07ac78467a233fd62c493de29a17e0cf2b2b6"},
	{"nvim-treesitter/nvim-treesitter", "8b3a191c015dd66a92d51a112ed96af0aac13b63"},
}

// reviewParsers is the engine-owned treesitter language list, compiled
// once at image build (nvim-treesitter main branch, headless install)
// into a root-owned site dir — no runtime compiles. Engine-owned on
// purpose: a manifest knob here would grow without bound.
var reviewParsers = []string{
	"bash", "c", "css", "diff", "go", "gomod", "gosum", "html",
	"javascript", "json", "lua", "luau", "markdown", "markdown_inline",
	"python", "regex", "rust", "toml", "tsx", "typescript", "vim",
	"vimdoc", "yaml",
}

// AgentRefreshArg is the build arg that busts the Docker layer cache
// for the channel-tracking (unversioned) agent installers. Every `vibe
// rebuild` stamps it with a fresh value so those layers — and only
// those — re-run and re-pull the latest agent builds; pinned agents and
// the system toolchains sit in plain layers and stay cached. When a
// project has never rebuilt the arg is absent.
const AgentRefreshArg = "VIBE_AGENT_REFRESH"

// GenerateInstall renders the install Dockerfile for the given
// selection. The output is a pure function of the selection (and the
// refresh flag) — canonical layer order, engine-pinned toolchains,
// manifest-pinned or channel-tracking agents — so identical inputs
// yield identical bytes and a warm Docker layer cache. The result
// always satisfies ValidateDockerfile. Nil means nothing to install.
//
// refresh weaves the AgentRefreshArg cache-buster into the UNVERSIONED
// agent layers only; a manifest-pinned agent installs its exact version
// in a plain layer no refresh can move, and the system toolchains are
// never touched. A selection with only pinned agents ignores refresh
// entirely.
func GenerateInstall(agents []schema.AgentSpec, toolchains []schema.Toolchain, refresh bool) []byte {
	want := map[string]bool{}
	pin := map[schema.AgentKind]string{}
	for _, a := range agents {
		want[string(a.Kind)] = true
		pin[a.Kind] = a.Version
	}
	for _, t := range toolchains {
		want[string(t)] = true
	}
	if len(want) == 0 {
		return nil
	}
	// codex is npm-distributed: it drags the node toolchain in.
	if want[string(schema.AgentCodex)] {
		want[string(schema.ToolchainNode)] = true
	}

	var b strings.Builder
	b.WriteString("# Generated by the vibe engine from image.agents / image.toolchains.\n")
	b.WriteString("# Not project input: regenerated every build, pins move with engine releases.\n")
	b.WriteString("ARG " + BaseImageArg + "\n")
	b.WriteString("FROM ${" + BaseImageArg + "}\n")

	if entries := pathEntries(want); len(entries) > 0 {
		b.WriteString("\nENV PATH=" + strings.Join(entries, ":") + ":${PATH}\n")
	}
	if layers := rootLayers(want); len(layers) > 0 {
		b.WriteString("\nUSER root\n")
		for _, l := range layers {
			b.WriteString("\n" + l)
		}
	}
	b.WriteString("\nUSER vscode\n")
	for _, l := range userLayers(want, pin, refresh) {
		b.WriteString("\n" + l)
	}
	return []byte(b.String())
}

// wantsAgent reports whether the selection includes any agent CLI.
func wantsAgent(want map[string]bool) bool {
	return want[string(schema.AgentClaude)] || want[string(schema.AgentCodex)] || want[string(schema.AgentGrok)]
}

// pathEntries lists the bin directories the selection installs into,
// in fixed v1 PATH order. Node needs none: nodesource installs to
// /usr/bin.
func pathEntries(want map[string]bool) []string {
	var out []string
	if wantsAgent(want) {
		out = append(out, "/home/vscode/.local/bin")
	}
	if want[string(schema.ToolchainBun)] {
		out = append(out, "/home/vscode/.bun/bin")
	}
	if want[string(schema.ToolchainRokit)] {
		out = append(out, "/home/vscode/.rokit/bin")
	}
	if want[string(schema.ToolchainGo)] {
		out = append(out, "/usr/local/go/bin", "/home/vscode/go/bin")
	}
	return out
}

// rootLayers renders the layers that need root: the agent-state mount
// point, system toolchains, and apt dependencies. Fixed order:
// agent-state, tmux, go, node, rokit's unzip guard.
func rootLayers(want map[string]bool) []string {
	var out []string
	// The agent-state named volume mounts here; Docker initializes a
	// fresh volume from the image path, so baking the directory
	// vscode-owned is what makes the volume writable by the agent.
	out = append(out, `# Agent-state mount point: fresh named volumes inherit this ownership.
RUN mkdir -p /vibe/agent-state && chown vscode:vscode /vibe/agent-state
`)
	if wantsAgent(want) {
		// Pinned source build (see the tmuxVersion decision comment) at
		// /usr/local/bin/tmux — the path App.Agent probes first. Single
		// stage on purpose: ValidateDockerfile forbids extra build
		// stages, so the build deps stay in the image (a dev container
		// carries compilers anyway). jq feeds the statusline glue
		// (never the hot-path state hook).
		out = append(out, `# tmux `+tmuxVersion+` carries the persistent agent session (docs/architecture.md
# (agent sessions)): pinned source build with --enable-sixel — distro tmux
# drops sixel images on pane redraw and varies per base image. jq
# parses the Claude statusline JSON.
RUN apt-get update \
    && apt-get install -y --no-install-recommends jq build-essential byacc pkg-config libevent-dev libncurses-dev ca-certificates curl \
    && tmp="$(mktemp -d)" \
    && curl -fsSL -o "$tmp/tmux.tar.gz" "https://github.com/tmux/tmux/releases/download/`+tmuxVersion+`/tmux-`+tmuxVersion+`.tar.gz" \
    && echo "`+tmuxSHA256+`  $tmp/tmux.tar.gz" | sha256sum -c - \
    && tar -C "$tmp" -xzf "$tmp/tmux.tar.gz" \
    && cd "$tmp/tmux-`+tmuxVersion+`" \
    && ./configure --prefix=/usr/local --enable-sixel \
    && make -j"$(nproc)" \
    && make install \
    && cd / \
    && rm -rf "$tmp" \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*
`)
		out = append(out, reviewLayers()...)
	}
	if want[string(schema.ToolchainGo)] {
		out = append(out, `# Go toolchain: official tarball pinned by version + per-arch checksum,
# upstream layout under /usr/local/go (PATH above carries its bin dirs).
RUN tmp="$(mktemp -d)" \
    && case "$(uname -m)" in \
         x86_64)  arch=amd64; sha="`+goSHA256AMD64+`" ;; \
         aarch64) arch=arm64; sha="`+goSHA256ARM64+`" ;; \
         *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;; \
       esac \
    && curl -fsSL -o "$tmp/go.tar.gz" "https://go.dev/dl/go`+goVersion+`.linux-${arch}.tar.gz" \
    && echo "${sha}  $tmp/go.tar.gz" | sha256sum -c - \
    && rm -rf /usr/local/go \
    && tar -C /usr/local -xzf "$tmp/go.tar.gz" \
    && rm -rf "$tmp"
`)
	}
	if want[string(schema.ToolchainNode)] {
		out = append(out, `# Node: required by npm-distributed agent CLIs and available standalone.
RUN curl -fsSL "https://deb.nodesource.com/setup_`+nodeMajor+`.x" | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*
`)
	}
	if want[string(schema.ToolchainRokit)] {
		out = append(out, `# rokit ships as a zip; make sure unzip exists on lean bases.
RUN command -v unzip >/dev/null 2>&1 \
    || { apt-get update \
         && apt-get install -y --no-install-recommends unzip \
         && apt-get clean \
         && rm -rf /var/lib/apt/lists/*; }
`)
	}
	return out
}

// reviewLayers renders the bundled review stack (rides the wantsAgent
// gate exactly like tmux — core product UX, not a manifest choice):
// the nvim + lazygit binaries, the SHA-pinned plugin packpath, and the
// build-time parser compile. Three separate layers on purpose — the
// parser layer is the speculative one (nvim-treesitter main-branch
// headless install) and stays independently droppable/cacheable. The
// runtime config that loads all of this lives in the payload
// (payload/container/nvim), so keymap iteration never rebuilds these
// layers.
func reviewLayers() []string {
	var out []string
	out = append(out, `# The bundled review stack (docs/tui-layout.md "Editor surfaces"):
# nvim + lazygit as pinned release artifacts — the container carries
# the review surface because a cold host is the norm.
RUN tmp="$(mktemp -d)" \
    && case "$(uname -m)" in \
         x86_64)  arch=x86_64; nvim_sha="`+nvimSHA256AMD64+`"; lazygit_sha="`+lazygitSHA256AMD64+`" ;; \
         aarch64) arch=arm64;  nvim_sha="`+nvimSHA256ARM64+`"; lazygit_sha="`+lazygitSHA256ARM64+`" ;; \
         *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;; \
       esac \
    && curl -fsSL -o "$tmp/nvim.tar.gz" "https://github.com/neovim/neovim/releases/download/v`+nvimVersion+`/nvim-linux-${arch}.tar.gz" \
    && echo "${nvim_sha}  $tmp/nvim.tar.gz" | sha256sum -c - \
    && tar -C /opt -xzf "$tmp/nvim.tar.gz" \
    && ln -sfn "/opt/nvim-linux-${arch}" /opt/nvim \
    && ln -sf /opt/nvim/bin/nvim /usr/local/bin/nvim \
    && curl -fsSL -o "$tmp/lazygit.tar.gz" "https://github.com/jesseduffield/lazygit/releases/download/v`+lazygitVersion+`/lazygit_`+lazygitVersion+`_linux_${arch}.tar.gz" \
    && echo "${lazygit_sha}  $tmp/lazygit.tar.gz" | sha256sum -c - \
    && tar -C "$tmp" -xzf "$tmp/lazygit.tar.gz" lazygit \
    && install -m 0755 "$tmp/lazygit" /usr/local/bin/lazygit \
    && rm -rf "$tmp"
`)
	var plugins strings.Builder
	plugins.WriteString(`# Review-stack plugins at pinned SHAs into a root-owned native
# packpath — no plugin manager, no runtime network; .git dirs stripped
# so the bytes are inert. diffview is the maintained fork (upstream
# dormant since 2024).
RUN mkdir -p /opt/vibe/nvim/pack/vibe/start`)
	for _, p := range reviewPlugins {
		name := p.Repo[strings.IndexByte(p.Repo, '/')+1:]
		dst := "/opt/vibe/nvim/pack/vibe/start/" + name
		plugins.WriteString(" \\\n    && git clone --no-checkout \"https://github.com/" + p.Repo + "\" \"" + dst + "\"")
		plugins.WriteString(" \\\n    && git -C \"" + dst + "\" checkout --detach " + p.SHA)
	}
	plugins.WriteString(" \\\n    && rm -rf /opt/vibe/nvim/pack/vibe/start/*/.git\n")
	out = append(out, plugins.String())
	out = append(out, `# Treesitter parsers for the engine-owned language list, compiled at
# image build (the tmux layer's build-essential compiles, the pinned
# tree-sitter CLI drives — a main-branch nvim-treesitter requirement)
# into a root-owned site dir the payload nvim config puts on the
# runtime path — no runtime compiles, no writable plugin state. The
# CLI is cargo-built with a pinned toolchain (release prebuilts need
# glibc 2.39 — newer than bookworm-era bases); the toolchain lives in
# scratch and dies inside this layer, only the CLI binary remains.
RUN tmp="$(mktemp -d)" \
    && export RUSTUP_HOME="$tmp/rustup" CARGO_HOME="$tmp/cargo" \
    && curl -fsSL https://sh.rustup.rs | sh -s -- -y --profile minimal --default-toolchain `+rustVersion+` --no-modify-path \
    && "$tmp/cargo/bin/cargo" install tree-sitter-cli --version `+treesitterCLIVersion+` --locked --root /usr/local \
    && rm -rf "$tmp" /usr/local/.crates.toml /usr/local/.crates2.json \
    && mkdir -p /opt/vibe/nvim-data \
    && HOME=/tmp XDG_DATA_HOME=/opt/vibe/nvim-data nvim --headless \
       --cmd "set packpath^=/opt/vibe/nvim" \
       -c "lua require('nvim-treesitter').install({'`+strings.Join(reviewParsers, `','`)+`'}):wait(900000)" \
       -c "quitall!" \
    && test "$(ls /opt/vibe/nvim-data/nvim/site/parser/*.so | wc -l)" -eq `+strconv.Itoa(len(reviewParsers))+`
`)
	return out
}

// refreshBust returns the shell prefix that ties a channel-tracking
// agent layer's cache key to the refresh token: after variable
// expansion the RUN text embeds the token value, so a new token misses
// the cache and re-runs the installer. Empty when not busting, which
// keeps that layer plainly cacheable.
func refreshBust(bust bool) string {
	if !bust {
		return ""
	}
	return `: "agents-refresh=${` + AgentRefreshArg + `}" \
    && `
}

// userLayers renders the layers that install as vscode into the home
// directory. Fixed order: agents (claude, codex, grok), then bun,
// rokit. Unversioned agents track their channel and, when refresh is
// set, gain the AgentRefreshArg cache-buster (declared once, up front);
// manifest-pinned agents install their exact version in a plain layer
// the buster never touches. bun and rokit stay engine-pinned but,
// sitting after the agents, re-run too when a refresh busts the chain
// (cheap — the expensive system toolchains live in rootLayers, before
// USER vscode).
func userLayers(want map[string]bool, pin map[schema.AgentKind]string, refresh bool) []string {
	// A version starting with a digit is an exact pin (frozen layer);
	// anything else ("stable", "latest", an npm dist-tag) names a
	// CHANNEL — a moving target that refreshes exactly like an
	// unversioned entry, because freezing a channel in a cached layer
	// is the staleness bug all over again.
	isChannel := func(v string) bool {
		return v != "" && (v[0] < '0' || v[0] > '9')
	}
	bustFor := func(kind schema.AgentKind) bool {
		return refresh && (pin[kind] == "" || isChannel(pin[kind]))
	}
	anyBust := false
	for _, kind := range []schema.AgentKind{schema.AgentClaude, schema.AgentCodex, schema.AgentGrok} {
		if want[string(kind)] && bustFor(kind) {
			anyBust = true
		}
	}
	var out []string
	if anyBust {
		out = append(out, `# Agent refresh: this build arg's value (a fresh token per rebuild)
# is woven into the channel-tracking installers below so their Docker
# layer cache misses and re-pulls the latest builds. Pinned agents sit
# in plain layers it never reaches.
ARG `+AgentRefreshArg+`
`)
	}
	if want[string(schema.AgentClaude)] {
		target := claudeChannel + " channel"
		spec := claudeChannel
		if v := pin[schema.AgentClaude]; v != "" {
			spec = v
			if isChannel(v) {
				target = "manifest-selected " + v + " channel"
			} else {
				target = "manifest-pinned " + v
			}
		}
		out = append(out, `# Claude Code: the installer's `+target+`.
RUN `+refreshBust(bustFor(schema.AgentClaude))+`curl -fsSL https://claude.ai/install.sh | bash -s -- `+spec+` \
    && test -x /home/vscode/.local/bin/claude
`)
	}
	if want[string(schema.AgentCodex)] {
		// Unversioned tracks the npm dist-tag; a manifest pin is exact.
		codexSpec := codexChannel
		if v := pin[schema.AgentCodex]; v != "" {
			codexSpec = v
		}
		out = append(out, `RUN `+refreshBust(bustFor(schema.AgentCodex))+`npm install -g --prefix /home/vscode/.local "@openai/codex@`+codexSpec+`"
`)
	}
	if want[string(schema.AgentGrok)] {
		out = append(out, `# Grok (xAI official). Its state (auth.json, config.toml) lives in
# ~/.grok with no env override, so ~/.grok is symlinked onto the
# agent-state volume path BEFORE install (fresh volumes inherit the
# dir from the image; lifecycle.sh materializes it on older volumes),
# making logins survive rebuilds like claude/codex relocation does.
# The installer symlinks grok/agent into GROK_BIN_DIR pointing at
# ~/.grok/downloads/, which the runtime volume mount would shadow — so
# the real binary is materialized instead.
RUN `+refreshBust(bustFor(schema.AgentGrok))+`mkdir -p /vibe/agent-state/grok \
    && ln -s /vibe/agent-state/grok /home/vscode/.grok \
    && curl -fsSL https://x.ai/cli/install.sh | GROK_BIN_DIR=/home/vscode/.local/bin bash -s -- \
    && bin="$(readlink -f /home/vscode/.local/bin/grok)" \
    && rm -f /home/vscode/.local/bin/grok /home/vscode/.local/bin/agent \
    && cp "$bin" /home/vscode/.local/bin/grok \
    && ln -s grok /home/vscode/.local/bin/agent \
    && rm -rf /vibe/agent-state/grok/downloads
`)
	}
	if want[string(schema.ToolchainBun)] {
		out = append(out, `RUN curl -fsSL https://bun.sh/install | bash -s -- "bun-v`+bunVersion+`" \
    && test -x /home/vscode/.bun/bin/bun
`)
	}
	if want[string(schema.ToolchainRokit)] {
		out = append(out, `RUN tmp="$(mktemp -d)" && cd "$tmp" \
    && curl -fsSL -o rokit.zip "https://github.com/rojo-rbx/rokit/releases/download/v`+rokitVersion+`/rokit-`+rokitVersion+`-linux-$(uname -m).zip" \
    && unzip -q rokit.zip \
    && ./rokit self-install \
    && rm -rf "$tmp"
`)
	}
	return out
}

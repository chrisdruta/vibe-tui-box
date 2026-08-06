package builder

import (
	"strconv"
	"strings"

	"vibe/internal/schema"
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
	// grok's installer takes no version parameter and defaults to its
	// stable channel — the channel unversioned grok actually tracks
	// (the old "latest" BoM label predated reading the installer).
	grokChannel   = "stable"
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
	// chafa is the image half of the review stack: the sixel encoder
	// behind the tui's preview window (ctrl+click on an image path) —
	// tmux ingests what chafa emits. Pinned source build carrying v1's
	// exact version + checksum: Debian's 1.14.5 predates --probe and
	// the current sixel work, and upstream ships no arm64 static
	// binary. Single stage like tmux, which also defuses v1's staged-
	// copy trap (c1e4c28): the -dev packages keep their runtime libs
	// in the image, so build and runtime cannot drift apart.
	chafaVersion = "1.18.2"
	chafaSHA256  = "0b8d9ba9f347e8b6c0c71878217c9b0e478b4a42aa4babea0bf20840567239c2"
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
	// gh is the in-container push path (docs/configuration.md "GitHub
	// access" — the v2 revival of v1's per-project gh logins): rides
	// every agent image like the review stack, because agents commit and
	// a push-dead container was the v2 cutover's silent regression.
	// Pinned release artifact, per-arch checksums.
	ghVersion     = "2.96.0"
	ghSHA256AMD64 = "83d5c2ccad5498f58bf6368acb1ab32588cf43ab3a4b1c301bf36328b1c8bd60"
	ghSHA256ARM64 = "06f86ec7103d41993b76cd78072f43595c34aaa56506d971d9860e67140bf909"
	// nvim-treesitter's build-time parser compile shells out to the
	// tree-sitter CLI. The 0.25 line is the newest OFFICIAL binary
	// that runs on the bookworm-era bases: every 0.26.x prebuilt
	// links glibc 2.39 (Ubuntu 24.04 runners) vs the base's 2.36 —
	// the first rebuild proved it, and a cargo-built CLI was tried
	// and rejected the same day (a Rust toolchain in the layer is
	// exactly the heavyweight dep this image avoids). The README's
	// "CLI >= 0.26.1" floor is documentation, not enforcement:
	// install.lua only ever runs `tree-sitter build` for our parser
	// set (none carry `generate = true` in parsers.lua at the pinned
	// SHA — the one version-sensitive subcommand), verified end to
	// end by building a pinned grammar to a working .so with this
	// exact binary (2026-07-26). Release assets use x64/arm64 names;
	// glibc floors: 2.34 (x64) / 2.29 (arm64).
	treesitterCLIVersion     = "0.25.10"
	treesitterCLISHA256AMD64 = "8283ddba69253c698f6e987ba0e2f9285e079c8db4d36ebe1394b5bb3a0ebdfd"
	treesitterCLISHA256ARM64 = "07fbff8ae0eeb0d3e496e14fc1a30dcc730cc2c97d70e601e5357f2e51958af5"
)

// reviewPlugins are cloned into the root-owned native packpath
// (/opt/vibe/nvim/pack/vibe/start) at exactly these commits — no
// plugin manager, no runtime network, no plugin bytes on volumes (the
// marketplace-install rejection record). These SHAs are executable
// Lua: bump them deliberately, with a look at the upstream diff, never
// by reflex. diffview.nvim was here for one day and was retired by
// dogfood (2026-07-26): lazygit owns diff review, and oil.nvim owns
// the files surface (mini.files' floating columns read as broken
// inside a full-screen popup).
var reviewPlugins = []struct{ Repo, SHA string }{
	{"echasnovski/mini.nvim", "946ae64e0ee807ae3c41f382f0114b4ed4915b2c"},
	{"stevearc/oil.nvim", "b73018b75affd13fa38e2fc94ef753b465f770d7"},
	{"lewis6991/gitsigns.nvim", "31d6fb2d618bca1482b9f274751ead5f03461408"},
	{"folke/tokyonight.nvim", "cdc07ac78467a233fd62c493de29a17e0cf2b2b6"},
	{"nvim-treesitter/nvim-treesitter", "8b3a191c015dd66a92d51a112ed96af0aac13b63"},
}

// reviewParsers is the engine-owned treesitter language list, compiled
// once at image build (nvim-treesitter main branch, headless install)
// into a root-owned site dir — no runtime compiles. Engine-owned on
// purpose: a manifest knob here would grow without bound. The
// `requires` entries some of these carry in parsers.lua (ecma,
// html_tags, jsx) are query-only pseudo-parsers with no install_info —
// their queries ship inside the cloned plugin and nothing compiles, so
// they must NOT appear in this list (they would produce no .so and
// trip the exact-count guard below).
var reviewParsers = []string{
	"bash", "c", "css", "diff", "go", "gomod", "gosum", "html",
	"javascript", "json", "lua", "luau", "markdown", "markdown_inline",
	"python", "regex", "rust", "toml", "tsx", "typescript", "vim",
	"vimdoc", "yaml",
}

// agentRefreshArgPrefix names the PER-AGENT build args that bust the
// Docker layer cache for the channel-tracking (unversioned or
// channel-selecting) agent installers: VIBE_AGENT_REFRESH_CLAUDE,
// _CODEX, _GROK. Each layer embeds only its OWN arg, so one agent's
// version bump misses only that agent's layer — layer chaining still
// rebuilds everything after a miss, which is why the most volatile
// agent installs last (userLayers). The value is the channel's
// resolved upstream version (mintAgentRefresh), falling back to a
// rebuild timestamp when the probe failed; pinned agents and the
// system toolchains sit in plain layers no arg reaches. When a project
// has never rebuilt the args are absent.
const agentRefreshArgPrefix = "VIBE_AGENT_REFRESH_"

// AgentRefreshArgFor names the cache-buster build arg for one agent.
func AgentRefreshArgFor(kind schema.AgentKind) string {
	return agentRefreshArgPrefix + strings.ToUpper(string(kind))
}

// isChannel reports whether an agent version qualifier names a CHANNEL
// (a word: "stable", "latest", an npm dist-tag) rather than an exact
// pin (digit-leading). A channel is a moving target that refreshes
// exactly like an unversioned entry — freezing a channel in a cached
// layer is the staleness bug all over again.
func isChannel(v string) bool {
	return v != "" && (v[0] < '0' || v[0] > '9')
}

// AgentChannel returns the channel a channel-tracking agent resolves
// through — the manifest's channel word, or the engine default per
// kind — and "" for an exact pin (a plain cached layer: nothing to
// probe, nothing to bust).
func AgentChannel(spec schema.AgentSpec) string {
	if spec.Version != "" {
		if !isChannel(spec.Version) {
			return ""
		}
		return spec.Version
	}
	switch spec.Kind {
	case schema.AgentClaude:
		return claudeChannel
	case schema.AgentCodex:
		return codexChannel
	case schema.AgentGrok:
		return grokChannel
	}
	return ""
}

// InstallPart is one named row of the tools-image bill of materials —
// the unit build progress reports on. Chrome instructions (ARG, FROM,
// ENV, USER) attach to a neighboring part so every Dockerfile step maps
// to exactly one row.
type InstallPart struct {
	ID      string // stable slug ("tmux", "go", "claude", ...)
	Title   string // display name
	Detail  string // pin summary ("3.7b · source build"); base's is filled by the caller
	Refresh bool   // channel-tracking layer this build's refresh token will bust
}

// InstallPlan is the generated Dockerfile together with the map from
// classic-builder step ordinals to parts, computed at generation time —
// the daemon's "Step N/M" stream can then be rendered as part
// transitions without parsing instruction text.
type InstallPlan struct {
	Dockerfile []byte
	Parts      []InstallPart
	// StepPart maps 1-based build step ordinals (the classic builder
	// numbers every instruction) to indexes into Parts.
	StepPart []int
	// RefreshKinds lists the agents whose layers declare a per-agent
	// cache-buster arg (AgentRefreshArgFor), in layer order. Passing a
	// build arg without its declaration draws a daemon warning, so
	// callers gate the token values on this.
	RefreshKinds []schema.AgentKind
}

// installChunk is one generation unit: a text fragment plus the BoM
// part its instructions belong to. A zero-ID part marks chrome, whose
// instructions attach to the next part (or the last, at the tail).
type installChunk struct {
	part InstallPart
	text string
}

func chrome(text string) installChunk { return installChunk{text: text} }

// countInstructions counts Dockerfile instructions in a generated
// fragment: instruction lines start at column zero, while comments and
// backslash continuations never do. This holds by construction for
// engine-generated text; it is not a general Dockerfile parser.
func countInstructions(text string) int {
	n := 0
	for line := range strings.SplitSeq(text, "\n") {
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		n++
	}
	return n
}

// GenerateInstallPlan renders the install Dockerfile for the given
// selection, annotated with the bill of materials naming which part
// owns each build step. The Dockerfile bytes are a pure function of
// the selection (and the refresh flag) — canonical layer order,
// engine-pinned toolchains, manifest-pinned or channel-tracking
// agents — so identical inputs yield identical bytes and a warm
// Docker layer cache. The result always satisfies ValidateDockerfile.
// Nil means nothing to install.
//
// refresh weaves the AgentRefreshArg cache-buster into the UNVERSIONED
// agent layers only; a manifest-pinned agent installs its exact version
// in a plain layer no refresh can move, and the system toolchains are
// never touched. A selection with only pinned agents ignores refresh
// entirely.
func GenerateInstallPlan(agents []schema.AgentSpec, toolchains []schema.Toolchain, refresh bool) *InstallPlan {
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

	chunks := []installChunk{chrome("# Generated by the vibe engine from image.agents / image.toolchains.\n" +
		"# Not project input: regenerated every build, pins move with engine releases.\n" +
		"ARG " + BaseImageArg + "\n" +
		"FROM ${" + BaseImageArg + "}\n")}
	if entries := pathEntries(want); len(entries) > 0 {
		chunks = append(chunks, chrome("\nENV PATH="+strings.Join(entries, ":")+":${PATH}\n"))
	}
	if layers := rootLayers(want); len(layers) > 0 {
		chunks = append(chunks, chrome("\nUSER root\n"))
		for _, l := range layers {
			chunks = append(chunks, installChunk{part: l.part, text: "\n" + l.text})
		}
	}
	chunks = append(chunks, chrome("\nUSER vscode\n"))
	for _, l := range userLayers(want, pin, refresh) {
		chunks = append(chunks, installChunk{part: l.part, text: "\n" + l.text})
	}

	plan := &InstallPlan{}
	var b strings.Builder
	index := map[string]int{}
	pending := 0 // chrome instructions waiting for their neighboring part
	for _, c := range chunks {
		b.WriteString(c.text)
		n := countInstructions(c.text)
		if c.part.ID == "" {
			pending += n
			continue
		}
		idx, ok := index[c.part.ID]
		if !ok {
			idx = len(plan.Parts)
			index[c.part.ID] = idx
			plan.Parts = append(plan.Parts, c.part)
		}
		for i := 0; i < pending+n; i++ {
			plan.StepPart = append(plan.StepPart, idx)
		}
		pending = 0
	}
	// Tail chrome (USER vscode with no user layers) joins the last part.
	for i := 0; i < pending; i++ {
		plan.StepPart = append(plan.StepPart, len(plan.Parts)-1)
	}
	plan.Dockerfile = []byte(b.String())
	// The busted parts ARE the declared per-agent args: part IDs for
	// agent layers are the schema kind strings.
	for _, p := range plan.Parts {
		if p.Refresh {
			plan.RefreshKinds = append(plan.RefreshKinds, schema.AgentKind(p.ID))
		}
	}
	return plan
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

// Part identities for the BoM rows. The base part also absorbs the
// header chrome and the agent-state mount point; rokit's unzip guard
// shares the rokit part with its user-layer install.
func partBase() InstallPart {
	return InstallPart{ID: "base", Title: "base"}
}
func partRokit() InstallPart {
	return InstallPart{ID: "rokit", Title: "rokit", Detail: rokitVersion}
}

// rootLayers renders the layers that need root: the agent-state mount
// point, system toolchains, and apt dependencies. Fixed order:
// agent-state, tmux, go, node, rokit's unzip guard.
func rootLayers(want map[string]bool) []installChunk {
	var out []installChunk
	// The agent-state named volume mounts here; Docker initializes a
	// fresh volume from the image path, so baking the directory
	// vscode-owned is what makes the volume writable by the agent.
	out = append(out, installChunk{part: partBase(), text: `# Agent-state mount point: fresh named volumes inherit this ownership.
RUN mkdir -p /vibe/agent-state && chown vscode:vscode /vibe/agent-state
`})
	if wantsAgent(want) {
		// Pinned source build (see the tmuxVersion decision comment) at
		// /usr/local/bin/tmux — the path App.Agent probes first. Single
		// stage on purpose: ValidateDockerfile forbids extra build
		// stages, so the build deps stay in the image (a dev container
		// carries compilers anyway). jq feeds the statusline glue
		// (never the hot-path state hook).
		out = append(out, installChunk{part: InstallPart{ID: "tmux", Title: "tmux", Detail: tmuxVersion + " · source build"}, text: `# tmux ` + tmuxVersion + ` carries the persistent agent session (docs/architecture.md
# (agent sessions)): pinned source build with --enable-sixel — distro tmux
# drops sixel images on pane redraw and varies per base image. jq
# parses the Claude statusline JSON.
RUN apt-get update \
    && apt-get install -y --no-install-recommends jq build-essential byacc pkg-config libevent-dev libncurses-dev ca-certificates curl \
    && tmp="$(mktemp -d)" \
    && curl -fsSL -o "$tmp/tmux.tar.gz" "https://github.com/tmux/tmux/releases/download/` + tmuxVersion + `/tmux-` + tmuxVersion + `.tar.gz" \
    && echo "` + tmuxSHA256 + `  $tmp/tmux.tar.gz" | sha256sum -c - \
    && tar -C "$tmp" -xzf "$tmp/tmux.tar.gz" \
    && cd "$tmp/tmux-` + tmuxVersion + `" \
    && ./configure --prefix=/usr/local --enable-sixel \
    && make -j"$(nproc)" \
    && make install \
    && cd / \
    && rm -rf "$tmp" \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*
`})
		// Rides after tmux on purpose: that chunk already installed
		// build-essential/pkg-config/curl, this one adds only the image
		// codec -dev packages (whose runtime .so files stay — the
		// lockstep invariant is structural here, not a checklist).
		// ldconfig registers libchafa under /usr/local/lib.
		out = append(out, installChunk{part: InstallPart{ID: "chafa", Title: "chafa", Detail: chafaVersion + " · source build"}, text: `# chafa ` + chafaVersion + ` encodes preview images to sixel for the tui's preview
# window (docs/tui-layout.md "Preview window"); pinned source build —
# distro chafa predates --probe and the current sixel work.
RUN apt-get update \
    && apt-get install -y --no-install-recommends libglib2.0-dev libjpeg62-turbo-dev libpng-dev libwebp-dev libfreetype6-dev librsvg2-dev xz-utils \
    && tmp="$(mktemp -d)" \
    && curl -fsSL -o "$tmp/chafa.tar.xz" "https://hpjansson.org/chafa/releases/chafa-` + chafaVersion + `.tar.xz" \
    && echo "` + chafaSHA256 + `  $tmp/chafa.tar.xz" | sha256sum -c - \
    && tar -C "$tmp" -xJf "$tmp/chafa.tar.xz" \
    && cd "$tmp/chafa-` + chafaVersion + `" \
    && ./configure --prefix=/usr/local \
    && make -j"$(nproc)" \
    && make install \
    && ldconfig \
    && cd / \
    && rm -rf "$tmp" \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*
`})
		out = append(out, reviewLayers()...)
		out = append(out, installChunk{part: InstallPart{ID: "gh", Title: "gh", Detail: ghVersion}, text: `# gh (GitHub CLI): the in-container push path. Logins are per-project
# fine-grained tokens behind GH_CONFIG_DIR on the agent-state volume;
# lifecycle.sh post-start wires git to push through them
# (docs/configuration.md "GitHub access").
RUN tmp="$(mktemp -d)" \
    && case "$(uname -m)" in \
         x86_64)  arch=amd64; sha="` + ghSHA256AMD64 + `" ;; \
         aarch64) arch=arm64; sha="` + ghSHA256ARM64 + `" ;; \
         *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;; \
       esac \
    && curl -fsSL -o "$tmp/gh.tar.gz" "https://github.com/cli/cli/releases/download/v` + ghVersion + `/gh_` + ghVersion + `_linux_${arch}.tar.gz" \
    && echo "${sha}  $tmp/gh.tar.gz" | sha256sum -c - \
    && tar -C "$tmp" -xzf "$tmp/gh.tar.gz" \
    && install -m 0755 "$tmp/gh_` + ghVersion + `_linux_${arch}/bin/gh" /usr/local/bin/gh \
    && rm -rf "$tmp"
`})
	}
	if want[string(schema.ToolchainGo)] {
		out = append(out, installChunk{part: InstallPart{ID: "go", Title: "go", Detail: goVersion}, text: `# Go toolchain: official tarball pinned by version + per-arch checksum,
# upstream layout under /usr/local/go (PATH above carries its bin dirs).
RUN tmp="$(mktemp -d)" \
    && case "$(uname -m)" in \
         x86_64)  arch=amd64; sha="` + goSHA256AMD64 + `" ;; \
         aarch64) arch=arm64; sha="` + goSHA256ARM64 + `" ;; \
         *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;; \
       esac \
    && curl -fsSL -o "$tmp/go.tar.gz" "https://go.dev/dl/go` + goVersion + `.linux-${arch}.tar.gz" \
    && echo "${sha}  $tmp/go.tar.gz" | sha256sum -c - \
    && rm -rf /usr/local/go \
    && tar -C /usr/local -xzf "$tmp/go.tar.gz" \
    && rm -rf "$tmp"
`})
	}
	if want[string(schema.ToolchainNode)] {
		out = append(out, installChunk{part: InstallPart{ID: "node", Title: "node", Detail: nodeMajor + ".x"}, text: `# Node: required by npm-distributed agent CLIs and available standalone.
RUN curl -fsSL "https://deb.nodesource.com/setup_` + nodeMajor + `.x" | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*
`})
	}
	if want[string(schema.ToolchainRokit)] {
		out = append(out, installChunk{part: partRokit(), text: `# rokit ships as a zip; make sure unzip exists on lean bases.
RUN command -v unzip >/dev/null 2>&1 \
    || { apt-get update \
         && apt-get install -y --no-install-recommends unzip \
         && apt-get clean \
         && rm -rf /var/lib/apt/lists/*; }
`})
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
func reviewLayers() []installChunk {
	var out []installChunk
	out = append(out, installChunk{part: InstallPart{ID: "review", Title: "review", Detail: "nvim " + nvimVersion + " · lazygit " + lazygitVersion}, text: `# The bundled review stack (docs/tui-layout.md "Editor surfaces"):
# nvim + lazygit as pinned release artifacts — the container carries
# the review surface because a cold host is the norm.
RUN tmp="$(mktemp -d)" \
    && case "$(uname -m)" in \
         x86_64)  arch=x86_64; nvim_sha="` + nvimSHA256AMD64 + `"; lazygit_sha="` + lazygitSHA256AMD64 + `" ;; \
         aarch64) arch=arm64;  nvim_sha="` + nvimSHA256ARM64 + `"; lazygit_sha="` + lazygitSHA256ARM64 + `" ;; \
         *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;; \
       esac \
    && curl -fsSL -o "$tmp/nvim.tar.gz" "https://github.com/neovim/neovim/releases/download/v` + nvimVersion + `/nvim-linux-${arch}.tar.gz" \
    && echo "${nvim_sha}  $tmp/nvim.tar.gz" | sha256sum -c - \
    && tar -C /opt -xzf "$tmp/nvim.tar.gz" \
    && ln -sfn "/opt/nvim-linux-${arch}" /opt/nvim \
    && ln -sf /opt/nvim/bin/nvim /usr/local/bin/nvim \
    && curl -fsSL -o "$tmp/lazygit.tar.gz" "https://github.com/jesseduffield/lazygit/releases/download/v` + lazygitVersion + `/lazygit_` + lazygitVersion + `_linux_${arch}.tar.gz" \
    && echo "${lazygit_sha}  $tmp/lazygit.tar.gz" | sha256sum -c - \
    && tar -C "$tmp" -xzf "$tmp/lazygit.tar.gz" lazygit \
    && install -m 0755 "$tmp/lazygit" /usr/local/bin/lazygit \
    && rm -rf "$tmp"
`})
	var plugins strings.Builder
	plugins.WriteString(`# Review-stack plugins at pinned SHAs into a root-owned native
# packpath — no plugin manager, no runtime network; .git dirs stripped
# so the bytes are inert.
RUN mkdir -p /opt/vibe/nvim/pack/vibe/start`)
	for _, p := range reviewPlugins {
		name := p.Repo[strings.IndexByte(p.Repo, '/')+1:]
		dst := "/opt/vibe/nvim/pack/vibe/start/" + name
		plugins.WriteString(" \\\n    && git clone --no-checkout \"https://github.com/" + p.Repo + "\" \"" + dst + "\"")
		plugins.WriteString(" \\\n    && git -C \"" + dst + "\" checkout --detach " + p.SHA)
	}
	plugins.WriteString(" \\\n    && rm -rf /opt/vibe/nvim/pack/vibe/start/*/.git\n")
	out = append(out, installChunk{part: InstallPart{ID: "plugins", Title: "plugins", Detail: strconv.Itoa(len(reviewPlugins)) + " nvim plugins · SHA-pinned"}, text: plugins.String()})
	out = append(out, installChunk{part: InstallPart{ID: "parsers", Title: "parsers", Detail: "treesitter · " + strconv.Itoa(len(reviewParsers)) + " languages"}, text: `# Treesitter parsers for the engine-owned language list, compiled at
# image build (the tmux layer's build-essential compiles, the pinned
# OFFICIAL tree-sitter CLI drives — see the pin comment for why the
# 0.25 line) into a root-owned site dir the payload nvim config puts
# on the runtime path — no runtime compiles, no writable plugin state.
RUN tmp="$(mktemp -d)" \
    && case "$(uname -m)" in \
         x86_64)  tsarch=x64;   ts_sha="` + treesitterCLISHA256AMD64 + `" ;; \
         aarch64) tsarch=arm64; ts_sha="` + treesitterCLISHA256ARM64 + `" ;; \
         *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;; \
       esac \
    && curl -fsSL -o "$tmp/tree-sitter.gz" "https://github.com/tree-sitter/tree-sitter/releases/download/v` + treesitterCLIVersion + `/tree-sitter-linux-${tsarch}.gz" \
    && echo "${ts_sha}  $tmp/tree-sitter.gz" | sha256sum -c - \
    && gunzip "$tmp/tree-sitter.gz" \
    && install -m 0755 "$tmp/tree-sitter" /usr/local/bin/tree-sitter \
    && rm -rf "$tmp" \
    && mkdir -p /opt/vibe/nvim-data \
    && HOME=/tmp XDG_DATA_HOME=/opt/vibe/nvim-data nvim --headless \
       --cmd "set packpath^=/opt/vibe/nvim" \
       -c "lua require('nvim-treesitter').install({'` + strings.Join(reviewParsers, `','`) + `'}):wait(900000)" \
       -c "quitall!" \
    && test "$(ls /opt/vibe/nvim-data/nvim/site/parser/*.so | wc -l)" -eq ` + strconv.Itoa(len(reviewParsers)) + `
`})
	return out
}

// refreshBust returns the shell prefix that ties a channel-tracking
// agent layer's cache key to ITS OWN refresh arg: after variable
// expansion the RUN text embeds the arg's value (the channel's
// resolved version, or a rebuild timestamp when the probe failed), so
// the layer cache misses exactly when the value moves. Empty when not
// busting, which keeps that layer plainly cacheable.
func refreshBust(bust bool, kind schema.AgentKind) string {
	if !bust {
		return ""
	}
	return `: "agents-refresh=${` + AgentRefreshArgFor(kind) + `}" \
    && `
}

// refreshArgDecl declares a busted agent layer's own cache-buster arg,
// directly above its RUN. The placement is LOAD-BEARING, not chrome
// hygiene: the engine builds with the classic builder, where a changed
// arg value busts every RUN after the arg's DECLARATION (all following
// RUNs use declared args implicitly), not just the RUNs that reference
// it. Declared immediately above its own layer, each arg's bust
// boundary coincides exactly with what layer chaining would rebuild
// anyway — hoist these declarations together and per-agent busting
// silently collapses back to shared.
func refreshArgDecl(bust bool, kind schema.AgentKind) string {
	if !bust {
		return ""
	}
	return "# The arg's value tracks this agent's channel (resolved version,\n" +
		"# or a rebuild timestamp when the probe failed): unchanged upstream\n" +
		"# keeps the layer warm-cached, a real bump re-pulls.\n" +
		"ARG " + AgentRefreshArgFor(kind) + "\n"
}

// userLayers renders the layers that install as vscode into the home
// directory. Fixed order: bun, rokit, then agents as codex, grok,
// claude — the engine-pinned toolchains sit BEFORE the agents so a
// refresh-busted agent chain never re-runs them (branch-review
// remainder item 6), and claude sits LAST among the agents because it
// releases most often and a layer-cache miss rebuilds everything after
// it: the most volatile layer at the tail keeps a claude-only bump
// from re-pulling its neighbors. Channel-tracking agents gain their
// own per-agent cache-buster arg when refresh is set; manifest-pinned
// agents install their exact version in a plain layer no buster
// touches.
func userLayers(want map[string]bool, pin map[schema.AgentKind]string, refresh bool) []installChunk {
	bustFor := func(kind schema.AgentKind) bool {
		return refresh && (pin[kind] == "" || isChannel(pin[kind]))
	}
	// agentDetail names the moving-or-pinned target a BoM row reports:
	// the channel default, a manifest channel, or an exact pin.
	agentDetail := func(kind schema.AgentKind, channel string) string {
		v := pin[kind]
		switch {
		case v == "":
			return channel + " channel"
		case isChannel(v):
			return v + " channel"
		default:
			return v
		}
	}
	var out []installChunk
	if want[string(schema.ToolchainBun)] {
		out = append(out, installChunk{part: InstallPart{ID: "bun", Title: "bun", Detail: bunVersion}, text: `RUN curl -fsSL https://bun.sh/install | bash -s -- "bun-v` + bunVersion + `" \
    && test -x /home/vscode/.bun/bin/bun
`})
	}
	if want[string(schema.ToolchainRokit)] {
		out = append(out, installChunk{part: partRokit(), text: `RUN tmp="$(mktemp -d)" && cd "$tmp" \
    && curl -fsSL -o rokit.zip "https://github.com/rojo-rbx/rokit/releases/download/v` + rokitVersion + `/rokit-` + rokitVersion + `-linux-$(uname -m).zip" \
    && unzip -q rokit.zip \
    && ./rokit self-install \
    && rm -rf "$tmp"
`})
	}
	if want[string(schema.AgentCodex)] {
		// Unversioned tracks the npm dist-tag; a manifest pin is exact.
		codexSpec := codexChannel
		if v := pin[schema.AgentCodex]; v != "" {
			codexSpec = v
		}
		out = append(out, installChunk{part: InstallPart{ID: "codex", Title: "codex", Detail: agentDetail(schema.AgentCodex, codexChannel), Refresh: bustFor(schema.AgentCodex)}, text: refreshArgDecl(bustFor(schema.AgentCodex), schema.AgentCodex) + `RUN ` + refreshBust(bustFor(schema.AgentCodex), schema.AgentCodex) + `npm install -g --prefix /home/vscode/.local "@openai/codex@` + codexSpec + `"
`})
	}
	if want[string(schema.AgentGrok)] {
		out = append(out, installChunk{part: InstallPart{ID: "grok", Title: "grok", Detail: agentDetail(schema.AgentGrok, grokChannel), Refresh: bustFor(schema.AgentGrok)}, text: refreshArgDecl(bustFor(schema.AgentGrok), schema.AgentGrok) + `# Grok (xAI official). Its state (auth.json, config.toml) lives in
# ~/.grok with no env override, so ~/.grok is symlinked onto the
# agent-state volume path BEFORE install (fresh volumes inherit the
# dir from the image; lifecycle.sh materializes it on older volumes),
# making logins survive rebuilds like claude/codex relocation does.
# The installer symlinks grok/agent into GROK_BIN_DIR pointing at
# ~/.grok/downloads/, which the runtime volume mount would shadow — so
# the real binary is materialized instead.
RUN ` + refreshBust(bustFor(schema.AgentGrok), schema.AgentGrok) + `mkdir -p /vibe/agent-state/grok \
    && ln -s /vibe/agent-state/grok /home/vscode/.grok \
    && curl -fsSL https://x.ai/cli/install.sh | GROK_BIN_DIR=/home/vscode/.local/bin bash -s -- \
    && bin="$(readlink -f /home/vscode/.local/bin/grok)" \
    && rm -f /home/vscode/.local/bin/grok /home/vscode/.local/bin/agent \
    && cp "$bin" /home/vscode/.local/bin/grok \
    && ln -s grok /home/vscode/.local/bin/agent \
    && rm -rf /vibe/agent-state/grok/downloads
`})
	}
	// claude closes the agent chain (see the order comment above): the
	// near-daily releaser sits where its bump busts nothing downstream.
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
		out = append(out, installChunk{part: InstallPart{ID: "claude", Title: "claude", Detail: agentDetail(schema.AgentClaude, claudeChannel), Refresh: bustFor(schema.AgentClaude)}, text: refreshArgDecl(bustFor(schema.AgentClaude), schema.AgentClaude) + `# Claude Code: the installer's ` + target + `.
RUN ` + refreshBust(bustFor(schema.AgentClaude), schema.AgentClaude) + `curl -fsSL https://claude.ai/install.sh | bash -s -- ` + spec + ` \
    && test -x /home/vscode/.local/bin/claude
`})
	}
	return out
}

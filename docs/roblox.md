# Roblox integration recipe

A worked example of layering a real toolchain on the generic harness. The rule:
the harness installs **ecosystem machinery** (Rokit); the repository declares and
materializes its own tools.

## Install

```bash
~/dev/vibe-devcontainer-submodule/install.sh --preset roblox ~/dev/my-roblox-game
```

The `roblox` preset uses the Python 3.14 base image (for build/pipeline scripting
with `uv`), sets `INSTALL_ROKIT=true`, and adds the Luau LSP and Rojo VS Code
extensions. `rokit` is added to `DEV_REQUIRED_COMMANDS`.

## Project bootstrap

Tool versions come from the repository, not the image. With `rokit.toml` and
`wally.toml` present, the generic bootstrap already runs `rokit install` and
`wally install`; anything extra goes in the project hook:

```bash
# .devcontainer/project/post-create.sh
uv sync --frozen           # if not lockfile-detected already
wally-package-types --sourcemap sourcemap.json Packages 2>/dev/null || true
```

## Services (Rojo)

The harness never starts services. Start Rojo from the project hook or a tmux pane:

```bash
# .devcontainer/project/post-start.sh (keep idempotent)
pgrep -f "rojo serve" >/dev/null || (rojo serve &>/tmp/rojo.log &)
```

To reach Rojo from Roblox Studio on the Windows host, forward the port in the
project's `devcontainer.json`:

```jsonc
"forwardPorts": [34872],
"portsAttributes": { "34872": { "label": "Rojo" } }
```

Forwarded ports bind to loopback on the host.

## Studio bridge / Blender / MCP

Application integrations stay in the project's own configuration:

- A host-side Studio bridge that needs to call **into** the container uses the
  forwarded port above; the container reaching **out** to a host service needs the
  `--add-host=host.docker.internal:host-gateway` runArg
  (see [local-models.md](local-models.md) for the same pattern).
- Blender (headless) is heavy: prefer a Dev Container Feature or a Compose sidecar
  in the project over adding it to the shared Dockerfile.
- MCP server registration is agent configuration — script it in
  `project/post-create.sh` so it lands in the persisted agent state.

## Reviewing generated images and renders

For pipelines that produce image batches (Blender renders, generated
textures/sprites), point the harness image reviewer at the output directory in
`config.env` (`VIBE_PREVIEW_DIR`, `VIBE_PREVIEW_GLOB`) and review from the
terminal: `vibe review` in any host terminal, or `prefix+i` inside the agent
tmux session. Approve/reject verdicts append to a JSONL file the pipeline or
agent can act on — see [usage.md](usage.md#reviewing-images).

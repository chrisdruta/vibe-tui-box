# Local models: Ollama on the host

Don't mount the GPU into containers. On AMD + WSL2 + Docker Desktop that path is
unsupported (Docker's `--gpus` flag is NVIDIA-only, and ROCm-on-WSL explicitly
excludes containers), and on macOS containers can't see Metal at all. The setup
that works on both: run the inference server natively on the host, where the GPU
drivers are first-class, and let containers reach it over the host gateway.

Model weights live once on the host, every devcontainer shares the same server,
and containers stay slim.

## 1. Start Ollama

The same helper works on Windows (run from WSL) and macOS:

```bash
./.devcontainer/harness/scripts/host/start-ollama.sh --parallel 24
```

The helper:

- stops the Ollama tray / menu-bar app first (it would otherwise respawn the
  server with default settings), then any running server, and waits for port
  11434 to free;
- exports the tuning variables for this launch only — on WSL they are forwarded
  to the Windows process via `WSLENV` `/w` flags; nothing is written to the
  system environment;
- runs `ollama serve` in the foreground (Ctrl+C stops it; relaunch the desktop app
  afterwards if you want the tray icon back).

On macOS, install Ollama with `brew install ollama` or from
[ollama.com/download/mac](https://ollama.com/download/mac); Apple Silicon uses
Metal acceleration automatically, with unified memory serving as VRAM.

| Flag               | Default | Sets                       |
| ------------------ | ------- | -------------------------- |
| `--parallel`       | 24      | `OLLAMA_NUM_PARALLEL`      |
| `--kv-cache-type`  | `q8_0`  | `OLLAMA_KV_CACHE_TYPE`     |
| `--context-length` | 4096    | `OLLAMA_CONTEXT_LENGTH`    |
| `--max-loaded`     | 1       | `OLLAMA_MAX_LOADED_MODELS` |

KV cache is allocated as `context_length × parallel` up front; on a 16 GB card most
VRAM is weights. After the first batch, check `ollama.exe ps` — the model must show
**100% GPU**. If it doesn't, restart with a lower `--parallel`: spilling weights to
CPU costs far more than parallelism gains.

## 2. Route the container to the host

Add to the project's `runArgs` in `devcontainer.json` (deliberately opt-in,
not part of the generic harness):

```jsonc
"--add-host=host.docker.internal:host-gateway"
```

Then `dev rebuild`. On Docker Desktop for Mac, `host.docker.internal` resolves
without this runArg — adding it anyway is harmless and keeps the project config
portable across both hosts.

## 3. Point tooling at it

Ollama speaks the OpenAI API; e.g. in the project `.env`:

```bash
OPENAI_BASE_URL=http://host.docker.internal:11434/v1
OPENAI_API_KEY=ollama   # placeholder; Ollama ignores it
```

Anything started via `dev agent` / `dev run` picks these up.

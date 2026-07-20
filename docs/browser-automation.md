# Browser automation (playwright-cli)

Gives coding agents a headless Chromium they can drive from the shell —
navigate, snapshot the accessibility tree, screenshot pages and read them back.
Uses the [Playwright Agent CLI](https://playwright.dev/agent-cli/introduction)
(`@playwright/cli`), which is designed for token-efficient agent use.

The split follows the harness policy: OS libraries bake in via a project
image extension (`.vibe/Dockerfile` — see [extending.md](extending.md);
there is no runtime sudo, so apt can only run at build time); the browser
binary downloads at post-create into the persistent agents volume;
everything else is project-owned.

Verified against a Next.js project (nimbus) on the Debian base image, amd64.

## Per-project recipe

1. **Image extension** — easiest at install time: `install.sh --extras
   playwright` (seeds `.vibe/Dockerfile` + the compose block and implies
   Node). On an existing project, copy
   `examples/extensions/playwright/Dockerfile` to `.vibe/Dockerfile`
   (+ the dockerignore template) per [extending.md](extending.md), enable
   Node in the base args, and persist browser downloads across rebuilds:

   ```yaml
   services:
     base:
       build:
         args:
           INSTALL_NODE: "true"
     dev:
       image: ${VIBE_PROJECT_NAME}-dev
       build:
         context: ./.vibe
         args:
           VIBE_BASE_IMAGE: ${VIBE_PROJECT_NAME}-base
           # Optionally pin the playwright version that resolves the apt
           # dependency list (default: latest):
           # PLAYWRIGHT_VERSION: "1.50.1"
       environment:
         PLAYWRIGHT_BROWSERS_PATH: /home/vscode/.agents/ms-playwright
   ```

2. **Dev dependency** — `bun add -d @playwright/cli` (or the npm/pnpm
   equivalent). Its bundled Playwright version always matches the browser
   revision it downloads.

3. **`project/post-create.sh`** — download the browser (no-op once the volume
   has it):

   ```bash
   bunx playwright-cli install-browser chromium
   ```

4. **`.playwright/cli.config.json`** in the repo root — both settings are
   required in this container:

   ```json
   {
     "browser": {
       "browserName": "chromium",
       "launchOptions": {
         "chromiumSandbox": false
       }
     }
   }
   ```

   - `browserName`: the CLI defaults to branded Chrome, which is not installed.
   - `chromiumSandbox: false`: Chromium's own sandbox needs unprivileged user
     namespaces, which the harness's seccomp / `no-new-privileges` hardening
     blocks. The container is the isolation boundary here, so disabling the
     inner sandbox is acceptable; prefer pages you trust (your own dev server).

5. **`.gitignore`** — the CLI writes session artifacts next to the repo:

   ```
   .playwright/*
   !.playwright/cli.config.json
   .playwright-cli/
   ```

6. **Agent skills (optional)** — `bunx playwright-cli install --skills`
   installs a command-reference skill to `.claude/skills/playwright-cli/`;
   commit it so agents get the full reference on demand. Add a short note to
   the project's CLAUDE.md/AGENTS.md pointing at the workflow (open →
   snapshot/screenshot → close; screenshots to a scratchpad, not the repo).

## Smoke test

```bash
bunx playwright-cli open http://localhost:3000
bunx playwright-cli screenshot --filename /tmp/check.png
bunx playwright-cli close
```

If launch fails with `error while loading shared libraries`, the image was not
rebuilt after adding the feature. If it fails with `No usable sandbox!`, the
`chromiumSandbox: false` config is missing.

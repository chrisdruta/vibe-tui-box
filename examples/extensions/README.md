# Image extensions

Worked examples of the project image-extension mechanism
([docs/extending.md](../../docs/extending.md)): a `.vibe/Dockerfile` that
chains onto the shared harness image, for system-level tooling the shared
image deliberately doesn't carry.

To use one, from your project root:

```bash
cp .vibe/harness/examples/extensions/blender/Dockerfile .vibe/Dockerfile
cp .vibe/harness/src/templates/extensions/dockerignore .vibe/.dockerignore
# then in .vibe/compose.yaml, add under services:
#   dev:
#     image: ${VIBE_PROJECT_NAME}-dev
#     build:
#       context: ./.vibe
#       args:
#         VIBE_BASE_IMAGE: ${VIBE_PROJECT_NAME}-base
./vibe rebuild
```

(`install.sh --extras playwright` does all of this for the playwright one.)

- **`playwright/`** — Chromium system libraries for `@playwright/cli`
  browser automation (needs Node in the base image; recipe:
  [docs/browser-automation.md](../../docs/browser-automation.md)).
- **`blender/`** — headless Blender via Debian's package (amd64 + arm64).

The rules that keep extensions safe (enforced by convention + the runtime
compose base): start `FROM ${VIBE_BASE_IMAGE}`, do root work only at build
time, end with `USER vscode`, and keep installers multi-arch
(amd64 + arm64). Everything else is ordinary Dockerfile.

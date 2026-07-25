---
description: Author a vibe rebuild request (.vibe/requests/<id>.json) for the host operator to review
argument-hint: [what needs to change and why]
---

You are running inside a vibe-managed container. You cannot change the
container you run in — no ports, mounts, packages, services, or env
files can be altered from inside. The one channel is a request file the
HOST operator reviews and approves; approval is bound to an immutable
candidate digest, never to your file.

Author that request now.

1. Work out the exact `.vibe/vibe.yaml` change needed. If the argument
   text ("$ARGUMENTS") doesn't pin it down, inspect the workspace (the
   manifest, failing command, or missing dependency) until you can state
   the change precisely. Where the manifest must change, prefer editing
   `.vibe/vibe.yaml` yourself first — the request's candidate is
   compiled from current workspace inputs at poll time, so the edit IS
   the payload of the request.
2. Write `.vibe/requests/<id>.json` with exactly these fields:

   ```json
   {"format": 1, "id": "<kebab-case-slug>", "kind": "rebuild",
    "reason": "<why this is needed — one or two sentences>",
    "summary": "<exactly what changes, e.g. 'add 127.0.0.1:34872:34872'>"}
   ```

   The id must be a short kebab-case slug unique under `.vibe/requests/`.
   Keep reason and summary to plain single-line text: the operator reads
   them through a control-character-escaping encoder, and the TRUSTED
   half of their decision is the engine's own plan diff — your summary
   should match what the diff will show, or the mismatch will read as
   suspicious.
3. Tell the user the request is filed and that, on the host, they review
   it with `vibe request list`, then `vibe request show <id>`, and apply
   it with `vibe request approve <digest>`.

Do not edit files under `/vibe/` (read-only engine mounts), and do not
attempt to restart, rebuild, or reconfigure containers yourself.

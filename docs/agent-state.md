# Agent state and multi-agent use

## The state volume

Each project gets one named Docker volume, mounted at `~/.agents` in the container:

```text
agent-state-<workspace-folder-basename>
├── claude/   # CLAUDE_CONFIG_DIR
├── codex/    # CODEX_HOME
├── gh/       # GH_CONFIG_DIR — `gh auth login` persists per project
└── grok/     # ~/.grok is a symlink here (Grok has no config-dir env override)
```

Log in to each agent once per project; logins survive `vibe rebuild` and image
upgrades. Per-project logins are deliberate compartmentalization, not an
accident — see [positioning.md](positioning.md). The volume mountpoint is pre-created in the image owned by `vscode` —
necessary because with sudo removed and all capabilities dropped, a root-owned
volume could never be repaired from inside the container.

## Installed agents

| Agent       | Command  | Enable via            | Auth                          |
| ----------- | -------- | --------------------- | ----------------------------- |
| Claude Code | `claude` | default               | OAuth or `ANTHROPIC_API_KEY`  |
| Codex CLI   | `codex`  | `INSTALL_CODEX=true`  | OAuth or `OPENAI_API_KEY`     |
| Grok Build  | `grok`   | `INSTALL_GROK=true`   | OAuth or `XAI_API_KEY`        |

`DEV_AGENT_CMD` in `config.env` selects the default for `vibe agent`; run the others
with `vibe run codex` / `vibe run grok`, or side by side in tmux panes via `vibe shell`.
Agents can also invoke each other as subprocesses (e.g. Claude shelling out to
`codex` or `grok` to cross-check work) — they share the workspace and the
`.env`-loaded credentials of the process that spawned them.

When both `claude` and `codex` are installed, post-create also installs OpenAI's
[Codex plugin for Claude Code](https://github.com/openai/codex-plugin-cc) into the
state volume (user scope — nothing lands in the project repo). Claude Code sessions
then get `/codex:review`, `/codex:adversarial-review`, `/codex:rescue`,
`/codex:transfer`, and job management (`/codex:status`, `/codex:result`,
`/codex:cancel`), all riding on the container's `codex` login. The install needs the network once;
if container creation happens offline it warns and moves on — rerun post-create or
`claude plugin install codex@openai-codex` later. Remove it with
`claude plugin uninstall codex@openai-codex` (note: the next container *rebuild*
reinstalls it as long as the Codex CLI is in the image).

Grok's binary is materialized into `~/.local/bin` at image build time (its installer
would otherwise symlink into `~/.grok/downloads`, which the volume shadows); its
self-update therefore does not stick — update Grok by rebuilding the image.

## Worktrees and naming

The volume name uses only the workspace folder **basename**:

- Different worktree folder names (`my-project`, `my-project-feature-x`) get
  **separate** state volumes → separate logins, full isolation.
- Two projects whose folders share a basename (e.g. `~/dev/a/app` and `~/dev/b/app`)
  **collide** on the same volume — rename one folder, or change the volume `source=`
  in that project's `compose.yaml` to a unique key.
- The same `source=` edit can also **deliberately** share one volume — logins and
  session history included — across projects. That trades away per-project
  isolation; see [positioning.md](positioning.md#why-logins-are-per-project)
  before doing it.

## Resetting state

```bash
docker volume ls | grep agent-state
docker volume rm agent-state-<name>      # container must not be running
```

The next `vibe up` recreates it empty; agents will ask to log in again.

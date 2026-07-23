# Architecture v2 — adversarial design review (Codex)

_Verbatim record. Reviewer: Codex (GPT-5.x) via the codex-rescue subagent,
2026-07-23. Target: `docs/architecture-v2.md` (draft), with `docs/security.md`
as the trust baseline. Lens: adversarial trust-chain / security review._

_Verdict: do not advance to implementation until the six Critical findings
and the invariant itself are made internally consistent. This document is the
source of truth for the v2 revision. The 2026-07-23 revision of
[`architecture-v2.md`](architecture-v2.md) addresses every Critical, High, and
Medium finding; implementation remains gated on the M0 design proofs recorded
there._

---

# Adversarial design review

## 1. Trust chain described by v2

The proposed chain is:

1. A stable host shim in `~/.vibe/bin/vibe` selects a host-owned project record.
2. That record selects a release under `~/.vibe/versions/<version>/`.
3. The shim verifies the binary and embedded payload against `manifest.json`, then executes the binary.
4. The binary snapshots workspace-controlled inputs into host-owned state.
5. A closed Go schema converts `vibe.yaml` into generated Compose.
6. Only generated Compose is sent to Docker; the workspace cannot author Compose directly.
7. Docker provides the outer isolation boundary.
8. A read-only overmount exposes the same trusted payload inside the container.
9. Human approval establishes trust in release pins and broker-requested rebuilds.

Trusted components are therefore the shim, store, project registry, release verifier, Go parser/renderer, Docker/tmux executables, human approval UI, and release infrastructure.

Untrusted inputs include all workspace content, `.git`, `vibe.yaml`, the extension Dockerfile, broker requests, project hooks, `.env`, image contents, Docker/registry responses, release downloads, container output, and terminal bytes.

The central problem is that the quoted invariant is stronger than the design actually implements. v2 does feed daemon-facing values derived from container-writable input to Docker, and deliberately feeds the extension Dockerfile itself to `docker build`. The design needs either a narrower invariant or a substantially narrower project schema.

## 2. Concrete attack scenarios

### C0 — The stated invariant is false by design

**Severity: Critical**

- **Entry point:** Any malicious project or compromised container can edit `vibe.yaml` and `.vibe/Dockerfile`.
- **Exploitation:** The attacker selects image references, environment values, ports, mount paths, sidecars, and arbitrary Dockerfile instructions. The renderer serializes those values into Compose, while the Dockerfile is sent directly to `docker build`.
- **Impact:** Before considering any parser bypass, bytes or semantic values chosen by the container are already being fed to the Docker daemon. Release attestation proves the engine's provenance; it does not make the workload description trusted.
- **Broken statement:** The Premise says the host never "feeds to the docker daemon any byte a container could have written." The project-surface section explicitly makes the Dockerfile "container-writable input to `docker build`."

**Required design change:** Choose one:

- Preserve the literal invariant by allowing only opaque identifiers selecting host-trusted, predeclared configurations. Remove arbitrary image references, environment values, paths, ports, sidecars, and extension Dockerfiles from safe mode.
- Or restate the invariant accurately: untrusted workspace bytes are never executed by the host and reach Docker only through a canonical, capability-bounded typed model; Dockerfiles and unsafe capabilities are separately trusted human inputs.

Under the defender goal supplied for this review, the first option is the compliant one.

---

### C1 — Broker approval is detached from the state that gets built

**Severity: Critical**

- **Entry point:** The attacker controls `vibe.yaml`, the Dockerfile, and request timing.
- **Exploitation:**
  1. Submit a benign rebuild request.
  2. Let the host render the diff described in broker step 3.
  3. After the user has inspected it, replace the manifest or Dockerfile.
  4. The user approves.
  5. Broker step 4 performs a "fresh snapshot," thereby snapshotting the attacker's replacement rather than the reviewed state.
- **Impact:** Docker receives unapproved configuration. In dev mode, the same approval also causes unreviewed harness source to become a host executable through `dev sync`.
- **Broken assumption:** "Approve = normal `vibe rebuild` path with fresh snapshot." The freshness is precisely the vulnerability: approval is not bound to a snapshot digest.

**Minimal mitigation:** Snapshot the complete candidate first, store it in host-owned immutable state, calculate its digest, render the diff from that snapshot, and bind the approval to that digest. Build only that snapshot. If the workspace changes meanwhile, expose it as a new pending candidate.

---

### C2 — `EvalSymlinks` cannot authorize a later Docker bind mount

**Severity: Critical**

- **Entry point:** The attacker can mutate a workspace mount-source directory concurrently with `vibe rebuild`.
- **Exploitation:**
  1. Create a real `models/` directory inside the workspace.
  2. Reference it from `runtime.mounts`; `EvalSymlinks` confirms that it is inside the workspace.
  3. After validation, rename the directory and replace the original pathname with a symlink to `/`, a host credential directory, or `/var/run/docker.sock`.
  4. Docker later reopens and resolves that pathname when creating the bind.
- **Impact:** An arbitrary host path can be mounted into the container. A read-only bind still exposes host secrets, and read-only status does not prevent connecting to a mounted Unix socket when permissions allow.
- **Broken assumptions:** "Mount sources must resolve … inside the workspace" and the claimed prohibition against sockets and host binds. Canonicalizing a pathname does not freeze the directory entry at that pathname.

**Minimal mitigation:** Safe mode cannot support live bind mounts sourced from attacker-writable workspace subpaths. Copy the selected content into a host-owned immutable snapshot or named volume before use. Otherwise require `--unsafe`. `openat2` or `O_NOFOLLOW` during validation does not solve the later daemon reopen.

---

### C3 — Generated Compose still has a second parser and interpolation phase

**Severity: Critical unless explicitly resolved**

- **Entry point:** The attacker controls strings in `vibe.yaml`; depending on Compose invocation, it may also control the project `.env`.
- **Exploitation:**
  1. Place `${NAME}`, `$NAME`, or `$$` sequences in mount paths, images, ports, targets, or environment values.
  2. The Go schema validates the pre-interpolation value.
  3. Docker Compose interpolates the generated YAML afterward.
  4. If Compose uses the workspace as its project directory, its implicit `.env` supplies attacker-controlled substitutions. Even without that, inherited host variables can leak into the container or alter validated values.
- **Impact:** The daemon receives a different model from the one validated by Go. Mount-source validation, image allowlists, target deny-lists, and secret separation can all be bypassed.
- **Broken assumption:** Open question 6 says interpolation "is disabled in rendered output by construction," but the design specifies no escaping, rejection, environment scrub, or post-interpolation enforcement. This is weaker than v1's explicit "rendered under a scrubbed environment" requirement.

**Minimal mitigation:** Prefer the Docker API over Compose. If Compose remains:

- Reject or correctly escape `$` in every user-derived daemon-facing scalar.
- Run under a fixed host-owned working/project directory.
- Supply an empty host-owned interpolation environment.
- Clear all `COMPOSE_*` variables and unapproved host variables.
- Validate the final normalized model produced under exactly the same environment used for execution.
- Never let workspace `.env` participate in Compose evaluation.

---

### C4 — Rejection writes permit host-file clobber through symlinks

**Severity: Critical**

- **Entry point:** The attacker controls `.vibe/requests/`.
- **Exploitation:**
  1. Create `<ulid>.denied.json` as a symlink to `~/.bashrc`, the host shim, a tmux configuration, or another host-user file.
  2. Submit the corresponding request.
  3. When the user rejects it, the host follows the symlink while writing the denial response.
- **Impact:** Arbitrary host-user file overwrite or truncation. Targeting a shell startup file or trusted launcher can turn the next invocation into host code execution.
- **Broken assumption:** Broker step 5 says rejection "writes `.vibe/requests/<ulid>.denied.json`." Merely testing a "symlinked requests dir" does not make individual output files safe.

**Minimal mitigation:** Do not write from the host into attacker-controlled workspace paths. Store broker results under host-owned state and expose them through a read-only mount or container-side command. If workspace writes are unavoidable, use directory-FD-relative, no-follow, beneath-only creation with `O_CREAT|O_EXCL`; never overwrite a pre-existing path.

---

### C5 — Dev mode turns container-writable Go source into host code

**Severity: Critical**

- **Entry point:** A dev-mode project whose repository is writable by the container.
- **Exploitation:**
  1. Modify `cmd/vibe`, `internal/store`, or any other Go source to include a host payload.
  2. Submit an ordinary rebuild request.
  3. Broker step 4 automatically runs `dev sync`.
  4. The pinned builder compiles the malicious source.
  5. The output is registered and later executed on the host.
- **Impact:** Direct arbitrary host execution. The pinned builder protects the host toolchain; it does not establish trust in the source or resulting executable.
- **Broken assumptions:** "A compromised workspace cannot inject a host toolchain" is irrelevant to malicious source. "Dev sync runs first" also hides this trust transition behind an environment-rebuild approval whose displayed diff excludes Go source.

**Minimal mitigation:** Never run `dev sync` automatically from the broker. More fundamentally, executing a binary derived from container-writable source is incompatible with the stated invariant. Dev mode must either:

- explicitly declare that the boundary is disabled and require separate source-snapshot approval, or
- keep the dev engine inside a container and never execute it on the host.

A source hash names hostile code; it does not make it trusted.

---

### C6 — Default release verification permits origin/network compromise

**Severity: Critical**

- **Entry point:** The attacker controls the canonical release origin, a trusted TLS interception point, or another component capable of replacing both artifact and checksum; `gh` is absent.
- **Exploitation:**
  1. Serve a malicious binary and matching `SHA256SUMS`.
  2. TLS succeeds.
  3. Default `prefer` continues because attestation verification is unavailable.
  4. The binary unpacks or supplies its own payload and manifest.
  5. Future shim verification faithfully authenticates the attacker's installed state.
- **Impact:** Persistent arbitrary host execution.
- **Broken assumptions:** TLS plus checksum from the same origin gives transport integrity, not independent publisher authentication. This is weaker than security.md's first-contact promise of publisher authentication.

**Minimal mitigation:** Make provenance verification native to the trusted binary and default to `require`. Pin the expected issuer, repository, workflow, ref/tag policy, and artifact digest. Do not make `gh` availability part of the root of trust. An explicit one-invocation unsafe import may exist, but it must display and record the exact digest and unsafe status.

---

### H1 — Snapshotting has lost v1's symlink/special-file guarantees

**Severity: High**

- **Entry point:** The attacker controls the Dockerfile, `.env`, and files recursively copied into the extension context.
- **Exploitation:** Race a regular file into a symlink to `/proc/self/environ`, host credentials, or another host file while the host snapshots or renders a diff. A recursive copier that follows links can copy the host file into the build context; a diff reader can disclose it in the UI.
- **Impact:** Host-secret disclosure to the container or feeding unintended host bytes to Docker.
- **Broken assumption:** v2 says only that the context is "snapshotted host-side." The v1 baseline explicitly rejected symlinks, gitlinks, and special files and materialized through a controlled object path.

**Minimal mitigation:** Define an FD-relative snapshot algorithm: no symlinks, devices, sockets, FIFOs, hardlink surprises, or traversal; bounded file count and aggregate size; `openat2(RESOLVE_BENEATH|RESOLVE_NO_SYMLINKS)` where available; `openat`/`fstat` verification elsewhere; host-owned temporary destination followed by atomic rename. Snapshot an explicit allowlist, not `.vibe/` wholesale.

---

### H2 — Host executable and environment resolution is unspecified

**Severity: High**

- **Entry point:** A malicious repo contains `docker`, `gh`, `git`, or `tmux` binaries, and the host has `.` or a workspace-relative directory in `PATH`, or activates project-controlled environment settings.
- **Exploitation:** The engine invokes a tool through `exec.LookPath`; the workspace executable runs on the host before any schema or snapshot gate. Environment variables such as `LD_PRELOAD`, `DYLD_*`, `GIT_*`, `DOCKER_*`, `COMPOSE_*`, `TMUX_TMPDIR`, `BASH_ENV`, or `ENV` can similarly redirect trusted commands.
- **Impact:** Immediate host code execution or redirection to an attacker-controlled Docker daemon/tmux socket.
- **Broken assumption:** The design trusts `gh`, Docker, git, and tmux implicitly but carries none of the prior H4–H8 path/PATH/environment hardening into v2.

**Minimal mitigation:** Run host tools under a fixed minimal environment and trusted PATH, resolve executables outside all workspace roots, reject symlinked/workspace-resident tools, invoke them with argv rather than a shell, and explicitly control Docker and tmux endpoint variables.

---

### H3 — Extension Dockerfiles expose the evolving BuildKit frontend surface

**Severity: High**

- **Entry point:** The attacker controls `.vibe/Dockerfile`.
- **Exploitation:** Use a custom `# syntax=` frontend, arbitrary `FROM`, build networks, remote contexts supported by the selected frontend, or entitlements enabled in the host builder. The design only comments that the file should use `FROM ${VIBE_BASE_IMAGE}`; it does not define an enforceable Dockerfile subset.
- **Impact:** Untrusted instructions and network-delivered build frontend code are handed directly to a privileged host daemon component. The risk includes BuildKit/daemon vulnerabilities and any host resources exposed by builder configuration.
- **Broken assumption:** "Same residual as v1" is inconsistent with the new claim of enforcement "by construction."

**Minimal mitigation:** Remove extension Dockerfiles from safe mode. Otherwise make their use a separately approved unsafe operation, disable custom syntax/frontends and dangerous entitlements, use an isolated rootless builder, fixed network policy, digest-pinned base/frontend, and a minimal snapshotted context.

---

### H4 — The approval UI is itself attacker-controlled

**Severity: High**

- **Entry point:** Attacker-controlled `reason`, `summary`, filenames, Dockerfile lines, YAML values, Docker errors, and diff content.
- **Exploitation:** Inject ANSI CSI sequences, OSC 52 clipboard commands, cursor repositioning, screen clearing, hyperlinks, bidi overrides, terminal-title changes, or fake approval chrome.
- **Impact:** The attacker can hide malicious changes, display a false "verified" state, confuse which action is selected, manipulate the clipboard, or exercise terminal-emulator vulnerabilities.
- **Broken assumption:** Security.md previously treated terminal bytes as residual exposure. v2 now places a security decision—the approval ceremony—inside that surface, so it is no longer merely residual.

**Minimal mitigation:** Render untrusted content through a terminal-safe encoder: strip or visibly escape all C0/C1 controls except normalized newline/tab, reject bidi controls, normalize invalid UTF-8, cap lines and widths, and never invoke an external pager. Render trusted approval chrome after the untrusted content and include the approved digest.

---

### H5 — Alternate mounts and exact overmounts remain expressible

**Severity: High**

- **Entry point:** The attacker controls `runtime.mounts` and sidecar named volumes.
- **Exploitation:** Select `.vibe/harness`, the workspace target, the agent-state target, or a protected target's parent as a custom mount target. Compose normalizes volumes by target, so duplicate targets may replace an earlier required mount depending on render order. A sidecar may also request an existing named volume if names are not forcibly project-scoped.
- **Impact:** The trusted payload overmount can be replaced, doctor/lifecycle behavior can be forged, and other projects' agent-state credentials may be exposed.
- **Broken assumption:** The design says exact mounts "cannot be expressed, overridden, or removed," but only constrains custom mount sources. It does not specify target deny-lists, target uniqueness, normalized-model enforcement, or named-volume scoping.

**Minimal mitigation:** Reject mount targets that equal or intersect protected targets; enforce unique targets after Compose normalization; reconstruct and verify exact required mounts last. Generate named-volume names solely from the host project digest, prohibit external/name overrides, and apply all hardening to every sidecar.

---

### H6 — Release and dev identities are names, not immutable artifact identities

**Severity: High**

- **Entry point:** A release tag is reissued, a concurrent acquisition installs different bytes for the same version, or a dev build is reproduced under different builder/network state.
- **Exploitation:** `versions/<version>` and `versions/dev-<source-hash>` identify mutable build processes. The same dev source hash can produce different binaries because the builder tag, downloaded modules, target platform, or network responses differ.
- **Impact:** One project can execute bytes different from those originally trusted. A global `dev-<hash>` path can affect another project despite the policy claim that it "never satisfies another project's pin."
- **Broken assumption:** The trust record stores a pin/mode, while the trusted executable identity should be its artifact digest plus provenance.

**Minimal mitigation:** Store versions by artifact digest and never overwrite an existing digest directory. A release record must bind version, artifact digest, and verified provenance. Dev identity must also include project-record ID, source Merkle root, builder image digest, dependency state, target, and output digest.

---

### H7 — `env_file` path safety and host-child separation are undefined

**Severity: High**

- **Entry point:** The attacker controls `env_file`, its symlink target, and its contents.
- **Exploitation:** Point it outside the workspace, replace it with a symlink between checks, or provide variables such as `DOCKER_HOST`, `PATH`, `LD_PRELOAD`, `COMPOSE_FILE`, or `TMUX_TMPDIR`. If the implementation merges these into `cmd.Env` for `docker exec`, they affect the host process instead of merely the container.
- **Impact:** Host-file reads, host tool redirection, secret leakage, or host code execution.
- **Broken assumption:** The mount-source rule does not cover `env_file`. "Explicit per-process loading" does not specify whether variables are passed as Docker container environment or inherited by the host Docker client.

**Minimal mitigation:** Require a workspace-relative, no-symlink file and snapshot it safely. Parse dotenv literally—no substitution or command semantics. Never merge its values into a host subprocess environment; use Docker API fields or explicit `--env=KEY=VALUE` argv entries. Reject invalid keys, NUL, excessive values, and duplicates.

---

### H8 — Dev snapshot enumeration may revive git filter/hook execution

**Severity: High design gap**

- **Entry point:** The attacker controls `.git/config`, hooks, filters, fsmonitor settings, credential helpers, and repository contents.
- **Exploitation:** If `dev sync`, hashing, changelog generation, or snapshotting invokes host `git`, commands such as status/diff/archive can activate repo configuration, fsmonitor processes, filters, pagers, or external diff commands.
- **Impact:** Host execution before the trusted build-container boundary.
- **Broken assumption:** v2 deletes the mirror and says "hash of the source tree," but does not define a git-free walker. v1 specifically avoided porcelain against the workspace and used archive materialization to avoid hooks.

**Minimal mitigation:** Never invoke host git against an attacker-writable repository. Enumerate and hash via an FD-safe filesystem walker with an explicit ignore policy. If git semantics are required, run git inside a disposable container with no host credentials and only a read-only source snapshot.

---

### H9 — Project discovery and tmux/fleet dispatch are underspecified shell boundaries

**Severity: High**

- **Entry point:** Attacker-controlled root names, paths containing shell/tmux metacharacters, nested `.vibe` directories, or project display names.
- **Exploitation:** Cause the shim to select the wrong trust record through symlinked/nested roots, or inject path/name content into tmux `run-shell`, format strings, session commands, or palette actions.
- **Impact:** Wrong-project trust reuse or host command execution.
- **Broken assumption:** "The shim resolves cwd → project record" and "picking a stopped one runs its `up`" do not specify canonical-root matching or shell-free dispatch.

**Minimal mitigation:** Discover projects by walking canonical filesystem paths without git; match a host-owned canonical root/device identity; reject symlink roots. Keep untrusted names out of tmux shell strings and formats. Dispatch actions inside the Go process with argv-only subprocess calls.

---

### H10 — Mutable image tags undermine the attested-release story

**Severity: High**

- **Entry point:** Registry compromise, tag mutation, or malicious user-selected sidecar/base image.
- **Exploitation:** `mcr...:debian`, `postgres:17`, and `golang:<pin>` resolve to different content after the release was attested. Image extension and bootstrap downloads introduce further mutable inputs.
- **Impact:** The actual code run inside the isolation boundary is not determined by the trusted release. A malicious builder image also controls the dev artifact later executed on the host.
- **Broken assumption:** "The trusted unit is a release" is incomplete when the release delegates execution to mutable tags and network downloads.

**Minimal mitigation:** Digest-pin the shared base, BuildKit frontend, and dev builder. Record resolved digests for project images and display changes during approval. Dev builds should be network-disabled or use a verified dependency cache.

---

### M1 — YAML parsing remains a pre-schema denial-of-service surface

**Severity: Medium**

A 1 MiB byte cap alone does not stop deeply nested mappings, alias expansion, excessive nodes, duplicate-key confusion, or pathological scalars. The strict schema only operates after parsing succeeds.

---

### M2 — Broker size caps do not prevent directory and event floods

**Severity: Medium**

A compromised container can create millions of tiny request entries, continuously replace them, or generate valid ULIDs faster than `_state` polls. Directory traversal, sorting, diff generation, and attention-state churn can exhaust host CPU and memory. Cap directory entries, work per poll, pending requests per project, and request rate.

---

### M3 — Store verification has a verify/use race unless versions are immutable

**Severity: Medium, potentially High with unsafe replacement semantics**

The shim verifies `vibe` and then executes it by pathname; it verifies payload and later supplies its pathname to Docker. Concurrent update/GC can replace either between verification and use unless version directories are immutable and protected by locking. Hashing an opened FD and executing that FD where supported is stronger.

---

### M4 — Sidecar port and hardening policy is incomplete

**Severity: Medium**

Loopback-only enforcement is stated for `runtime.ports`, but the `services` description separately allows ports without saying the same rule applies. It is also unclear whether non-root, capability dropping, `no-new-privileges`, mount restrictions, and namespace restrictions apply to every sidecar.

No Low findings are material enough to list separately.

## 3. Security-model regressions and contradictions

The following places are weaker than the supplied v1 security baseline:

1. **Trusted-source/setup warning regression.** security.md says maliciously authored project configuration is not proven contained and the first-contact installer warns users to use disposable clones or a future jailed profile. v2's first-contact prompt shows only the harness pin and verification status. The command surface contains no jailed profile. This risks implying that an attested engine makes a malicious project safe.

2. **The extension Dockerfile contradicts "by construction."** v1 explicitly called Compose and Dockerfile input residual exposure. v2 carries the Dockerfile forward unchanged while claiming the root rule is now enforced structurally.

3. **Environment scrubbing disappeared.** v1 renders Compose under a scrubbed environment and structurally enforces the resulting model. v2 merely asserts that interpolation is disabled. That is not an equivalent guarantee.

4. **Snapshot materialization is underspecified.** v1 rejects symlinks, gitlinks, and special files and avoids checkout hooks. v2 says "snapshotted host-side" without preserving those rules.

5. **Publisher authentication is weaker by default.** v1 first contact promises publisher authentication. v2 defaults to `prefer`, meaning hosts without `gh` accept same-origin checksums only.

6. **Terminal exposure moved inside the trust decision.** v1 classified hostile terminal bytes as residual. The broker now uses hostile text and diffs as the basis for approval, making terminal-safe rendering part of the root of trust.

7. **Git-hook leakage remains relevant.** v2 defaults `git_hooks: false`, but `git_lfs: true` may still install a shared-workspace hook depending on bootstrap behavior. More importantly, any host git use during `dev sync` would execute against attacker-controlled `.git` state.

8. **`DOCKER_HOST` handling disappeared.** v1 explicitly identifies a remote Docker endpoint as equivalent to a socket and has doctor flag it. v2 does not preserve that check or define an allowed Docker endpoint.

## 4. Answers to the six open questions

### 1. Is TLS plus same-origin SHA256SUMS meaningful?

Only for corruption detection. It does not provide independent publisher authentication because an origin or TLS interception compromise replaces both artifact and checksum.

`require` should be the default. Verification should be implemented natively in the trusted Go binary with pinned Sigstore/GitHub identity policy. If verification cannot run, update/init must fail before unpacking or executing the candidate. CI should have no override. An interactive, one-command unsafe import can exist, but must show and record the exact digest and unsafe provenance.

### 2. Store locking: same as v1 or revisit?

Revisit it around immutable artifacts and atomic state transitions.

Use native advisory locks on host-owned files—`flock(2)` via Go on Linux/WSL2 and macOS—rather than shell `flock` or stale lock directories. Use:

- immutable digest-addressed version directories;
- staging directory plus fsync and atomic rename;
- per-project locks for trust records and snapshots;
- a defined lock order;
- shared/read locking or opened-FD verification during shim execution;
- no automatic deletion of a lock merely because a PID string looks stale.

### 3. Can the approval UI be weaponized?

Yes. Escape sequences, bidi characters, hostile filenames, fake UI chrome, clipboard control, and the approve/use TOCTOU all undermine the approval.

The UI must sanitize bytes and approve an immutable snapshot digest. No external pager or shell-rendered diff should handle hostile content.

### 4. Can one project be tricked into another project's dev build?

Yes, or at least the current design does not prove otherwise. `dev-<source-hash>` is global and does not bind project identity, builder digest, dependency resolution, target, or output digest. Record scoping is a policy assertion, not a storage invariant.

Use artifact-digest storage and bind dev records to the canonical project-record ID and complete build provenance. Never overwrite a dev artifact path and never allow a dev record to satisfy a release pin. This still does not solve the fundamental fact that dev mode executes workspace-derived host code.

### 5. YAML library and limits

Use `gopkg.in/yaml.v3`, parse into `yaml.Node`, inspect the node tree before decoding into the typed schema, and use `KnownFields(true)` for the final decode.

Recommended limits:

- 256 KiB input by default; 1 MiB absolute ceiling;
- exactly one document;
- maximum depth 32;
- maximum 10,000 nodes;
- maximum 1,000 entries in any mapping or sequence;
- maximum scalar size 64 KiB;
- reject anchors, aliases, merge keys, custom tags, non-string mapping keys, duplicate keys, NUL, invalid UTF-8, and disallowed control characters.

Rejecting aliases entirely is simpler and safer than attempting expansion accounting.

### 6. Can environment values reach host execution?

Yes, if either of two boundaries is implemented incorrectly:

1. Compose interpolates `$` in generated values using host or workspace environment.
2. Container environment values are merged into the host Docker client's `cmd.Env`.

The safe rule is:

- Environment values are opaque container data.
- They never enter a host subprocess environment.
- Docker/Compose/tmux/git/gh run under a fixed allowlisted host environment.
- Values are passed through typed Docker API fields or explicit argv entries.
- `$` is escaped or rejected before Compose sees it.
- `env_file` is safely snapshotted, parsed literally, and constrained inside the workspace.

## Verdict

The closed schema is a meaningful improvement over v1's evolving Compose source scanner: it removes `include`, `extends`, `provider`, nested-key smuggling, and most source-level feature growth if the renderer truly operates on a typed AST.

It does not yet establish the claimed root of trust. The broker TOCTOU, live bind-mount race, dev-mode host execution, workspace response writes, Compose interpolation phase, and default unauthenticated update path are release-blocking critical findings. The design should not advance to implementation until those boundaries—and the invariant itself—are made internally consistent.

package model

import (
	"strings"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
)

// Generated Docker object names derive from the project ID — never from
// display names or paths — so they are stable, collision-free, and safe
// to print.

const namePrefix = "vibe-"

// idTag shortens the project ID for object names; 12 base32 characters
// (60 bits) keeps accidental collision out of reach for a host fleet.
func idTag(id domain.ProjectID) string {
	s := string(id)
	if len(s) > 12 {
		s = s[:12]
	}
	return s
}

func DevContainerName(id domain.ProjectID) string {
	return namePrefix + idTag(id) + "-dev"
}

func SidecarContainerName(id domain.ProjectID, service string) string {
	return namePrefix + idTag(id) + "-svc-" + service
}

func NetworkName(id domain.ProjectID) string {
	return namePrefix + idTag(id)
}

func AgentStateVolumeName(id domain.ProjectID) string {
	return namePrefix + idTag(id) + "-agent-state"
}

func ServiceVolumeName(id domain.ProjectID, volume string) string {
	return namePrefix + idTag(id) + "-vol-" + volume
}

func ExtensionImageRef(id domain.ProjectID) string {
	return namePrefix + idTag(id) + "-ext"
}

func ToolsImageRef(id domain.ProjectID) string {
	return namePrefix + idTag(id) + "-tools"
}

// Fixed in-container targets for engine-generated mounts. Custom import
// targets may not equal, contain, or be contained by any of these.
const (
	WorkspaceTarget  = "/workspace"
	PayloadTarget    = "/vibe/payload"
	AgentStateTarget = "/vibe/agent-state"
	ResultsTarget    = "/vibe/results"
	// RuntimeDirTarget is the dev container's XDG runtime dir, backed by
	// a real tmpfs mount. The uid in the path is a contract with the dev
	// user (vscode, uid 1000 in the devcontainer base — compile pins the
	// user, the tmpfs mount options mint the ownership). tmpfs clears on
	// every container boot, so state-dir.sh records can't go stale across
	// a hard stop/start the way writable-layer /tmp lets them.
	RuntimeDirTarget = "/run/user/1000"
	// DNSConfTarget is where the dns ledger sidecar sees the
	// engine-authored CoreDNS config (a read-only bind of the artifact
	// payload's dns/ subtree). It never exists in the dev container, but
	// /vibe/* is engine-owned namespace, so imports stay off it.
	DNSConfTarget = "/vibe/dns"
)

// ReservedTargets lists every engine-owned mount target.
func ReservedTargets() []string {
	return []string{WorkspaceTarget, PayloadTarget, AgentStateTarget, ResultsTarget, RuntimeDirTarget, DNSConfTarget}
}

// reservedTargetFor returns the engine-owned target that t equals,
// contains, or is contained by, or "" when t is free.
func reservedTargetFor(t string) string {
	for _, r := range ReservedTargets() {
		if t == r || strings.HasPrefix(t, r+"/") || strings.HasPrefix(r, t+"/") {
			return r
		}
	}
	return ""
}

// PayloadEntrypoint is the dev container command when an artifact's
// payload is mounted.
const PayloadEntrypoint = PayloadTarget + "/container/entrypoint.sh"

// PayloadAgentSession is the container-side session carrier `vibe
// agent` wraps the agent CLI in when the payload is mounted and the
// image has tmux (docs/architecture.md (agent sessions)).
const PayloadAgentSession = PayloadTarget + "/container/agent-session.sh"

// PayloadAgentPS is the container-side feeder for `vibe ps` agent rows:
// it joins the inner tmux server with the agent-state records.
const PayloadAgentPS = PayloadTarget + "/container/agent-ps.sh"

// PayloadAgentWatch is the container-side sentinel the `vibe _watch`
// daemon streams: one line per inner-tmux/state-record change.
const PayloadAgentWatch = PayloadTarget + "/container/agent-watch.sh"

// PayloadLifecycle runs the project's post-create/post-start hooks
// (workspace files, workload trust) inside the container; the engine
// execs it after reconcile when the payload is mounted.
const PayloadLifecycle = PayloadTarget + "/container/lifecycle.sh"

// PayloadEgressSample is the container-side live-socket sampler for the
// egress view: pure /proc/net parsing with fd-readlink attribution
// (unprivileged by design — cap_drop ALL removes NET_RAW), TSV rows
// behind a version line.
const PayloadEgressSample = PayloadTarget + "/container/egress-sample.sh"

// DNSServiceName is the engine-generated DNS ledger sidecar's service
// slot: container vibe-<id12>-svc-dns, read via `vibe logs dns`. The
// schema mirrors it in reservedServiceNames (string duplicated to keep
// the dependency direction inward).
const DNSServiceName = "dns"

// CoreDNSImageRef is the digest-pinned forwarder image the engine
// synthesizes into every provisioned project's plan. The digest is the
// multi-arch manifest-list digest (amd64 and arm64 among others), so
// every release target resolves a platform image from the same pin.
const CoreDNSImageRef = "coredns/coredns:1.14.6@sha256:900f9c109f7a33545d3c811516e8376df9019147b750f5ce3e254468769176ea"

// PayloadDNSDir is the artifact payload subtree bind-mounted (read-
// only) at DNSConfTarget in the dns sidecar.
const PayloadDNSDir = "dns"

// DNSCorefile is the CoreDNS config path inside the dns sidecar.
const DNSCorefile = DNSConfTarget + "/Corefile"

// DNSConfRelPath is the Corefile's payload-relative path — the
// capability-probe key (DNSConfPresent): an artifact without this file
// predates the egress ledger (or staged without it) and compiles
// without the sidecar.
const DNSConfRelPath = PayloadDNSDir + "/Corefile"

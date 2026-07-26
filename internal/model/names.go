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
)

// ReservedTargets lists every engine-owned mount target.
func ReservedTargets() []string {
	return []string{WorkspaceTarget, PayloadTarget, AgentStateTarget, ResultsTarget}
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

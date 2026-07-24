package model

import "github.com/chrisdruta/vibe-tui-box/internal/domain"

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

// PayloadEntrypoint is the dev container command when an artifact's
// payload is mounted.
const PayloadEntrypoint = PayloadTarget + "/container/entrypoint.sh"

// PayloadAgentSession is the container-side session carrier `vibe
// agent` wraps the agent CLI in when the payload is mounted and the
// image has tmux (docs/agent-session-design.md).
const PayloadAgentSession = PayloadTarget + "/container/agent-session.sh"

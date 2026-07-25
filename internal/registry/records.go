// Package registry owns the persistent per-project records under
// ~/.vibe/state/projects and the cross-project fleet view. Records are
// versioned JSON with compare-and-swap revisions; unknown fields are
// rejected so data upgrades stay explicit.
package registry

import (
	"fmt"
	"time"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/paths"
)

// RecordFormat is the current persistent format. v2 starts at 1 and has
// no v1 import path.
const RecordFormat = 1

// Mode selects where the project's engine binary comes from.
type Mode string

const (
	ModeRelease Mode = "release"
	ModeDev     Mode = "dev"
)

// Record is one registered project.
type Record struct {
	Format         int                `json:"format"`
	Revision       uint64             `json:"revision"`
	ID             domain.ProjectID   `json:"id"`
	Root           string             `json:"root"`
	RootIdentity   paths.FileIdentity `json:"root_identity"`
	DisplayName    string             `json:"display_name"`
	Artifact       domain.Digest      `json:"artifact,omitempty"`
	ReleaseVersion string             `json:"release_version,omitempty"`
	Mode           Mode               `json:"mode"`
	Approved       *domain.Digest     `json:"approved_candidate,omitempty"`
	// AgentRefresh is the current agent-refresh generation: a token set by
	// `vibe rebuild --refresh-agents` and folded into the tools-image
	// build so the channel-tracking agents (claude, codex, grok) re-pull
	// to latest and then stay warm until the next refresh. Empty means the
	// project has never refreshed (agents install at their engine pins).
	AgentRefresh string    `json:"agent_refresh,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// NewRecord carries the caller-supplied fields for Create; the registry
// assigns ID, revision, format, and timestamps.
type NewRecord struct {
	Root           paths.Root
	DisplayName    string
	Artifact       domain.Digest
	ReleaseVersion string
	Mode           Mode
}

func (n NewRecord) validate() error {
	if n.Root.Path == "" {
		return fmt.Errorf("%w: project root is required", domain.ErrInvalid)
	}
	if n.DisplayName == "" {
		return fmt.Errorf("%w: display name is required", domain.ErrInvalid)
	}
	if len(n.DisplayName) > 64 {
		return fmt.Errorf("%w: display name exceeds 64 bytes", domain.ErrInvalid)
	}
	switch n.Mode {
	case ModeRelease, ModeDev:
	default:
		return fmt.Errorf("%w: mode %q", domain.ErrInvalid, n.Mode)
	}
	return nil
}

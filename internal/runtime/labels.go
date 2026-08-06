// Package runtime turns an immutable candidate plan into Docker state
// and back: reconcile, tear down, and report. It coordinates the store,
// locks, and the Docker API adapter; it never reads the workspace.
package runtime

import (
	"vibe/internal/domain"
)

// Engine-owned Docker labels. Every managed object carries them; the
// engine never touches objects without ManagedLabel.
const (
	ManagedLabel   = "dev.vibe.managed"
	ProjectLabel   = "dev.vibe.project"
	CandidateLabel = "dev.vibe.candidate"
	ArtifactLabel  = "dev.vibe.artifact"
	RoleLabel      = "dev.vibe.role"
	// TxnLabel carries the replacement-transaction nonce on every
	// container a transaction creates: content labels identify what a
	// container runs, the nonce identifies who made it, which is what
	// crash recovery must prove before it may delete anything.
	TxnLabel = "dev.vibe.txn"
	// DNSLabel records the resolver IP a container was created with
	// (the dns sidecar's runtime address — deliberately outside the
	// candidate digest). Reconcile compares it against the sidecar's
	// live address: drift escalates a plain start to a replace, because
	// resolv.conf is baked at create time.
	DNSLabel = "dev.vibe.dns"
)

// objectLabels is the label set for networks and volumes, which exist
// across candidates.
func objectLabels(project domain.ProjectID) map[string]string {
	return map[string]string{
		ManagedLabel: "true",
		ProjectLabel: string(project),
	}
}

// containerLabels merges the plan's per-container labels with the
// candidate and artifact pins.
func containerLabels(planLabels map[string]string, candidate, artifact domain.Digest) map[string]string {
	labels := make(map[string]string, len(planLabels)+2)
	for k, v := range planLabels {
		labels[k] = v
	}
	labels[CandidateLabel] = candidate.String()
	if !artifact.IsZero() {
		labels[ArtifactLabel] = artifact.String()
	}
	return labels
}

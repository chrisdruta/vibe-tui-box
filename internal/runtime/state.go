package runtime

import (
	"sort"

	"vibe/internal/dockerapi"
	"vibe/internal/domain"
)

// ContainerStatus is the reconciled view of one managed container.
type ContainerStatus struct {
	Name      string        `json:"name"`
	Role      string        `json:"role"`
	Running   bool          `json:"running"`
	Candidate domain.Digest `json:"candidate,omitzero"`
	InSync    bool          `json:"in_sync"` // candidate label matches the approved candidate
}

// State summarizes a project's runtime.
type State struct {
	Project    domain.ProjectID  `json:"project"`
	Approved   domain.Digest     `json:"approved_candidate,omitzero"`
	Containers []ContainerStatus `json:"containers"`
}

// Running reports whether every container in the state is running and
// in sync; false for an empty state.
func (s State) Running() bool {
	if len(s.Containers) == 0 {
		return false
	}
	for _, c := range s.Containers {
		if !c.Running || !c.InSync {
			return false
		}
	}
	return true
}

// summarize converts raw container listings into a deterministic state,
// sorted by container name.
func summarize(project domain.ProjectID, approved domain.Digest, containers []dockerapi.ContainerState) State {
	state := State{Project: project, Approved: approved}
	for _, c := range containers {
		status := ContainerStatus{
			Name:    string(c.Name),
			Role:    c.Labels[RoleLabel],
			Running: c.Running,
		}
		if d, err := domain.ParseDigest(c.Labels[CandidateLabel]); err == nil {
			status.Candidate = d
		}
		status.InSync = !approved.IsZero() && status.Candidate == approved
		state.Containers = append(state.Containers, status)
	}
	sort.Slice(state.Containers, func(i, j int) bool {
		return state.Containers[i].Name < state.Containers[j].Name
	})
	return state
}

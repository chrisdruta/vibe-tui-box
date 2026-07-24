package app

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/chrisdruta/vibe-tui-box/internal/dockerapi"
	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/model"
	"github.com/chrisdruta/vibe-tui-box/internal/paths"
	"github.com/chrisdruta/vibe-tui-box/internal/registry"
)

// RegisterRequest registers the project containing Dir. DisplayName
// defaults to the root directory's base name.
type RegisterRequest struct {
	Dir         string
	DisplayName string
}

type RegisterResult struct {
	Record registry.Record
}

func (a *App) Register(ctx context.Context, req RegisterRequest) (RegisterResult, error) {
	root, err := paths.Discover(req.Dir)
	if err != nil {
		return RegisterResult{}, &domain.OpError{Op: "register", Err: err}
	}
	name := req.DisplayName
	if name == "" {
		name = filepath.Base(root.Path)
	}
	rec, err := a.deps.Registry.Create(ctx, registry.NewRecord{
		Root:        root,
		DisplayName: name,
		Mode:        registry.ModeRelease,
	})
	if err != nil {
		return RegisterResult{}, &domain.OpError{Op: "register", Err: err}
	}
	a.bumpTuiSerial(ctx)
	return RegisterResult{Record: rec}, nil
}

// PSRequest lists the fleet; Dir scopes the agent-session rows to the
// project containing it (none when Dir is outside a running project).
type PSRequest struct {
	Dir string
}

// AgentPSRow is one agent session inside the current project's dev
// container, as reported by the container-side agent-ps.sh join of the
// inner tmux server and the state records.
type AgentPSRow struct {
	Name   string
	State  string
	Since  int64  // unix epoch of the state's reference time; 0 unknown
	Age    string // compact human age rendered against the engine clock
	Detail string
}

// PSResult lists registered projects in deterministic fleet order,
// plus the current project's agent sessions when its dev container is
// running.
type PSResult struct {
	Projects     []registry.Record
	AgentProject string // display name owning Agents; "" when none
	Agents       []AgentPSRow
}

func (a *App) PS(ctx context.Context, req PSRequest) (PSResult, error) {
	records, err := a.deps.Registry.List(ctx)
	if err != nil {
		return PSResult{}, &domain.OpError{Op: "ps", Err: err}
	}
	result := PSResult{Projects: records}
	// Agent rows are strictly best-effort decoration: no project here,
	// no running container, no payload — the fleet listing stands alone.
	if _, rec, err := a.resolveProject(ctx, req.Dir); err == nil {
		if name, err := a.requireDevContainer(ctx, rec); err == nil {
			var out bytes.Buffer
			res, err := a.deps.Docker.Exec(ctx, dockerapi.ExecRequest{
				Container: name,
				User:      devContainerUser,
				Argv:      []string{"bash", model.PayloadAgentPS},
				Streams:   dockerapi.Streams{Out: &out},
			})
			if err == nil && res.ExitCode == 0 {
				if rows := parseAgentPS(out.String(), a.deps.Clock.Now()); len(rows) > 0 {
					result.AgentProject = rec.DisplayName
					result.Agents = rows
				}
			}
		}
	}
	return result, nil
}

// parseAgentPS decodes agent-ps.sh's `name|state|epoch|detail` rows.
// The bytes are container-controlled: fields are stripped to printable
// ASCII and bounded before they can reach a terminal or JSON output.
func parseAgentPS(out string, now time.Time) []AgentPSRow {
	var rows []AgentPSRow
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(strings.TrimRight(line, "\r"), "|")
		if len(parts) != 4 || parts[0] == "" {
			continue
		}
		row := AgentPSRow{
			Name:   sanitizeField(parts[0]),
			State:  sanitizeField(parts[1]),
			Detail: sanitizeField(parts[3]),
		}
		if ts, err := strconv.ParseInt(parts[2], 10, 64); err == nil && ts > 0 {
			row.Since = ts
			row.Age = compactAge(now.Unix() - ts)
		}
		rows = append(rows, row)
	}
	return rows
}

func sanitizeField(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0x20 && r < 0x7f {
			b.WriteRune(r)
		}
		if b.Len() >= 64 {
			break
		}
	}
	return b.String()
}

func compactAge(s int64) string {
	if s < 0 {
		s = 0
	}
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm", s/60)
	case s < 86400:
		return fmt.Sprintf("%dh", s/3600)
	default:
		return fmt.Sprintf("%dd", s/86400)
	}
}

// ForgetRequest removes the registration of the project containing Dir.
// It never touches the workspace itself.
type ForgetRequest struct {
	Dir string
}

type ForgetResult struct {
	Record registry.Record
}

func (a *App) Forget(ctx context.Context, req ForgetRequest) (ForgetResult, error) {
	root, err := paths.Discover(req.Dir)
	if err != nil {
		return ForgetResult{}, &domain.OpError{Op: "forget", Err: err}
	}
	rec, err := a.deps.Registry.Resolve(ctx, root)
	if err != nil {
		return ForgetResult{}, &domain.OpError{Op: "forget", Err: err}
	}
	if err := a.deps.Registry.Delete(ctx, rec.ID, rec.Revision); err != nil {
		return ForgetResult{}, &domain.OpError{Op: "forget", Project: rec.ID, Err: err}
	}
	a.bumpTuiSerial(ctx)
	return ForgetResult{Record: rec}, nil
}

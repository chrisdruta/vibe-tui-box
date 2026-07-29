// Package builder orchestrates extension image builds. Its Dockerfile
// parsing has one deliberately small purpose: enforce the supported
// frontend and final-user contract and reject unsupported directives.
// It is not a general Dockerfile interpreter — the build itself always
// uses the frozen candidate context.
package builder

import (
	"bufio"
	"context"
	"fmt"
	"strings"

	"github.com/chrisdruta/vibe-tui-box/internal/dockerapi"
	"github.com/chrisdruta/vibe-tui-box/internal/domain"
)

// BaseImageArg is the only build arg the engine supplies; extension
// Dockerfiles must start FROM it.
const BaseImageArg = "VIBE_BASE_IMAGE"

// Candidate is one frozen, validated extension build input.
type Candidate struct {
	Digest     domain.Digest // build-candidate content digest
	ContextDir string        // frozen context on the host
	Dockerfile string        // path within the context
	BaseImage  dockerapi.ResolvedImage
	Tag        string
	// RefreshArgs are the per-agent cache-buster build args
	// (AgentRefreshArgFor(kind) → the channel's resolved version, or a
	// rebuild timestamp) that bust the channel-tracking agent layers'
	// cache. Tools builds only; empty for extension builds.
	RefreshArgs map[string]string
}

// ValidateDockerfile enforces the closed contract:
//
//   - no BuildKit frontend selection (# syntax directives);
//   - exactly one FROM, and it must be ${VIBE_BASE_IMAGE};
//   - no ADD, ONBUILD, COPY --from, or additional build stages; and
//   - any final USER instruction must return to vscode.
func ValidateDockerfile(content []byte) error {
	fromCount := 0
	lastUser := ""
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	scanner.Buffer(nil, 1<<20)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			directive := strings.ToLower(strings.ReplaceAll(line, " ", ""))
			if strings.HasPrefix(directive, "#syntax=") {
				return fmt.Errorf("%w: line %d: custom BuildKit frontends (# syntax) are not allowed", domain.ErrInvalid, lineNo)
			}
			continue
		}
		fields := strings.Fields(line)
		instr := strings.ToUpper(fields[0])
		switch instr {
		case "FROM":
			fromCount++
			if fromCount > 1 {
				return fmt.Errorf("%w: line %d: multi-stage builds are not allowed", domain.ErrInvalid, lineNo)
			}
			if len(fields) < 2 || (fields[1] != "${"+BaseImageArg+"}" && fields[1] != "$"+BaseImageArg) {
				return fmt.Errorf("%w: line %d: extension images must start FROM ${%s}", domain.ErrInvalid, lineNo, BaseImageArg)
			}
		case "ADD":
			return fmt.Errorf("%w: line %d: ADD is not allowed (use COPY)", domain.ErrInvalid, lineNo)
		case "COPY":
			// COPY --from pulls content from an arbitrary unpinned image —
			// the same hole as ADD's remote URLs, and outside the frozen
			// context + pinned base everything else is confined to.
			for _, f := range fields[1:] {
				if strings.HasPrefix(strings.ToLower(f), "--from") {
					return fmt.Errorf("%w: line %d: COPY --from is not allowed (all content comes from the pinned base or the frozen context)", domain.ErrInvalid, lineNo)
				}
			}
		case "ONBUILD":
			return fmt.Errorf("%w: line %d: ONBUILD is not allowed", domain.ErrInvalid, lineNo)
		case "USER":
			if len(fields) < 2 {
				return fmt.Errorf("%w: line %d: USER needs a name", domain.ErrInvalid, lineNo)
			}
			lastUser = fields[1]
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("%w: dockerfile: %v", domain.ErrInvalid, err)
	}
	if fromCount == 0 {
		return fmt.Errorf("%w: dockerfile has no FROM instruction", domain.ErrInvalid)
	}
	if lastUser != "" && lastUser != "vscode" {
		return fmt.Errorf("%w: extension images must end as user vscode, not %q", domain.ErrInvalid, lastUser)
	}
	// The Dockerfile must declare the arg before FROM can expand it; the
	// engine injects the value, never the project.
	if !strings.Contains(string(content), "ARG "+BaseImageArg) {
		return fmt.Errorf("%w: dockerfile must declare `ARG %s` before FROM", domain.ErrInvalid, BaseImageArg)
	}
	return nil
}

// Service runs approved extension builds.
type Service struct {
	docker dockerapi.Client
}

func NewService(docker dockerapi.Client) (*Service, error) {
	if docker == nil {
		return nil, fmt.Errorf("%w: builder requires a docker client", domain.ErrInvalid)
	}
	return &Service{docker: docker}, nil
}

// Build submits the frozen candidate. The base image is passed only as
// the pinned digest reference.
func (s *Service) Build(ctx context.Context, cand Candidate, sink dockerapi.ProgressSink) (dockerapi.BuiltImage, error) {
	base := string(cand.BaseImage.Ref)
	if !cand.BaseImage.Digest.IsZero() {
		if name, _, found := strings.Cut(base, "@"); found {
			base = name
		}
		base += "@" + cand.BaseImage.Digest.String()
	}
	buildArgs := map[string]string{BaseImageArg: base}
	for arg, value := range cand.RefreshArgs {
		if value != "" {
			buildArgs[arg] = value
		}
	}
	return s.docker.Build(ctx, dockerapi.BuildRequest{
		Tag:        cand.Tag,
		ContextDir: cand.ContextDir,
		Dockerfile: cand.Dockerfile,
		BuildArgs:  buildArgs,
	}, sink)
}

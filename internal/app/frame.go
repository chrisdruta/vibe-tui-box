package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/tmuxui"
)

// FrameRequest drives the hidden `vibe _frame` sidebar renderer. Input
// carries the tmux porcelain the sidebar loop gathered (the engine
// never talks to the UI server's tmux itself — the shell owns those
// mechanics); CacheDir is the engine-truth cache beside the tmux
// socket, "" when the cache layer is unavailable.
type FrameRequest struct {
	Input    io.Reader
	CacheDir string
}

// FrameResult is the rendered frame: the click map for
// @vibe_sidebar_map, the tray's ghost cells for @vibe_ghosts, the
// ghost map (session names in range order) for @vibe_ghost_map, and
// the newline-free ANSI body.
type FrameResult struct {
	Map      string
	Ghosts   string
	GhostMap string
	Body     string
}

// maxFrameInput bounds the porcelain read; a sidebar frame's tmux
// listing is a few KB, so anything beyond this is a runaway feeder.
const maxFrameInput = 1 << 20

// RenderFrame renders one sidebar frame from gathered tmux porcelain
// plus the cache-only engine truth. It never touches Docker: frames
// stay cheap by contract, and engine facts are only as fresh as the
// background fetch that last filled the cache.
func (a *App) RenderFrame(ctx context.Context, req FrameRequest) (FrameResult, error) {
	fail := opFail[FrameResult]("_frame", "")
	if req.Input == nil {
		return fail(fmt.Errorf("%w: frame input is required", domain.ErrInvalid))
	}
	data, err := io.ReadAll(io.LimitReader(req.Input, maxFrameInput))
	if err != nil {
		return fail(err)
	}
	in := tmuxui.ParseFrameData(string(data))
	in.Now = a.deps.Clock.Now().Unix()
	for i := range in.Sessions {
		in.Sessions[i].Branch = gitBranch(in.Sessions[i].Path)
	}
	if req.CacheDir != "" {
		if fleet, err := os.ReadFile(filepath.Join(req.CacheDir, "fleet")); err == nil {
			in.Fleet = tmuxui.ParseFleet(strings.Split(string(fleet), "\n"))
		}
		if agents, err := os.ReadFile(filepath.Join(req.CacheDir, "agents")); err == nil {
			in.Agents = tmuxui.ParseAgents(strings.Split(string(agents), "\n"))
		}
		for _, s := range in.Sessions {
			if s.ID != in.SelfSession || s.Project == "" {
				continue
			}
			if detail, err := os.ReadFile(filepath.Join(req.CacheDir, "detail."+s.Project)); err == nil {
				in.Detail = strings.Split(strings.TrimRight(string(detail), "\n"), "\n")
			}
			break
		}
	}
	out := tmuxui.Frame(in)
	return FrameResult{Map: out.Map, Ghosts: out.Ghosts, GhostMap: out.GhostMap, Body: out.Body}, nil
}

// gitBranch reads .git/HEAD directly — no git subprocess on the frame
// path. It follows the .git-file indirection (worktrees, submodule
// checkouts); a detached HEAD shows the short sha; anything unreadable
// is simply no branch.
func gitBranch(dir string) string {
	if dir == "" {
		return ""
	}
	g := filepath.Join(dir, ".git")
	if info, err := os.Lstat(g); err != nil {
		return ""
	} else if info.Mode().IsRegular() {
		data, err := os.ReadFile(g)
		if err != nil {
			return ""
		}
		line, _, _ := strings.Cut(string(data), "\n")
		gd, ok := strings.CutPrefix(line, "gitdir: ")
		if !ok || gd == "" {
			return ""
		}
		if !filepath.IsAbs(gd) {
			gd = filepath.Join(dir, gd)
		}
		g = gd
	}
	data, err := os.ReadFile(filepath.Join(g, "HEAD"))
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(string(data), "\n")
	if ref, ok := strings.CutPrefix(line, "ref: refs/heads/"); ok {
		return ref
	}
	if len(line) > 7 {
		return line[:7] // detached: the short sha is the label
	}
	return line
}

var churnRe = regexp.MustCompile(`(\d+) insertion|(\d+) deletion`)

// gitChurn renders the working-tree churn for the branch line
// (`+128 −40`, docs/tui-layout.md "Signal density"). Unlike gitBranch
// this DOES run a git subprocess — one `git diff --shortstat HEAD`
// (staged and unstaged both count: the question is "has the agent
// changed anything", not "what isn't staged yet") — so it belongs to
// the fetch-path renderers only, never the frame path. A clean tree,
// a non-repo, or any git failure is simply no churn.
func gitChurn(ctx context.Context, dir string) string {
	if dir == "" {
		return ""
	}
	if _, err := os.Lstat(filepath.Join(dir, ".git")); err != nil {
		return ""
	}
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "diff", "--shortstat", "HEAD").Output()
	if err != nil || len(bytes.TrimSpace(out)) == 0 {
		return ""
	}
	ins, del := 0, 0
	for _, m := range churnRe.FindAllStringSubmatch(string(out), -1) {
		if m[1] != "" {
			ins, _ = strconv.Atoi(m[1])
		}
		if m[2] != "" {
			del, _ = strconv.Atoi(m[2])
		}
	}
	return fmt.Sprintf("+%d −%d", ins, del)
}

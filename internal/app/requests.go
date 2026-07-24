package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/chrisdruta/vibe-tui-box/internal/broker"
	"github.com/chrisdruta/vibe-tui-box/internal/builder"
	"github.com/chrisdruta/vibe-tui-box/internal/dockerapi"
	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/model"
	"github.com/chrisdruta/vibe-tui-box/internal/registry"
	"github.com/chrisdruta/vibe-tui-box/internal/runtime"
	"github.com/chrisdruta/vibe-tui-box/internal/snapshot"
	"github.com/chrisdruta/vibe-tui-box/internal/store"
	"github.com/chrisdruta/vibe-tui-box/internal/terminal"
)

func (a *App) brokerStore(id domain.ProjectID) (*broker.Store, error) {
	return broker.NewStore(a.deps.Layout.Broker, id)
}

// RequestListRequest polls the workspace for agent rebuild requests,
// binds each new one to an immutable candidate, and lists everything
// pending.
type RequestListRequest struct {
	Dir string
}

type RequestListResult struct {
	Pending  []broker.Pending
	Problems []string // malformed request files, reported not fatal
}

func (a *App) RequestList(ctx context.Context, req RequestListRequest) (RequestListResult, error) {
	root, rec, err := a.resolveProject(ctx, req.Dir)
	if err != nil {
		return RequestListResult{}, &domain.OpError{Op: "request list", Err: err}
	}
	bs, err := a.brokerStore(rec.ID)
	if err != nil {
		return RequestListResult{}, &domain.OpError{Op: "request list", Project: rec.ID, Err: err}
	}

	raws, problems := broker.ReadRequests(root)
	var result RequestListResult
	for _, p := range problems {
		result.Problems = append(result.Problems, p.Error())
	}

	pending, err := bs.ListPending()
	if err != nil {
		return RequestListResult{}, &domain.OpError{Op: "request list", Project: rec.ID, Err: err}
	}
	known := map[domain.RequestID]broker.Pending{}
	for _, p := range pending {
		known[p.RequestID] = p
	}
	results, err := bs.ListResults()
	if err != nil {
		return RequestListResult{}, &domain.OpError{Op: "request list", Project: rec.ID, Err: err}
	}
	completed := map[domain.RequestID]bool{}
	for _, r := range results {
		completed[r.RequestID] = true
	}

	for _, raw := range raws {
		id := raw.Request.ID
		if prev, ok := known[id]; ok && prev.Content == raw.Digest {
			continue // already bound to a candidate
		}
		if completed[id] {
			if _, ok := known[id]; !ok {
				continue // decided; agent must use a new request ID
			}
		}
		// New or replaced content: bind to a fresh immutable candidate.
		cand, err := a.prepareCandidate(ctx, root, rec)
		if err != nil {
			result.Problems = append(result.Problems, fmt.Sprintf("%s: candidate: %v", id, err))
			continue
		}
		p := broker.Pending{
			RequestID: id,
			ProjectID: rec.ID,
			Candidate: cand.Record.Digest,
			Content:   raw.Digest,
			Reason:    raw.Request.Reason,
			Summary:   raw.Request.Summary,
			CreatedAt: a.deps.Clock.Now().UTC(),
		}
		if err := bs.WritePending(p); err != nil {
			return RequestListResult{}, &domain.OpError{Op: "request list", Project: rec.ID, Err: err}
		}
		known[id] = p
	}

	final, err := bs.ListPending()
	if err != nil {
		return RequestListResult{}, &domain.OpError{Op: "request list", Project: rec.ID, Err: err}
	}
	result.Pending = final
	return result, nil
}

// RequestShowRequest renders one pending request with sanitized text.
type RequestShowRequest struct {
	Dir string
	ID  domain.RequestID
}

type RequestShowResult struct {
	Pending   broker.Pending
	Reason    terminal.Encoded
	Summary   terminal.Encoded
	DiffLabel string           // trusted heading for Diff
	Diff      terminal.Encoded // bounded plan diff, approved → candidate
}

func (a *App) RequestShow(ctx context.Context, req RequestShowRequest) (RequestShowResult, error) {
	_, rec, err := a.resolveProject(ctx, req.Dir)
	if err != nil {
		return RequestShowResult{}, &domain.OpError{Op: "request show", Err: err}
	}
	bs, err := a.brokerStore(rec.ID)
	if err != nil {
		return RequestShowResult{}, &domain.OpError{Op: "request show", Project: rec.ID, Err: err}
	}
	pending, err := bs.ListPending()
	if err != nil {
		return RequestShowResult{}, &domain.OpError{Op: "request show", Project: rec.ID, Err: err}
	}
	for _, p := range pending {
		if p.RequestID == req.ID {
			label, diff := a.planDiff(ctx, rec, p.Candidate, terminal.DiffLimits{})
			return RequestShowResult{
				Pending:   p,
				Reason:    terminal.Encode(p.Reason, terminal.Limits{}),
				Summary:   terminal.Encode(p.Summary, terminal.Limits{MaxLines: 4}),
				DiffLabel: label,
				Diff:      diff,
			}, nil
		}
	}
	return RequestShowResult{}, &domain.OpError{Op: "request show", Project: rec.ID,
		Err: fmt.Errorf("%w: pending request %s", domain.ErrNotFound, req.ID)}
}

// planDiff renders the bounded plan diff between the project's approved
// candidate and a pending one — the trusted half of every approval:
// what will actually change, computed from immutable candidates, beside
// the agent's own untrusted summary. Failures degrade to a note rather
// than blocking the decision surface.
func (a *App) planDiff(ctx context.Context, rec registry.Record, pending domain.Digest, limits terminal.DiffLimits) (string, terminal.Encoded) {
	next, err := a.candidatePlanJSON(ctx, pending)
	if err != nil {
		return "plan diff:", terminal.Encoded{Lines: []string{fmt.Sprintf("(candidate plan unavailable: %v)", err)}}
	}
	if rec.Approved == nil {
		return "plan (no approved candidate yet — everything below is new):",
			terminal.Diff("", next, limits)
	}
	if *rec.Approved == pending {
		return "plan diff:", terminal.Encoded{Lines: []string{"(candidate is already the approved one)"}}
	}
	prev, err := a.candidatePlanJSON(ctx, *rec.Approved)
	if err != nil {
		return "plan diff:", terminal.Encoded{Lines: []string{fmt.Sprintf("(approved plan unavailable: %v)", err)}}
	}
	label := fmt.Sprintf("plan diff (approved %s → this candidate):", shortDigest(*rec.Approved))
	diff := terminal.Diff(prev, next, limits)
	if len(diff.Lines) == 0 {
		diff = terminal.Encoded{Lines: []string{"(no plan changes)"}}
	}
	return label, diff
}

// candidatePlanJSON reads a leased candidate's canonical plan, verified
// against its record digest.
func (a *App) candidatePlanJSON(ctx context.Context, digest domain.Digest) (string, error) {
	rec, err := a.deps.Store.ReadCandidateRecord(digest)
	if err != nil {
		return "", err
	}
	lease, err := a.deps.Store.Open(ctx, store.CandidateObject, digest)
	if err != nil {
		return "", err
	}
	defer lease.Close()
	data, err := os.ReadFile(filepath.Join(lease.Object.Path, runtime.PlanFileName))
	if err != nil {
		return "", err
	}
	if got := domain.SHA256(data); got != rec.Plan {
		return "", fmt.Errorf("%w: candidate %s plan digest mismatch", domain.ErrConflict, digest)
	}
	return string(data), nil
}

func shortDigest(d domain.Digest) string {
	hex := d.Hex()
	if len(hex) > 12 {
		hex = hex[:12]
	}
	return "sha256:" + hex
}

// RequestDecideRequest approves or rejects a pending request. Approval
// addresses the immutable candidate digest, never a workspace filename.
type RequestDecideRequest struct {
	Dir       string
	Candidate domain.Digest
	Approve   bool
	Message   string
	Yes       bool // skip the interactive confirmation
}

type RequestDecideResult struct {
	Result broker.Result
	State  *runtime.State
}

func (a *App) RequestDecide(ctx context.Context, req RequestDecideRequest) (RequestDecideResult, error) {
	op := "request reject"
	if req.Approve {
		op = "request approve"
	}
	_, rec, err := a.resolveProject(ctx, req.Dir)
	if err != nil {
		return RequestDecideResult{}, &domain.OpError{Op: op, Err: err}
	}
	bs, err := a.brokerStore(rec.ID)
	if err != nil {
		return RequestDecideResult{}, &domain.OpError{Op: op, Project: rec.ID, Err: err}
	}
	pending, err := bs.ListPending()
	if err != nil {
		return RequestDecideResult{}, &domain.OpError{Op: op, Project: rec.ID, Err: err}
	}
	var match *broker.Pending
	for i := range pending {
		if pending[i].Candidate == req.Candidate {
			match = &pending[i]
			break
		}
	}
	if match == nil {
		return RequestDecideResult{}, &domain.OpError{Op: op, Project: rec.ID,
			Err: fmt.Errorf("%w: no pending request for candidate %s", domain.ErrNotFound, req.Candidate)}
	}

	now := a.deps.Clock.Now().UTC()
	result := broker.Result{
		RequestID: match.RequestID,
		ProjectID: rec.ID,
		Candidate: &match.Candidate,
		CreatedAt: match.CreatedAt,
	}

	if !req.Approve {
		result.Status = broker.StatusRejected
		result.Message = req.Message
		result.CompletedAt = &now
		if err := bs.WriteResult(result); err != nil {
			return RequestDecideResult{}, &domain.OpError{Op: op, Project: rec.ID, Err: err}
		}
		if err := bs.DeletePending(match.RequestID); err != nil {
			return RequestDecideResult{}, &domain.OpError{Op: op, Project: rec.ID, Err: err}
		}
		return RequestDecideResult{Result: result}, nil
	}

	if !req.Yes && a.deps.Prompt != nil {
		diffLabel, diff := a.planDiff(ctx, rec, match.Candidate, terminal.DiffLimits{MaxLines: 40})
		ok, err := a.deps.Prompt.Confirm(ctx, terminal.Confirmation{
			Title:     fmt.Sprintf("Apply rebuild request %s?", match.RequestID),
			Digest:    match.Candidate,
			Chrome:    []string{"summary (agent-authored):"},
			Content:   terminal.Encode(match.Summary, terminal.Limits{MaxLines: 8}),
			DiffLabel: diffLabel,
			Diff:      diff,
		})
		if err != nil {
			return RequestDecideResult{}, &domain.OpError{Op: op, Project: rec.ID, Err: err}
		}
		if !ok {
			return RequestDecideResult{}, &domain.OpError{Op: op, Project: rec.ID,
				Err: fmt.Errorf("%w: not confirmed", domain.ErrCanceled)}
		}
	}

	cand, lease, err := a.runtime.LoadCandidate(ctx, match.Candidate)
	if err != nil {
		return RequestDecideResult{}, &domain.OpError{Op: op, Project: rec.ID, Err: err}
	}
	defer lease.Close()
	state, err := a.runtime.Up(ctx, cand, runtime.UpOptions{
		Progress:     a.deps.Progress,
		LifecycleOut: a.deps.LifecycleOut,
	})
	if err != nil {
		return RequestDecideResult{}, &domain.OpError{Op: op, Project: rec.ID, Err: err}
	}
	if _, err := a.deps.Registry.Update(ctx, rec.ID, rec.Revision, func(r *registry.Record) error {
		r.Approved = &match.Candidate
		return nil
	}); err != nil {
		return RequestDecideResult{}, &domain.OpError{Op: op, Project: rec.ID, Err: err}
	}

	result.Status = broker.StatusApproved
	result.CompletedAt = &now
	if err := bs.WriteResult(result); err != nil {
		return RequestDecideResult{}, &domain.OpError{Op: op, Project: rec.ID, Err: err}
	}
	if err := bs.DeletePending(match.RequestID); err != nil {
		return RequestDecideResult{}, &domain.OpError{Op: op, Project: rec.ID, Err: err}
	}
	return RequestDecideResult{Result: result, State: &state}, nil
}

// --- extension builds -------------------------------------------------

// buildExtension validates the frozen Dockerfile, requires per-digest
// operator approval, and builds the extension image from a context that
// contains only the Dockerfile and the build directory — never the env
// file or manifest.
func (a *App) buildExtension(ctx context.Context, rec registry.Record, frozen frozenInputs, digests map[string]domain.Digest) (dockerapi.BuiltImage, error) {
	dockerfile, err := os.ReadFile(filepath.Join(frozen.Snapshot.Path, model.SnapshotDockerfilePath))
	if err != nil {
		return dockerapi.BuiltImage{}, fmt.Errorf("%w: extension enabled but .vibe/Dockerfile is missing", domain.ErrInvalid)
	}
	if err := builder.ValidateDockerfile(dockerfile); err != nil {
		return dockerapi.BuiltImage{}, err
	}
	extDigest := extensionDigest(frozen.Snapshot.Files)

	if !a.extensionApproved(rec.ID, extDigest) {
		if a.deps.Prompt == nil {
			return dockerapi.BuiltImage{}, fmt.Errorf("%w: extension build %s is not approved", domain.ErrConflict, extDigest)
		}
		ok, err := a.deps.Prompt.Confirm(ctx, terminal.Confirmation{
			Title:   "Approve extension image build? This expands the trusted workload sent to Docker.",
			Digest:  extDigest,
			Chrome:  []string{".vibe/Dockerfile (frozen):"},
			Content: terminal.Encode(string(dockerfile), terminal.Limits{MaxLines: 60}),
		})
		if err != nil {
			return dockerapi.BuiltImage{}, err
		}
		if !ok {
			return dockerapi.BuiltImage{}, fmt.Errorf("%w: extension build not approved", domain.ErrCanceled)
		}
		if err := a.markExtensionApproved(rec.ID, extDigest); err != nil {
			return dockerapi.BuiltImage{}, err
		}
	}

	// Restricted build context.
	contextDir, err := a.deps.Store.NewStaging("build-context")
	if err != nil {
		return dockerapi.BuiltImage{}, err
	}
	defer os.RemoveAll(contextDir)
	if err := copyTree(frozen.Snapshot.Path, contextDir, []string{model.SnapshotDockerfilePath, model.SnapshotBuildDir}); err != nil {
		return dockerapi.BuiltImage{}, err
	}

	// With engine-installed tools in play the extension layers on top of
	// the tools image, not the raw base.
	base := frozen.Manifest.Image.Base
	if hasTools(frozen.Manifest) {
		base = model.ToolsImageRef(rec.ID)
	}
	return a.builder.Build(ctx, builder.Candidate{
		Digest:     extDigest,
		ContextDir: contextDir,
		Dockerfile: model.SnapshotDockerfilePath,
		BaseImage:  dockerapi.ResolvedImage{Ref: dockerapi.ImageRef(base), Digest: digests[base]},
		Tag:        model.ExtensionImageRef(rec.ID),
	}, a.deps.Progress)
}

// extensionDigest derives the approval digest from the frozen build
// inputs' snapshot entries.
func extensionDigest(files []snapshot.File) domain.Digest {
	var buf bytes.Buffer
	for _, f := range files {
		if f.Path == model.SnapshotDockerfilePath || strings.HasPrefix(f.Path, model.SnapshotBuildDir+"/") {
			buf.WriteString(f.Path)
			buf.WriteByte(0)
			buf.WriteString(strconv.FormatInt(f.Size, 10))
			buf.WriteByte(0)
			buf.WriteString(f.Digest.String())
			buf.WriteByte('\n')
		}
	}
	return domain.SHA256(buf.Bytes())
}

func (a *App) approvalPath(id domain.ProjectID, digest domain.Digest) string {
	return filepath.Join(a.deps.Layout.State, "approvals", string(id)+"-"+digest.Hex())
}

func (a *App) extensionApproved(id domain.ProjectID, digest domain.Digest) bool {
	_, err := os.Lstat(a.approvalPath(id, digest))
	return err == nil
}

func (a *App) markExtensionApproved(id domain.ProjectID, digest domain.Digest) error {
	path := a.approvalPath(id, digest)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(digest.String()+"\n"), 0o600)
}

// copyTree copies selected slash-relative entries (files or trees) from
// src to dst.
func copyTree(src, dst string, entries []string) error {
	for _, entry := range entries {
		from := filepath.Join(src, filepath.FromSlash(entry))
		info, err := os.Lstat(from)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.IsDir() {
			err = filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				rel, _ := filepath.Rel(src, p)
				target := filepath.Join(dst, rel)
				if d.IsDir() {
					return os.MkdirAll(target, 0o755)
				}
				return copyFile(p, target)
			})
			if err != nil {
				return err
			}
			continue
		}
		if err := copyFile(from, filepath.Join(dst, filepath.FromSlash(entry))); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	info, err := in.Stat()
	if err != nil {
		return err
	}
	perm := os.FileMode(0o644)
	if info.Mode()&0o100 != 0 {
		perm = 0o755
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

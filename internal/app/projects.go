package app

import (
	"context"
	"path/filepath"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
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
	return RegisterResult{Record: rec}, nil
}

// PSResult lists registered projects in deterministic fleet order.
type PSResult struct {
	Projects []registry.Record
}

func (a *App) PS(ctx context.Context) (PSResult, error) {
	records, err := a.deps.Registry.List(ctx)
	if err != nil {
		return PSResult{}, &domain.OpError{Op: "ps", Err: err}
	}
	return PSResult{Projects: records}, nil
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
	return ForgetResult{Record: rec}, nil
}

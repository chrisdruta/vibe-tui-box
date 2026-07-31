// Package initproject renders a payload preset into a new project's
// .vibe directory. Rendering is typed template substitution into a
// fixed output set; presets cannot choose paths outside .vibe/.
package initproject

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/payload"
)

// TemplateData is everything a preset template may reference. Unknown
// fields in a template are render errors.
type TemplateData struct {
	ProjectName string
	// AutoMemory is the manifest agent.memory value ("auto" or "off");
	// always set, because an empty substitution renders invalid YAML.
	AutoMemory string
}

// Render produces the .vibe file set from a preset: each preset file
// lands at its own path with a trailing ".tmpl" stripped and its
// contents executed as a template.
func Render(preset payload.Preset, data TemplateData) (map[string][]byte, error) {
	out := make(map[string][]byte, len(preset.Files))
	for name, content := range preset.Files {
		target := strings.TrimSuffix(name, ".tmpl")
		if !fs.ValidPath(target) || target != filepath.ToSlash(filepath.Clean(target)) {
			return nil, fmt.Errorf("%w: preset path %q", domain.ErrInvalid, name)
		}
		if strings.HasSuffix(name, ".tmpl") {
			tmpl, err := template.New(name).Option("missingkey=error").Parse(string(content))
			if err != nil {
				return nil, fmt.Errorf("%w: preset template %s: %v", domain.ErrInvalid, name, err)
			}
			var buf bytes.Buffer
			if err := tmpl.Execute(&buf, data); err != nil {
				return nil, fmt.Errorf("%w: preset template %s: %v", domain.ErrInvalid, name, err)
			}
			out[target] = buf.Bytes()
		} else {
			out[target] = content
		}
	}
	if _, ok := out["vibe.yaml"]; !ok {
		return nil, fmt.Errorf("%w: preset %q renders no vibe.yaml", domain.ErrInvalid, preset.Name)
	}
	return out, nil
}

// Materialize atomically creates <root>/.vibe from rendered files: the
// tree is staged beside it and renamed into place, and an existing
// .vibe is never overwritten.
func Materialize(root string, files map[string][]byte) ([]string, error) {
	vibeDir := filepath.Join(root, ".vibe")
	if _, err := os.Lstat(vibeDir); err == nil {
		return nil, fmt.Errorf("%w: %s already exists", domain.ErrConflict, vibeDir)
	}
	staging, err := os.MkdirTemp(root, ".vibe-init-*")
	if err != nil {
		return nil, fmt.Errorf("stage init: %w", err)
	}
	defer os.RemoveAll(staging)

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	created := make([]string, 0, len(names))
	for _, name := range names {
		target := filepath.Join(staging, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(target, files[name], 0o644); err != nil {
			return nil, err
		}
		created = append(created, filepath.Join(".vibe", filepath.FromSlash(name)))
	}
	if err := os.Rename(staging, vibeDir); err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("%w: %s already exists", domain.ErrConflict, vibeDir)
		}
		return nil, fmt.Errorf("create .vibe: %w", err)
	}
	return created, nil
}

// The root briefing pair. The seeded .vibe/AGENTS.md reaches no agent
// by itself: Claude Code auto-reads only CLAUDE.md (an AGENTS.md needs
// the @AGENTS.md import shim), and codex reads root AGENTS.md prose
// but has no import syntax — so the pointer is prose for codex and an
// @-import for Claude, resolved through the shim. Engine-owned
// constants, never preset-authored: presets stay confined to .vibe/.
const (
	rootAgentsFile = "AGENTS.md"
	rootClaudeFile = "CLAUDE.md"
	rootAgentsSeed = "Read [.vibe/AGENTS.md](.vibe/AGENTS.md) before working in this\nrepository from inside its vibe container — it is the container\nbriefing: write scopes, the read-only /vibe/* mounts, and the rebuild\nrequest protocol.\n\n@.vibe/AGENTS.md\n"
	rootClaudeSeed = "@AGENTS.md\n"
)

// SeedRootBriefing creates the root AGENTS.md pointer and the
// CLAUDE.md → @AGENTS.md shim beside a freshly materialized .vibe.
// Existing root files are never touched (O_EXCL): each survivor comes
// back as skipped so the caller can tell the operator to wire the
// pointer themselves.
func SeedRootBriefing(root string) (created, skipped []string, _ error) {
	for _, f := range []struct{ name, content string }{
		{rootAgentsFile, rootAgentsSeed},
		{rootClaudeFile, rootClaudeSeed},
	} {
		fh, err := os.OpenFile(filepath.Join(root, f.name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			if errors.Is(err, fs.ErrExist) {
				skipped = append(skipped, f.name)
				continue
			}
			return nil, nil, fmt.Errorf("seed %s: %w", f.name, err)
		}
		_, werr := fh.WriteString(f.content)
		if cerr := fh.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return nil, nil, fmt.Errorf("seed %s: %w", f.name, werr)
		}
		created = append(created, f.name)
	}
	return created, skipped, nil
}

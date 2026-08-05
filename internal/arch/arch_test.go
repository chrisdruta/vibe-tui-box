// Package arch pins the import graph AGENTS.md declares. The
// boundaries below are load-bearing invariants enforced nowhere else
// (no depguard rule, no CI graph check) — this test converts them from
// prose into failures.
package arch

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/chrisdruta/vibe-tui-box"

// imports maps each package directory (relative to the repo root) to
// the union of its non-test files' direct imports.
func imports(t *testing.T) map[string]map[string]bool {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]bool{}
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == "testdata" || name == "payload" || strings.HasPrefix(name, ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		pkg := filepath.ToSlash(rel)
		if out[pkg] == nil {
			out[pkg] = map[string]bool{}
		}
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			out[pkg][p] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatal("no Go packages found — walk is broken")
	}
	return out
}

func TestImportGraph(t *testing.T) {
	pkgs := imports(t)

	// dockerapi is the ONLY package that may see Docker SDK types.
	sdkPrefixes := []string{
		"github.com/docker/", "github.com/moby/", "github.com/containerd/",
	}
	for pkg, imps := range pkgs {
		if pkg == "internal/dockerapi" || strings.HasPrefix(pkg, "internal/dockerapi/") {
			continue
		}
		for imp := range imps {
			for _, sdk := range sdkPrefixes {
				if strings.HasPrefix(imp, sdk) {
					t.Errorf("%s imports Docker SDK package %s; only internal/dockerapi may", pkg, imp)
				}
			}
		}
	}

	// tmuxui renders pure views: terminal is its only project (and only
	// non-stdlib) dependency — no tmux calls, no Docker, no app.
	for imp := range pkgs["internal/tmuxui"] {
		if imp == module+"/internal/terminal" {
			continue
		}
		if strings.Contains(imp, ".") {
			t.Errorf("internal/tmuxui imports %s; terminal is its only allowed non-stdlib dependency", imp)
		}
	}

	// schema knows YAML but never Docker; model compiles the canonical
	// plan and never sees SDK types or the dockerapi client.
	for _, pkg := range []string{"internal/schema", "internal/model"} {
		if pkgs[pkg][module+"/internal/dockerapi"] {
			t.Errorf("%s imports internal/dockerapi", pkg)
		}
	}

	// The dependency arrows point strictly inward: nothing below the
	// command surface may reach back up into cli or app.
	for pkg, imps := range pkgs {
		if pkg == "cmd/vibe" || pkg == "internal/cli" {
			continue
		}
		if imps[module+"/internal/cli"] {
			t.Errorf("%s imports internal/cli", pkg)
		}
		if pkg != "internal/app" && imps[module+"/internal/app"] {
			t.Errorf("%s imports internal/app", pkg)
		}
	}
}

// TestMain guards against running outside the repo (go test ./... from
// a vendored copy, say) with a clearer failure than a nil map.
func TestMain(m *testing.M) {
	if _, err := os.Stat("../../go.mod"); err != nil {
		panic("internal/arch tests must run from the repository tree: " + err.Error())
	}
	os.Exit(m.Run())
}

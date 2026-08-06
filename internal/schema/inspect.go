package schema

import (
	"fmt"
	"strconv"

	"gopkg.in/yaml.v3"

	"vibe/internal/domain"
)

// Pos is a 1-based source position.
type Pos struct {
	Line   int
	Column int
}

// SourceIndex maps dotted schema paths ("runtime.ports[0]") to source
// positions. It is built during inspection; no parser nodes are
// retained after Load returns.
type SourceIndex map[string]Pos

// Pos looks up a path, returning the zero position when unknown.
func (idx SourceIndex) Pos(path string) Pos { return idx[path] }

// scalarTags are the only resolved tags the manifest may contain.
// Anything else (!!binary, !!timestamp, !!merge, custom tags) is
// rejected.
var scalarTags = map[string]bool{
	"!!str": true, "!!int": true, "!!bool": true,
	"!!float": true, "!!null": true,
}

type inspector struct {
	limits Limits
	index  SourceIndex
	nodes  int
}

// inspect validates the node graph against structural limits and
// records source positions for every schema path.
func inspect(root *yaml.Node, limits Limits) (SourceIndex, error) {
	if root.Kind != yaml.DocumentNode || len(root.Content) != 1 {
		return nil, fmt.Errorf("%w: manifest must be a single mapping document", domain.ErrInvalid)
	}
	ins := &inspector{limits: limits, index: make(SourceIndex)}
	if err := ins.walk(root.Content[0], "", 1); err != nil {
		return nil, err
	}
	return ins.index, nil
}

func (ins *inspector) walk(n *yaml.Node, path string, depth int) error {
	if depth > ins.limits.MaxDepth {
		return ins.errAt(n, path, fmt.Sprintf("nesting exceeds depth %d", ins.limits.MaxDepth))
	}
	if ins.nodes++; ins.nodes > ins.limits.MaxNodes {
		return ins.errAt(n, path, fmt.Sprintf("manifest exceeds %d nodes", ins.limits.MaxNodes))
	}
	if n.Anchor != "" {
		return ins.errAt(n, path, "anchors are not allowed")
	}
	if path != "" {
		ins.index[path] = Pos{Line: n.Line, Column: n.Column}
	}

	switch n.Kind {
	case yaml.AliasNode:
		return ins.errAt(n, path, "aliases are not allowed")
	case yaml.ScalarNode:
		if !scalarTags[n.Tag] {
			return ins.errAt(n, path, fmt.Sprintf("tag %s is not allowed", n.Tag))
		}
		if len(n.Value) > ins.limits.MaxScalar {
			return ins.errAt(n, path, fmt.Sprintf("scalar exceeds %d bytes", ins.limits.MaxScalar))
		}
		if err := checkText(n.Value); err != nil {
			return ins.errAt(n, path, err.Error())
		}
		return nil
	case yaml.SequenceNode:
		if len(n.Content) > ins.limits.MaxCollection {
			return ins.errAt(n, path, fmt.Sprintf("sequence exceeds %d entries", ins.limits.MaxCollection))
		}
		for i, c := range n.Content {
			if err := ins.walk(c, path+"["+strconv.Itoa(i)+"]", depth+1); err != nil {
				return err
			}
		}
		return nil
	case yaml.MappingNode:
		if len(n.Content)/2 > ins.limits.MaxCollection {
			return ins.errAt(n, path, fmt.Sprintf("mapping exceeds %d entries", ins.limits.MaxCollection))
		}
		seen := make(map[string]bool, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, val := n.Content[i], n.Content[i+1]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
				return ins.errAt(key, path, "mapping keys must be plain strings")
			}
			if err := checkText(key.Value); err != nil {
				return ins.errAt(key, path, "mapping key: "+err.Error())
			}
			if seen[key.Value] {
				return ins.errAt(key, path, fmt.Sprintf("duplicate key %q", key.Value))
			}
			seen[key.Value] = true
			child := key.Value
			if path != "" {
				child = path + "." + key.Value
			}
			ins.index[child] = Pos{Line: key.Line, Column: key.Column}
			if err := ins.walk(val, child, depth+1); err != nil {
				return err
			}
		}
		return nil
	default:
		return ins.errAt(n, path, "unsupported YAML node")
	}
}

func (ins *inspector) errAt(n *yaml.Node, path, msg string) error {
	return domain.FieldError{Path: path, Line: n.Line, Column: n.Column, Message: msg}
}

// checkText rejects NUL and C0/C1 control characters other than tab in
// scalar content (newlines inside block scalars arrive already split by
// the parser, but reject stray carriage returns and escapes).
func checkText(s string) error {
	for _, r := range s {
		if r == '\t' || r == '\n' {
			continue
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return fmt.Errorf("control character U+%04X is not allowed", r)
		}
	}
	return nil
}

package schema

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"vibe/internal/domain"
)

// Limits bound one manifest load. Zero fields take defaults.
type Limits struct {
	MaxBytes      int64
	MaxDepth      int
	MaxNodes      int
	MaxCollection int
	MaxScalar     int
}

// MaxBytesCeiling is the absolute manifest size limit; Limits.MaxBytes
// cannot raise it further.
const MaxBytesCeiling = 1 << 20

func DefaultLimits() Limits {
	return Limits{
		MaxBytes:      256 << 10,
		MaxDepth:      32,
		MaxNodes:      10000,
		MaxCollection: 1000,
		MaxScalar:     64 << 10,
	}
}

func (l Limits) withDefaults() Limits {
	d := DefaultLimits()
	if l.MaxBytes <= 0 {
		l.MaxBytes = d.MaxBytes
	}
	if l.MaxBytes > MaxBytesCeiling {
		l.MaxBytes = MaxBytesCeiling
	}
	if l.MaxDepth <= 0 {
		l.MaxDepth = d.MaxDepth
	}
	if l.MaxNodes <= 0 {
		l.MaxNodes = d.MaxNodes
	}
	if l.MaxCollection <= 0 {
		l.MaxCollection = d.MaxCollection
	}
	if l.MaxScalar <= 0 {
		l.MaxScalar = d.MaxScalar
	}
	return l
}

// Document is one successfully loaded manifest: the decoded types plus
// an index from schema paths to source positions.
type Document struct {
	Manifest Manifest
	Index    SourceIndex
}

// Load runs the full pipeline short of field validation: bounded read,
// UTF-8 and document checks, node-graph inspection, and KnownFields
// decode. Structural violations are errors; call Validate on the
// returned document for field diagnostics.
func Load(r io.Reader, limits Limits) (*Document, error) {
	limits = limits.withDefaults()

	source, err := io.ReadAll(io.LimitReader(r, limits.MaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	if int64(len(source)) > limits.MaxBytes {
		return nil, fmt.Errorf("%w: manifest exceeds %d bytes", domain.ErrInvalid, limits.MaxBytes)
	}
	if !utf8.Valid(source) {
		return nil, fmt.Errorf("%w: manifest is not valid UTF-8", domain.ErrInvalid)
	}
	if i := bytes.IndexByte(source, 0); i >= 0 {
		return nil, fmt.Errorf("%w: manifest contains a NUL byte", domain.ErrInvalid)
	}

	dec := yaml.NewDecoder(bytes.NewReader(source))
	var root yaml.Node
	if err := dec.Decode(&root); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%w: manifest is empty", domain.ErrInvalid)
		}
		return nil, fmt.Errorf("%w: %s", domain.ErrInvalid, yamlErrMessage(err))
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: manifest must contain exactly one YAML document", domain.ErrInvalid)
	}

	index, err := inspect(&root, limits)
	if err != nil {
		return nil, err
	}

	manifest, err := decode(source)
	if err != nil {
		return nil, err
	}

	return &Document{Manifest: manifest, Index: index}, nil
}

// yamlErrMessage strips the "yaml: " prefix the library adds so wrapped
// diagnostics read cleanly.
func yamlErrMessage(err error) string {
	msg := err.Error()
	if rest, ok := bytes.CutPrefix([]byte(msg), []byte("yaml: ")); ok {
		return string(rest)
	}
	return msg
}

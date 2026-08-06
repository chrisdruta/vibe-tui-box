package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"vibe/internal/domain"
)

// Versioned JSON records share one codec: deterministic marshalling, a
// format probe before the typed decode, and strict rejection of unknown
// fields. Every record kind (artifact, candidate, project) goes through
// these helpers so a change to the convention has exactly one home.

// MarshalRecord produces deterministic JSON: fixed field order from the
// struct, two-space indentation, one trailing newline.
func MarshalRecord(v any) ([]byte, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// DecodeRecord parses a versioned record: format probe, exact format
// match, then a DisallowUnknownFields decode. label names the record in
// diagnostics ("artifact record sha256:…").
func DecodeRecord[T any](data []byte, label string, wantFormat int) (T, error) {
	var zero T
	var probe struct {
		Format int `json:"format"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return zero, fmt.Errorf("%w: %s: %v", domain.ErrInvalid, label, err)
	}
	if probe.Format != wantFormat {
		return zero, fmt.Errorf("%w: %s format %d (supported: %d)", domain.ErrNotSupported, label, probe.Format, wantFormat)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var rec T
	if err := dec.Decode(&rec); err != nil {
		return zero, fmt.Errorf("%w: %s: %v", domain.ErrInvalid, label, err)
	}
	return rec, nil
}

// writeRecordOnce persists an immutable record file: an identical
// rewrite is success, divergent content is a conflict, and the first
// write lands atomically.
func writeRecordOnce(path, label string, rec any) error {
	data, err := MarshalRecord(rec)
	if err != nil {
		return err
	}
	if existing, err := os.ReadFile(path); err == nil {
		if bytes.Equal(existing, data) {
			return nil
		}
		return fmt.Errorf("%w: %s exists with different content", domain.ErrConflict, label)
	}
	return WriteFileAtomic(path, data, 0o600)
}

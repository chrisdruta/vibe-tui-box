package domain

import (
	"errors"
	"fmt"
)

// Sentinel error classes. Wrap them with %w so the CLI can map any
// failure to a stable exit code with errors.Is.
var (
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("conflict")
	ErrInvalid      = errors.New("invalid input")
	ErrUnavailable  = errors.New("unavailable")
	ErrNotSupported = errors.New("not supported")
	ErrCanceled     = errors.New("canceled")
)

// FieldError locates one diagnostic in a configuration source. Path is
// the dotted schema path ("runtime.ports[0]"); Line/Column are 1-based
// source positions, zero when unknown.
type FieldError struct {
	Path    string
	Line    int
	Column  int
	Message string
}

func (e FieldError) Error() string {
	switch {
	case e.Path != "" && e.Line > 0:
		return fmt.Sprintf("%s (line %d, column %d): %s", e.Path, e.Line, e.Column, e.Message)
	case e.Path != "":
		return fmt.Sprintf("%s: %s", e.Path, e.Message)
	case e.Line > 0:
		return fmt.Sprintf("line %d, column %d: %s", e.Line, e.Column, e.Message)
	default:
		return e.Message
	}
}

// FieldError diagnostics are always configuration problems.
func (e FieldError) Unwrap() error { return ErrInvalid }

// OpError attaches the failing operation and project to an underlying
// error without losing its class.
type OpError struct {
	Op      string
	Project ProjectID
	Err     error
}

func (e *OpError) Error() string {
	if e.Project != "" {
		return fmt.Sprintf("%s (project %s): %v", e.Op, e.Project, e.Err)
	}
	return fmt.Sprintf("%s: %v", e.Op, e.Err)
}

func (e *OpError) Unwrap() error { return e.Err }

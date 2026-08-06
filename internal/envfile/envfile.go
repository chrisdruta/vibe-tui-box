// Package envfile parses project env files literally: no shell syntax,
// no interpolation, no quoting semantics. Values are opaque container
// data and are never merged into a host process environment.
package envfile

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"vibe/internal/domain"
)

// Entry is one validated KEY=VALUE pair in source order.
type Entry struct {
	Key   string
	Value string
}

// Limits bound one env file. Zero fields take defaults.
type Limits struct {
	MaxEntries    int
	MaxKeyBytes   int
	MaxValueBytes int
	MaxTotalBytes int64
}

func (l Limits) withDefaults() Limits {
	if l.MaxEntries == 0 {
		l.MaxEntries = 1000
	}
	if l.MaxKeyBytes == 0 {
		l.MaxKeyBytes = 128
	}
	if l.MaxValueBytes == 0 {
		l.MaxValueBytes = 32 << 10
	}
	if l.MaxTotalBytes == 0 {
		l.MaxTotalBytes = 256 << 10
	}
	return l
}

// Parse reads the whole env file. Lines are LF- or CRLF-terminated;
// blank lines and lines whose first non-space character is '#' are
// skipped; everything else must be KEY=VALUE with a valid key. The
// value is the literal remainder of the line.
func Parse(r io.Reader, limits Limits) ([]Entry, error) {
	limits = limits.withDefaults()
	data, err := io.ReadAll(io.LimitReader(r, limits.MaxTotalBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read env file: %w", err)
	}
	if int64(len(data)) > limits.MaxTotalBytes {
		return nil, fmt.Errorf("%w: env file exceeds %d bytes", domain.ErrInvalid, limits.MaxTotalBytes)
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("%w: env file is not valid UTF-8", domain.ErrInvalid)
	}

	var entries []Entry
	seen := make(map[string]int)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(nil, int(limits.MaxTotalBytes)+1)
	line := 0
	for scanner.Scan() {
		line++
		raw := strings.TrimSuffix(scanner.Text(), "\r")
		if strings.ContainsRune(raw, 0) {
			return nil, fieldErr(line, "NUL byte")
		}
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, value, ok := strings.Cut(raw, "=")
		if !ok {
			return nil, fieldErr(line, "expected KEY=VALUE")
		}
		if err := validateKey(key, limits.MaxKeyBytes); err != nil {
			return nil, fieldErr(line, err.Error())
		}
		if len(value) > limits.MaxValueBytes {
			return nil, fieldErr(line, fmt.Sprintf("value exceeds %d bytes", limits.MaxValueBytes))
		}
		if prev, dup := seen[key]; dup {
			return nil, fieldErr(line, fmt.Sprintf("duplicate key %q (first set on line %d)", key, prev))
		}
		seen[key] = line
		entries = append(entries, Entry{Key: key, Value: value})
		if len(entries) > limits.MaxEntries {
			return nil, fieldErr(line, fmt.Sprintf("more than %d entries", limits.MaxEntries))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read env file: %w", err)
	}
	return entries, nil
}

func validateKey(key string, maxBytes int) error {
	if key == "" {
		return fmt.Errorf("empty key")
	}
	if len(key) > maxBytes {
		return fmt.Errorf("key exceeds %d bytes", maxBytes)
	}
	for i, c := range key {
		switch {
		case c == '_', c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
			if i == 0 {
				return fmt.Errorf("key %q starts with a digit", key)
			}
		default:
			return fmt.Errorf("key %q contains %q; allowed are [A-Za-z0-9_]", key, c)
		}
	}
	return nil
}

func fieldErr(line int, msg string) error {
	return domain.FieldError{Line: line, Column: 1, Message: msg}
}

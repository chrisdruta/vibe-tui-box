package domain

import (
	"crypto/rand"
	"fmt"
)

// ProjectID is a host-generated opaque identifier: 26 characters of
// lowercase base32 over 16 random bytes. It never derives from display
// names or paths.
type ProjectID string

// CandidateID aliases the digest hex of a candidate where an opaque ID
// is needed; candidates are otherwise addressed by Digest.
type CandidateID string

// RequestID names a broker rebuild request. Agents choose it inside the
// container, so parsing is strict and bounded.
type RequestID string

// base32lower is RFC 4648 base32, lowercased, without padding.
const base32lower = "abcdefghijklmnopqrstuvwxyz234567"

const projectIDLen = 26 // ceil(16 bytes * 8 / 5 bits)

func NewProjectID() (ProjectID, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate project id: %w", err)
	}
	out := make([]byte, 0, projectIDLen)
	var acc, bits uint
	for _, b := range raw {
		acc = acc<<8 | uint(b)
		bits += 8
		for bits >= 5 {
			bits -= 5
			out = append(out, base32lower[acc>>bits&31])
		}
	}
	if bits > 0 {
		out = append(out, base32lower[acc<<(5-bits)&31])
	}
	return ProjectID(out), nil
}

func ParseProjectID(s string) (ProjectID, error) {
	if len(s) != projectIDLen {
		return "", fmt.Errorf("%w: project id %q: want %d characters", ErrInvalid, s, projectIDLen)
	}
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < '2' || c > '7') {
			return "", fmt.Errorf("%w: project id %q: non-base32 character", ErrInvalid, s)
		}
	}
	return ProjectID(s), nil
}

// ParseRequestID validates an agent-chosen request name: 1-64 characters
// of lowercase alphanumerics and hyphens, starting with an alphanumeric.
func ParseRequestID(s string) (RequestID, error) {
	if len(s) == 0 || len(s) > 64 {
		return "", fmt.Errorf("%w: request id: want 1-64 characters, got %d", ErrInvalid, len(s))
	}
	for i, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-' && i > 0:
		default:
			return "", fmt.Errorf("%w: request id %q: allowed are [a-z0-9-], starting alphanumeric", ErrInvalid, s)
		}
	}
	return RequestID(s), nil
}

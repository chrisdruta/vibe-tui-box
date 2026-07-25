// Package assets embeds the container payload and project presets into
// the engine binary. Only internal/payload consumes this; everything
// else goes through payload.Bundle.
package assets

import "embed"

// PayloadFS holds payload/** exactly as tracked in the repository.
// Embedding does not preserve file modes; payload/manifest.json carries
// the authoritative modes and digests (regenerate with `go generate
// ./internal/payload`).
//
// The all: prefix is load-bearing: plain patterns skip dot-prefixed
// entries, and the claude plugin's mandatory .claude-plugin/ directory
// silently vanished from the embed while the manifest (a filesystem
// walk) kept it — every binary then failed its startup parity check.
//
//go:embed all:payload
var PayloadFS embed.FS

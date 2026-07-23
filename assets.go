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
//go:embed payload
var PayloadFS embed.FS

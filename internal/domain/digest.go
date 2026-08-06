// Package domain holds the small shared value types the engine passes
// between packages: digests, identifiers, platforms, and the error
// model. It depends on nothing inside the module.
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

// Digest is an immutable content address. The zero value means "no
// digest"; every non-zero value has been validated by a constructor.
type Digest struct {
	algorithm string
	hex       string
}

const sha256Alg = "sha256"

// ParseDigest accepts the canonical "sha256:<64 lowercase hex>" form.
func ParseDigest(s string) (Digest, error) {
	alg, hx, ok := strings.Cut(s, ":")
	if !ok || alg != sha256Alg {
		return Digest{}, fmt.Errorf("%w: digest %q: want sha256:<hex>", ErrInvalid, s)
	}
	if len(hx) != sha256.Size*2 {
		return Digest{}, fmt.Errorf("%w: digest %q: want %d hex characters", ErrInvalid, s, sha256.Size*2)
	}
	for _, c := range hx {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return Digest{}, fmt.Errorf("%w: digest %q: non-lowercase-hex character", ErrInvalid, s)
		}
	}
	return Digest{algorithm: sha256Alg, hex: hx}, nil
}

// SHA256 digests b.
func SHA256(b []byte) Digest {
	sum := sha256.Sum256(b)
	return Digest{algorithm: sha256Alg, hex: hex.EncodeToString(sum[:])}
}

// SHA256Reader digests everything readable from r without buffering it,
// so artifact-sized files never need to fit in memory.
func SHA256Reader(r io.Reader) (Digest, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return Digest{}, err
	}
	return Digest{algorithm: sha256Alg, hex: hex.EncodeToString(h.Sum(nil))}, nil
}

func (d Digest) String() string {
	if d.IsZero() {
		return ""
	}
	return d.algorithm + ":" + d.hex
}

func (d Digest) Hex() string { return d.hex }

// Short is the conventional 12-hex-character display form ("" for the
// zero digest) — the one short form shared by every renderer, and by
// the installed-binary name (`vibe-<short>`) that releases.go mints
// and gc.go's keep-set must reproduce exactly.
func (d Digest) Short() string {
	if len(d.hex) > 12 {
		return d.hex[:12]
	}
	return d.hex
}

func (d Digest) IsZero() bool { return d == Digest{} }

// MarshalText encodes the canonical string form; the zero digest
// encodes as the empty string so optional fields round-trip.
func (d Digest) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}

func (d *Digest) UnmarshalText(b []byte) error {
	if len(b) == 0 {
		*d = Digest{}
		return nil
	}
	parsed, err := ParseDigest(string(b))
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

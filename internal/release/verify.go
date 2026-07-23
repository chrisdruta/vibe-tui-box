package release

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"strings"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
)

// Verified reports a successful verification.
type Verified struct {
	ArchiveDigest domain.Digest
	Method        string
}

// Verifier checks a streamed archive digest against the release
// attestation. The M0 verifier consumes the release's checksums.txt;
// Sigstore attestation can replace it behind the same interface.
type Verifier interface {
	// Begin returns a hash the acquirer feeds the archive stream into,
	// and a finish function that validates the final digest.
	Begin(ctx context.Context, d Descriptor, attestation io.Reader) (hash.Hash, func() (Verified, error), error)
}

// maxChecksumsBytes bounds the attestation file.
const maxChecksumsBytes = 1 << 20

// ChecksumVerifier verifies against "sha256hex  filename" lines.
type ChecksumVerifier struct{}

func (ChecksumVerifier) Begin(ctx context.Context, d Descriptor, attestation io.Reader) (hash.Hash, func() (Verified, error), error) {
	want, err := findChecksum(attestation, d.ArchiveName)
	if err != nil {
		return nil, nil, err
	}
	h := sha256.New()
	finish := func() (Verified, error) {
		got := hex.EncodeToString(h.Sum(nil))
		if got != want {
			return Verified{}, fmt.Errorf("%w: archive %s digest sha256:%s does not match released checksum sha256:%s",
				domain.ErrConflict, d.ArchiveName, got, want)
		}
		digest, err := domain.ParseDigest("sha256:" + got)
		if err != nil {
			return Verified{}, err
		}
		return Verified{ArchiveDigest: digest, Method: "checksums.txt"}, nil
	}
	return h, finish, nil
}

func findChecksum(r io.Reader, name string) (string, error) {
	scanner := bufio.NewScanner(io.LimitReader(r, maxChecksumsBytes))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			continue
		}
		// Accept both "hash name" and "hash *name" (binary marker).
		if strings.TrimPrefix(fields[1], "*") != name {
			continue
		}
		sum := strings.ToLower(fields[0])
		if len(sum) != 64 {
			return "", fmt.Errorf("%w: malformed checksum for %s", domain.ErrInvalid, name)
		}
		for _, c := range sum {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				return "", fmt.Errorf("%w: malformed checksum for %s", domain.ErrInvalid, name)
			}
		}
		return sum, nil
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read checksums: %w", err)
	}
	return "", fmt.Errorf("%w: no checksum entry for %s", domain.ErrNotFound, name)
}

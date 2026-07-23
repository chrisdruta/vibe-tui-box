// Package release acquires engine release artifacts: resolve a version
// against the canonical source, stream the archive while hashing,
// verify it against the release attestation, extract only known entry
// types, validate the embedded payload manifest, and publish the result
// as an immutable store artifact.
package release

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/version"
)

// Descriptor names one downloadable release artifact.
type Descriptor struct {
	Version      string
	Platform     domain.Platform
	ArchiveName  string
	ArchiveURL   string
	ChecksumsURL string
}

// Source resolves versions and opens release streams.
type Source interface {
	Resolve(ctx context.Context, version string, platform domain.Platform) (Descriptor, error)
	OpenArtifact(ctx context.Context, d Descriptor) (io.ReadCloser, error)
	OpenAttestation(ctx context.Context, d Descriptor) (io.ReadCloser, error)
}

// DefaultBaseURL is the canonical release source.
const DefaultBaseURL = "https://github.com/chrisdruta/vibe-tui-box/releases/download"

// HTTPSource fetches releases over HTTPS with explicit timeouts, a
// redirect cap, and a fixed user agent.
type HTTPSource struct {
	base   string
	client *http.Client
}

func NewHTTPSource(base string) *HTTPSource {
	return &HTTPSource{
		base: base,
		client: &http.Client{
			Timeout: 5 * time.Minute,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
		},
	}
}

func (s *HTTPSource) Resolve(ctx context.Context, ver string, platform domain.Platform) (Descriptor, error) {
	if ver == "" || ver[0] != 'v' {
		return Descriptor{}, fmt.Errorf("%w: release version %q (want v-prefixed, e.g. v2.0.0)", domain.ErrInvalid, ver)
	}
	name := fmt.Sprintf("vibe_%s_%s_%s.tar.gz", ver, platform.OS, platform.Arch)
	return Descriptor{
		Version:      ver,
		Platform:     platform,
		ArchiveName:  name,
		ArchiveURL:   s.base + "/" + ver + "/" + name,
		ChecksumsURL: s.base + "/" + ver + "/checksums.txt",
	}, nil
}

func (s *HTTPSource) OpenArtifact(ctx context.Context, d Descriptor) (io.ReadCloser, error) {
	return s.get(ctx, d.ArchiveURL)
}

func (s *HTTPSource) OpenAttestation(ctx context.Context, d Descriptor) (io.ReadCloser, error) {
	return s.get(ctx, d.ChecksumsURL)
}

func (s *HTTPSource) get(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "vibe/"+version.Get().Release)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: fetch %s: %v", domain.ErrUnavailable, url, err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return resp.Body, nil
	case http.StatusNotFound:
		resp.Body.Close()
		return nil, fmt.Errorf("%w: %s", domain.ErrNotFound, url)
	default:
		resp.Body.Close()
		return nil, fmt.Errorf("%w: fetch %s: HTTP %d", domain.ErrUnavailable, url, resp.StatusCode)
	}
}

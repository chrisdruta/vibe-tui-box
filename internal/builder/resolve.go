package builder

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/schema"
)

// resolve.go probes the version a channel-tracking agent's channel
// currently points at — the pre-rebuild check that lets the refresh
// token mean "the resolved upstream version" instead of "now", so
// Docker's layer cache re-pulls an agent exactly when upstream moved
// and stays warm when it didn't. Each endpoint is the same source the
// agent's own installer resolves through, one small GET apiece.

const (
	// claude's installer resolves its channel through a bare version
	// file per channel (install.sh's DOWNLOAD_BASE_URL/latest).
	claudeReleasesURL = "https://downloads.claude.ai/claude-code-releases/"
	// codex is npm-distributed; the dist-tags document maps channel
	// names to versions.
	codexDistTagsURL = "https://registry.npmjs.org/-/package/@openai/codex/dist-tags"
	// grok's installer probes BASE_URL_PRIMARY/CHANNEL, a bare version
	// file like claude's.
	grokReleasesURL = "https://x.ai/cli/"
)

// resolvedVersionRe bounds what a probe may return: the value becomes
// a build arg whose expansion lands inside generated RUN text, so it
// gets the same closed, shell-inert charset schema.validAgentVersion
// enforces on manifest pins — version-shaped, bounded, nothing with
// shell meaning. Anything else is a failed probe, never a token.
var resolvedVersionRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9][0-9A-Za-z._+-]{0,60}$`)

// channelRe vets the channel path segment (a manifest channel word or
// an engine default) before it joins a URL.
var channelRe = regexp.MustCompile(`^[0-9A-Za-z._-]{1,64}$`)

// probeBodyLimit bounds a probe response read: version files are a few
// bytes, the dist-tags document a few hundred.
const probeBodyLimit = 1 << 16

// AgentVersionProbe resolves agent channels over HTTP. A nil Client
// uses http.DefaultClient; callers bound each probe with a context
// deadline.
type AgentVersionProbe struct {
	Client *http.Client
}

func (p AgentVersionProbe) Resolve(ctx context.Context, kind schema.AgentKind, channel string) (string, error) {
	if !channelRe.MatchString(channel) {
		return "", fmt.Errorf("%w: agent channel %q", domain.ErrInvalid, channel)
	}
	var version string
	var err error
	switch kind {
	case schema.AgentClaude:
		version, err = p.fetchVersionFile(ctx, claudeReleasesURL+channel)
	case schema.AgentGrok:
		version, err = p.fetchVersionFile(ctx, grokReleasesURL+channel)
	case schema.AgentCodex:
		version, err = p.fetchDistTag(ctx, channel)
	default:
		return "", fmt.Errorf("%w: unknown agent kind %q", domain.ErrInvalid, kind)
	}
	if err != nil {
		return "", err
	}
	if !resolvedVersionRe.MatchString(version) {
		return "", fmt.Errorf("%w: %s/%s resolved to non-version content", domain.ErrUnavailable, kind, channel)
	}
	return version, nil
}

func (p AgentVersionProbe) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: GET %s: %s", domain.ErrUnavailable, url, res.Status)
	}
	return io.ReadAll(io.LimitReader(res.Body, probeBodyLimit))
}

// fetchVersionFile reads a bare-version channel file (claude, grok):
// the first line is the version.
func (p AgentVersionProbe) fetchVersionFile(ctx context.Context, url string) (string, error) {
	body, err := p.get(ctx, url)
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(string(body), "\n")
	return strings.TrimSpace(line), nil
}

// fetchDistTag reads npm's dist-tags document and picks the channel's
// tag (unversioned codex tracks "latest").
func (p AgentVersionProbe) fetchDistTag(ctx context.Context, channel string) (string, error) {
	body, err := p.get(ctx, codexDistTagsURL)
	if err != nil {
		return "", err
	}
	tags := map[string]string{}
	if err := json.Unmarshal(body, &tags); err != nil {
		return "", fmt.Errorf("%w: dist-tags: %v", domain.ErrUnavailable, err)
	}
	v, ok := tags[channel]
	if !ok {
		return "", fmt.Errorf("%w: dist-tag %q not published", domain.ErrUnavailable, channel)
	}
	return v, nil
}

// ParseAgentRefresh decodes a persisted AgentRefresh token's per-agent
// pairs ("claude=2.1.220 codex=0.146.0") into kind→value. A legacy
// bare-timestamp token (minted before the per-agent fingerprint) has
// no pairs and parses empty — callers then fall back to the whole
// token, which busts every channel-tracking layer exactly as that
// token always did.
func ParseAgentRefresh(token string) map[schema.AgentKind]string {
	out := map[schema.AgentKind]string{}
	for field := range strings.FieldsSeq(token) {
		if kind, value, ok := strings.Cut(field, "="); ok && kind != "" && value != "" {
			out[schema.AgentKind(kind)] = value
		}
	}
	return out
}

package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chrisdruta/vibe-tui-box/internal/dockerapi"
	"github.com/chrisdruta/vibe-tui-box/internal/domain"
	"github.com/chrisdruta/vibe-tui-box/internal/model"
	"github.com/chrisdruta/vibe-tui-box/internal/terminal"
)

// Egress view bounds: everything read from containers is capped before
// parsing (the broker's read discipline applied to log streams), and
// every retained field is sanitized here so renderers and --json share
// one safe model.
const (
	egressDefaultTail    = 2000
	egressMaxBytes       = 1 << 20
	egressMaxDomains     = 500
	egressMaxConns       = 512
	egressSamplerTimeout = 10 * time.Second
	// egressSampleFormat is the sampler's version line; the script and
	// this parser move together inside one artifact.
	egressSampleFormat = "egress-sample\t1"
)

// EgressRequest drives the egress view: the project's DNS ledger (the
// dns sidecar's query log) joined with a one-shot in-container socket
// sample.
type EgressRequest struct {
	Dir  string
	Tail int // dns log lines to read; 0 means the default window
}

// EgressDomain is one deduplicated ledger entry. Name and LastType are
// sanitized at construction — domains are container-controlled bytes.
type EgressDomain struct {
	Name     string
	Queries  int
	LastType string
}

// EgressConn is one sampled live socket. Fields are engine-validated:
// addresses pass a strict shape gate, Comm is sanitized, PID 0 means
// the socket belongs to another uid and stays unattributed.
type EgressConn struct {
	Proto  string
	Local  string
	Remote string
	PID    int
	Comm   string
}

// EgressResult is the fused view. Each half degrades independently:
// an absent dns sidecar or a stopped dev container leaves its half
// unavailable with an engine-authored note, and only both missing is
// an error.
type EgressResult struct {
	Project          domain.ProjectID
	DNSAvailable     bool
	DNSNote          string
	Domains          []EgressDomain
	UnparsedLogLines int
	DomainsTruncated bool
	LogTruncated     bool
	SamplerAvailable bool
	SamplerNote      string
	Conns            []EgressConn
	MalformedRows    int
	ConnsTruncated   bool
}

// Egress reads the dns sidecar's query log through the logs API and
// runs the payload sampler in the dev container, returning the parsed,
// sanitized view. Log bytes never reach a terminal raw on this path —
// unlike `vibe logs`, this is a rendered surface.
func (a *App) Egress(ctx context.Context, req EgressRequest) (EgressResult, error) {
	_, rec, err := a.resolveProject(ctx, req.Dir)
	if err != nil {
		return EgressResult{}, opError("egress", "", err)
	}
	res := EgressResult{Project: rec.ID}
	tail := req.Tail
	if tail <= 0 {
		tail = egressDefaultTail
	}

	logs := &cappedWriter{max: egressMaxBytes}
	dnsName := dockerapi.ContainerName(model.SidecarContainerName(rec.ID, model.DNSServiceName))
	err = a.deps.Docker.Logs(ctx, dockerapi.LogsRequest{
		Container: dnsName,
		Tail:      tail,
		Streams:   dockerapi.Streams{Out: logs, Err: logs},
	})
	switch {
	case errors.Is(err, domain.ErrNotFound):
		res.DNSNote = "dns sidecar not found (vibe up creates it unless runtime.egress is off)"
	case err != nil:
		return EgressResult{}, opError("egress", rec.ID, err)
	default:
		res.DNSAvailable = true
		res.LogTruncated = logs.truncated
		res.Domains, res.UnparsedLogLines, res.DomainsTruncated = parseCoreDNSLogs(logs.buf.String(), egressMaxDomains)
	}

	if devName, derr := a.requireDevContainer(ctx, rec); derr != nil {
		// Ctrl-C must surface as an interrupt (exit 130), never be
		// absorbed into a note that lets the command exit 0.
		if ctx.Err() != nil {
			return EgressResult{}, opError("egress", rec.ID,
				fmt.Errorf("%w: egress: %v", domain.ErrCanceled, context.Cause(ctx)))
		}
		res.SamplerNote = "dev container is not running (run `vibe up`)"
	} else {
		out := &cappedWriter{max: egressMaxBytes}
		sctx, cancel := context.WithTimeout(ctx, egressSamplerTimeout)
		eres, xerr := a.execIn(sctx, devName, ContainerCommand{
			Argv:    []string{"bash", model.PayloadEgressSample},
			Streams: dockerapi.Streams{Out: out, Err: io.Discard},
		})
		cancel()
		switch {
		case xerr != nil && ctx.Err() != nil:
			// The parent context (not the sampler's own timeout) died:
			// this is an interrupt, not a degraded view.
			return EgressResult{}, opError("egress", rec.ID,
				fmt.Errorf("%w: egress: %v", domain.ErrCanceled, context.Cause(ctx)))
		case xerr != nil:
			res.SamplerNote = fmt.Sprintf("sampler failed: %v", xerr)
		case eres.ExitCode != 0:
			res.SamplerNote = fmt.Sprintf("sampler exited with code %d", eres.ExitCode)
		default:
			conns, malformed, truncated, ok := parseEgressSample(out.buf.String())
			if !ok {
				res.SamplerNote = "sampler output not understood (payload and container out of sync? vibe rebuild)"
			} else {
				res.SamplerAvailable = true
				res.Conns = conns
				res.MalformedRows = malformed
				res.ConnsTruncated = truncated
			}
		}
	}

	if !res.DNSAvailable && !res.SamplerAvailable {
		return EgressResult{}, opError("egress", rec.ID,
			fmt.Errorf("%w: %s; %s", domain.ErrUnavailable, res.DNSNote, res.SamplerNote))
	}
	return res, nil
}

// cappedWriter buffers up to max bytes and silently drops the rest,
// reporting only that truncation happened. It never errors: the log
// stream must drain, not abort.
type cappedWriter struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	if room := w.max - w.buf.Len(); room > 0 {
		if len(p) > room {
			w.buf.Write(p[:room])
			w.truncated = true
		} else {
			w.buf.Write(p)
		}
	} else if len(p) > 0 {
		w.truncated = true
	}
	return len(p), nil
}

var egressQTypeRe = regexp.MustCompile(`^[A-Z0-9]{1,10}$`)

// parseCoreDNSLogs folds CoreDNS query-log lines into a deduplicated
// domain ledger. It anchors on the quoted seven-field request section
// of the log plugin's common format —
//
//	[INFO] 10.0.0.2:52580 - 29008 "A IN example.org. udp 47 false 4096" NOERROR qr,rd,ra 110 0.0005s
//
// — and counts everything else (startup banner, level prefixes, other
// plugins' lines) as unparsed: never fatal, never retained, never
// rendered. Retained fields are sanitized here.
func parseCoreDNSLogs(raw string, maxDomains int) (domains []EgressDomain, unparsed int, truncated bool) {
	type entry struct {
		queries  int
		lastType string
	}
	seen := map[string]*entry{}
	for line := range strings.SplitSeq(raw, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		open := strings.IndexByte(line, '"')
		if open < 0 || !strings.Contains(line[:open], " - ") {
			unparsed++
			continue
		}
		rest := line[open+1:]
		end := strings.IndexByte(rest, '"')
		if end < 0 {
			unparsed++
			continue
		}
		fields := strings.Split(rest[:end], " ")
		if len(fields) != 7 {
			unparsed++
			continue
		}
		qtype, qname := fields[0], fields[2]
		if !egressQTypeRe.MatchString(qtype) {
			unparsed++
			continue
		}
		name := strings.ToLower(strings.TrimSuffix(qname, "."))
		if name == "" || len(name) > 253 {
			unparsed++
			continue
		}
		// Sanitize before dedup so hostile spellings of one name merge.
		name = terminal.Line(name, 253)
		e := seen[name]
		if e == nil {
			if len(seen) >= maxDomains {
				truncated = true
				continue
			}
			e = &entry{}
			seen[name] = e
		}
		e.queries++
		e.lastType = qtype
	}
	domains = make([]EgressDomain, 0, len(seen))
	for name, e := range seen {
		domains = append(domains, EgressDomain{Name: name, Queries: e.queries, LastType: e.lastType})
	}
	sort.Slice(domains, func(i, j int) bool {
		if domains[i].Queries != domains[j].Queries {
			return domains[i].Queries > domains[j].Queries
		}
		return domains[i].Name < domains[j].Name
	})
	return domains, unparsed, truncated
}

// egressAddrRe is the strict shape gate for sampler addresses: the
// characters the script can legitimately emit (hex, dots, colons,
// brackets) and nothing else, so a passing address is terminal-safe by
// charset.
var egressAddrRe = regexp.MustCompile(`^[0-9a-f.:\[\]]{1,53}$`)

var egressProtos = map[string]bool{"tcp": true, "tcp6": true, "udp": true, "udp6": true}

// parseEgressSample decodes the sampler's versioned TSV. A wrong or
// missing version line fails the whole sample (ok=false — the payload
// and the running container disagree); malformed rows are counted and
// dropped, never fatal.
func parseEgressSample(raw string) (conns []EgressConn, malformed int, truncated, ok bool) {
	lines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
	if len(lines) == 0 || lines[0] != egressSampleFormat {
		return nil, 0, false, false
	}
	for _, line := range lines[1:] {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 5 || !egressProtos[f[0]] || !egressAddrRe.MatchString(f[1]) || !egressAddrRe.MatchString(f[2]) {
			malformed++
			continue
		}
		pid := 0
		if f[3] != "-" {
			n, err := strconv.Atoi(f[3])
			if err != nil || n < 1 {
				malformed++
				continue
			}
			pid = n
		}
		comm := ""
		if f[4] != "-" {
			comm = terminal.Line(f[4], 32)
		}
		if len(conns) >= egressMaxConns {
			// Truncation means a row was actually dropped — an exactly
			// full undropped sample is not truncated (mirrors
			// parseCoreDNSLogs).
			truncated = true
			break
		}
		conns = append(conns, EgressConn{Proto: f[0], Local: f[1], Remote: f[2], PID: pid, Comm: comm})
	}
	return conns, malformed, truncated, true
}

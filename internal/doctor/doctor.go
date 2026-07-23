// Package doctor runs environment and project health checks. Checks
// are independent, run with bounded concurrency, and report in
// registration order regardless of completion order.
package doctor

import (
	"context"
	"time"

	"github.com/chrisdruta/vibe-tui-box/internal/dockerapi"
	"github.com/chrisdruta/vibe-tui-box/internal/paths"
	"github.com/chrisdruta/vibe-tui-box/internal/registry"
	"github.com/chrisdruta/vibe-tui-box/internal/store"
)

type Status string

const (
	OK   Status = "ok"
	Warn Status = "warn"
	Fail Status = "fail"
	Skip Status = "skip"
)

// Result is one completed check.
type Result struct {
	Name     string        `json:"name"`
	Status   Status        `json:"status"`
	Summary  string        `json:"summary"`
	Details  []string      `json:"details,omitempty"`
	Duration time.Duration `json:"duration_ns"`
}

// Input carries the resolved context checks may inspect. Record and
// Root are nil when the working directory is not a registered project;
// project-scoped checks then skip.
type Input struct {
	Layout   paths.Layout
	Store    *store.Store
	Docker   dockerapi.Client
	Root     *paths.Root
	Record   *registry.Record
	TmuxPath string
}

// Check is one named health probe.
type Check interface {
	Name() string
	Run(ctx context.Context, in *Input) Result
}

type checkFunc struct {
	name string
	fn   func(ctx context.Context, in *Input) Result
}

func (c checkFunc) Name() string { return c.name }

func (c checkFunc) Run(ctx context.Context, in *Input) Result {
	start := time.Now()
	res := c.fn(ctx, in)
	res.Name = c.name
	res.Duration = time.Since(start)
	return res
}

// concurrency bounds simultaneous checks.
const concurrency = 4

// Run executes every check and returns results in check order.
func Run(ctx context.Context, in *Input, checks []Check) []Result {
	results := make([]Result, len(checks))
	sem := make(chan struct{}, concurrency)
	done := make(chan int)
	for i, c := range checks {
		go func(i int, c Check) {
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = c.Run(ctx, in)
			done <- i
		}(i, c)
	}
	for range checks {
		<-done
	}
	return results
}

// Healthy reports whether no check failed.
func Healthy(results []Result) bool {
	for _, r := range results {
		if r.Status == Fail {
			return false
		}
	}
	return true
}

package terminal

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"vibe/internal/domain"
)

// Confirmation separates trusted chrome from encoded untrusted content
// so the prompt renderer cannot be spoofed by request text.
type Confirmation struct {
	Title     string
	Question  string // trusted ask line; "" renders the default "approve?"
	Digest    domain.Digest
	Chrome    []string // trusted lines from the engine
	Content   Encoded  // sanitized untrusted lines
	DiffLabel string   // trusted heading for Diff; "" hides the section
	Diff      Encoded  // sanitized plan diff (terminal.Diff output)
}

// Prompt asks the operator for decisions.
type Prompt interface {
	Confirm(ctx context.Context, c Confirmation) (bool, error)
}

// StdioPrompt confirms on an interactive terminal. Out receives the
// rendered confirmation; In is read line-wise for y/N.
type StdioPrompt struct {
	In  io.Reader
	Out io.Writer
}

func (p *StdioPrompt) Confirm(ctx context.Context, c Confirmation) (bool, error) {
	fmt.Fprintf(p.Out, "%s\n", c.Title)
	if !c.Digest.IsZero() {
		fmt.Fprintf(p.Out, "digest: %s\n", c.Digest)
	}
	for _, line := range c.Chrome {
		fmt.Fprintf(p.Out, "%s\n", line)
	}
	for _, line := range c.Content.Lines {
		fmt.Fprintf(p.Out, "  │ %s\n", line)
	}
	if c.Content.Truncated {
		fmt.Fprintln(p.Out, "  │ … (truncated)")
	}
	if c.DiffLabel != "" {
		fmt.Fprintf(p.Out, "%s\n", c.DiffLabel)
		for _, line := range c.Diff.Lines {
			fmt.Fprintf(p.Out, "  │ %s\n", line)
		}
		if c.Diff.Truncated {
			fmt.Fprintln(p.Out, "  │ … (truncated)")
		}
	}
	question := c.Question
	if question == "" {
		question = "approve?"
	}
	fmt.Fprintf(p.Out, "%s [y/N] ", question)

	answer := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(p.In).ReadString('\n')
		if err != nil {
			errCh <- err
			return
		}
		answer <- line
	}()
	select {
	case <-ctx.Done():
		return false, fmt.Errorf("%w: %v", domain.ErrCanceled, context.Cause(ctx))
	case err := <-errCh:
		return false, fmt.Errorf("read confirmation: %w", err)
	case line := <-answer:
		v := strings.ToLower(strings.TrimSpace(line))
		return v == "y" || v == "yes", nil
	}
}

// AutoApprove is a non-interactive prompt for --yes flows and tests.
type AutoApprove struct{ Approve bool }

func (a AutoApprove) Confirm(context.Context, Confirmation) (bool, error) { return a.Approve, nil }

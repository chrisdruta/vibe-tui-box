package dockerapi

import (
	"context"
	"fmt"
	"io"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
)

// Exec runs argv inside a container and streams I/O until the process
// exits. TTY mode multiplexes nothing; non-TTY output is demultiplexed
// into the separate out/err writers.
func (s *SDK) Exec(ctx context.Context, req ExecRequest) (ExecResult, error) {
	if len(req.Argv) == 0 {
		return ExecResult{}, fmt.Errorf("%w: exec requires argv", domain.ErrInvalid)
	}
	opts := container.ExecOptions{
		User:         req.User,
		WorkingDir:   req.WorkDir,
		Env:          req.Env,
		Cmd:          req.Argv,
		Tty:          req.TTY,
		AttachStdin:  req.Stdin,
		AttachStdout: true,
		AttachStderr: true,
	}
	// The exec pty is born 0x0 unless sized HERE: a resize sent before
	// the process starts is rejected by the daemon, and the resize
	// forwarder's first send is exactly that race — the lost initial
	// size left TTY execs reading 0x0 from stty until the next window
	// change (docker's own CLI pins ConsoleSize at create for the same
	// reason). The caller's first size is already sitting in the
	// channel (setupStreams sends it synchronously); peek it without
	// blocking so a caller that never sends is unaffected.
	if req.TTY && req.Streams.Resize != nil {
		select {
		case size, ok := <-req.Streams.Resize:
			if ok {
				opts.ConsoleSize = &[2]uint{uint(size.Height), uint(size.Width)}
			}
		default:
		}
	}
	created, err := s.cli.ContainerExecCreate(ctx, string(req.Container), opts)
	if err != nil {
		return ExecResult{}, mapErr("exec in "+string(req.Container), err)
	}

	hijack, err := s.cli.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{Tty: req.TTY})
	if err != nil {
		return ExecResult{}, mapErr("attach exec", err)
	}
	defer hijack.Close()

	if req.TTY && req.Streams.Resize != nil {
		go s.forwardExecResize(ctx, created.ID, req.Streams.Resize)
	}
	if err := pumpStreams(ctx, hijack, req.TTY, req.Streams); err != nil {
		return ExecResult{}, err
	}

	inspect, err := s.cli.ContainerExecInspect(ctx, created.ID)
	if err != nil {
		return ExecResult{}, mapErr("inspect exec", err)
	}
	return ExecResult{ExitCode: inspect.ExitCode}, nil
}

// Attach connects to the container's main process.
func (s *SDK) Attach(ctx context.Context, req AttachRequest) error {
	hijack, err := s.cli.ContainerAttach(ctx, string(req.Container), container.AttachOptions{
		Stream: true,
		Stdin:  req.Streams.In != nil,
		Stdout: true,
		Stderr: true,
	})
	if err != nil {
		return mapErr("attach to "+string(req.Container), err)
	}
	defer hijack.Close()

	if req.TTY && req.Streams.Resize != nil {
		go func() {
			for size := range req.Streams.Resize {
				_ = s.cli.ContainerResize(ctx, string(req.Container), container.ResizeOptions{
					Height: uint(size.Height),
					Width:  uint(size.Width),
				})
			}
		}()
	}
	return pumpStreams(ctx, hijack, req.TTY, req.Streams)
}

// Logs streams container logs into the caller's writers, demultiplexed
// (engine containers never allocate a TTY). In follow mode the copy
// runs until the container stops or ctx is canceled.
func (s *SDK) Logs(ctx context.Context, req LogsRequest) error {
	tail := "all"
	if req.Tail > 0 {
		tail = fmt.Sprintf("%d", req.Tail)
	}
	rc, err := s.cli.ContainerLogs(ctx, string(req.Container), container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     req.Follow,
		Tail:       tail,
	})
	if err != nil {
		return mapErr("logs of "+string(req.Container), err)
	}
	defer rc.Close()

	out := req.Streams.Out
	if out == nil {
		out = io.Discard
	}
	errOut := req.Streams.Err
	if errOut == nil {
		errOut = io.Discard
	}
	if _, err := stdcopy.StdCopy(out, errOut, rc); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("%w: %v", domain.ErrCanceled, context.Cause(ctx))
		}
		return fmt.Errorf("logs of %s: %w", req.Container, err)
	}
	return nil
}

func (s *SDK) forwardExecResize(ctx context.Context, execID string, resize <-chan WindowSize) {
	for {
		select {
		case <-ctx.Done():
			return
		case size, ok := <-resize:
			if !ok {
				return
			}
			_ = s.cli.ContainerExecResize(ctx, execID, container.ResizeOptions{
				Height: uint(size.Height),
				Width:  uint(size.Width),
			})
		}
	}
}

// pumpStreams copies the hijacked connection to the caller's streams
// until the remote side closes, honoring cancellation. Stdin copying
// stops when the connection closes; its goroutine must never block the
// return.
func pumpStreams(ctx context.Context, hijack types.HijackedResponse, tty bool, streams Streams) error {
	out := streams.Out
	if out == nil {
		out = io.Discard
	}
	errOut := streams.Err
	if errOut == nil {
		errOut = io.Discard
	}

	if streams.In != nil {
		go func() {
			_, _ = io.Copy(hijack.Conn, streams.In)
			_ = hijack.CloseWrite()
		}()
	}

	done := make(chan error, 1)
	go func() {
		var err error
		if tty {
			_, err = io.Copy(out, hijack.Reader)
		} else {
			_, err = stdcopy.StdCopy(out, errOut, hijack.Reader)
		}
		done <- err
	}()

	select {
	case <-ctx.Done():
		hijack.Close()
		<-done
		return fmt.Errorf("%w: %v", domain.ErrCanceled, context.Cause(ctx))
	case err := <-done:
		if err != nil && ctx.Err() == nil {
			return fmt.Errorf("stream copy: %w", err)
		}
		return nil
	}
}

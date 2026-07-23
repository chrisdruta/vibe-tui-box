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
	created, err := s.cli.ContainerExecCreate(ctx, string(req.Container), container.ExecOptions{
		User:         req.User,
		WorkingDir:   req.WorkDir,
		Env:          req.Env,
		Cmd:          req.Argv,
		Tty:          req.TTY,
		AttachStdin:  req.Stdin,
		AttachStdout: true,
		AttachStderr: true,
	})
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

package cli

import (
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"

	"github.com/chrisdruta/vibe-tui-box/internal/dockerapi"
)

// setupStreams wires stdio into dockerapi.Streams. When both ends are
// terminals it switches stdin to raw mode and forwards window resizes;
// the returned restore function must run before printing anything else.
func setupStreams(wantStdin bool) (streams dockerapi.Streams, tty bool, restore func()) {
	restore = func() {}
	streams = dockerapi.Streams{Out: os.Stdout, Err: os.Stderr}
	if wantStdin {
		streams.In = os.Stdin
	}

	inFd := int(os.Stdin.Fd())
	outFd := int(os.Stdout.Fd())
	tty = wantStdin && term.IsTerminal(inFd) && term.IsTerminal(outFd)
	if !tty {
		return streams, false, restore
	}

	oldState, err := term.MakeRaw(inFd)
	if err != nil {
		return streams, false, restore
	}

	resize := make(chan dockerapi.WindowSize, 1)
	streams.Resize = resize
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	stop := make(chan struct{})

	sendSize := func() {
		if w, h, err := term.GetSize(outFd); err == nil {
			select {
			case resize <- dockerapi.WindowSize{Width: w, Height: h}:
			default:
			}
		}
	}
	sendSize()
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-winch:
				sendSize()
			}
		}
	}()

	restore = func() {
		signal.Stop(winch)
		close(stop)
		_ = term.Restore(inFd, oldState)
	}
	return streams, true, restore
}

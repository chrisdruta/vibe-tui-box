package dockerapi

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
)

// buildSDK implements Client.Build: tar the frozen candidate context,
// submit it, forward normalized progress, and pin the result by local
// image ID.
func (s *SDK) buildSDK(ctx context.Context, req BuildRequest, sink ProgressSink) (BuiltImage, error) {
	contextTar, err := tarDirectory(req.ContextDir)
	if err != nil {
		return BuiltImage{}, err
	}
	args := make(map[string]*string, len(req.BuildArgs))
	for k, v := range req.BuildArgs {
		args[k] = &v
	}
	opts := build.ImageBuildOptions{
		Tags:        []string{req.Tag},
		Dockerfile:  req.Dockerfile,
		BuildArgs:   args,
		Remove:      true,
		ForceRemove: true,
	}
	resp, err := s.cli.ImageBuild(ctx, bytes.NewReader(contextTar), opts)
	if err != nil {
		return BuiltImage{}, mapErr("build image "+req.Tag, err)
	}
	defer resp.Body.Close()
	sink.Emit(Progress{Stage: StageBuildStart, Message: req.Tag})
	if err := forwardBuildProgress(resp.Body, sink); err != nil {
		return BuiltImage{}, fmt.Errorf("build image %s: %w", req.Tag, err)
	}

	inspect, err := s.cli.ImageInspect(ctx, req.Tag)
	if err != nil {
		return BuiltImage{}, mapErr("inspect built image "+req.Tag, err)
	}
	digest, err := domain.ParseDigest(inspect.ID)
	if err != nil {
		return BuiltImage{}, fmt.Errorf("built image %s has unparseable id %q", req.Tag, inspect.ID)
	}
	sink.Emit(Progress{Stage: StageBuild, Message: req.Tag, Done: true})
	return BuiltImage{Ref: ImageRef(req.Tag), Digest: digest}, nil
}

type buildMessage struct {
	Stream string `json:"stream"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
}

// stepLineRE matches the classic builder's step transition line. The
// daemon numbers every Dockerfile instruction, so N/M arrive complete
// on the first line of the stream.
var stepLineRE = regexp.MustCompile(`^Step (\d+)/(\d+) : (.*)$`)

// forwardBuildProgress classifies the daemon's classic-builder stream
// into step transitions, cache verdicts, and raw layer output. The
// stream payloads carry their own newlines and may split or batch
// lines, so it re-buffers into whole lines. Builder bookkeeping lines
// ("---> <id>", intermediate-container chatter, the final Successfully
// pair) inform nothing and are dropped.
func forwardBuildProgress(r io.Reader, sink ProgressSink) error {
	dec := json.NewDecoder(r)
	var buf strings.Builder
	step, steps := 0, 0
	emitLine := func(line string) {
		line = strings.TrimRight(line, "\r")
		switch {
		case line == "":
		case stepLineRE.MatchString(line):
			m := stepLineRE.FindStringSubmatch(line)
			step, _ = strconv.Atoi(m[1])
			steps, _ = strconv.Atoi(m[2])
			sink.Emit(Progress{Stage: StageBuild, Message: m[3], Step: step, Steps: steps, StepStart: true})
		case strings.HasPrefix(line, " ---> Using cache"):
			sink.Emit(Progress{Stage: StageBuild, Step: step, Steps: steps, Cached: true})
		case strings.HasPrefix(line, " ---> "),
			strings.HasPrefix(line, "Removing intermediate container"),
			strings.HasPrefix(line, "Successfully built "),
			strings.HasPrefix(line, "Successfully tagged "):
		default:
			sink.Emit(Progress{Stage: StageBuild, Message: line, Step: step, Steps: steps})
		}
	}
	fail := func(err error) error {
		sink.Emit(Progress{Stage: StageBuild, Message: err.Error(), Step: step, Steps: steps, Failed: true})
		return err
	}
	for {
		var msg buildMessage
		if err := dec.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				if buf.Len() > 0 {
					emitLine(buf.String())
				}
				return nil
			}
			return fail(err)
		}
		if msg.Error != nil {
			return fail(errors.New(msg.Error.Message))
		}
		for chunk := msg.Stream; chunk != ""; {
			line, rest, found := strings.Cut(chunk, "\n")
			if !found {
				buf.WriteString(line)
				break
			}
			buf.WriteString(line)
			emitLine(buf.String())
			buf.Reset()
			chunk = rest
		}
	}
}

// tarDirectory tars a frozen context of regular files and directories.
// Anything else is an error: candidate contexts were validated at
// snapshot time and must stay that way.
func tarDirectory(root string) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return tw.WriteHeader(&tar.Header{Name: rel + "/", Typeflag: tar.TypeDir, Mode: 0o755})
		case d.Type().IsRegular():
			mode := int64(0o644)
			if info.Mode()&0o100 != 0 {
				mode = 0o755
			}
			if err := tw.WriteHeader(&tar.Header{Name: rel, Size: info.Size(), Mode: mode}); err != nil {
				return err
			}
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			_, err = io.Copy(tw, f)
			f.Close()
			return err
		default:
			return fmt.Errorf("%w: build context entry %s is not a regular file", domain.ErrInvalid, rel)
		}
	})
	if err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// WaitContainer blocks until the container exits and returns its exit
// code.
func (s *SDK) WaitContainer(ctx context.Context, id ContainerID) (int, error) {
	respCh, errCh := s.cli.ContainerWait(ctx, string(id), container.WaitConditionNotRunning)
	select {
	case <-ctx.Done():
		return 0, fmt.Errorf("%w: wait container: %v", domain.ErrCanceled, context.Cause(ctx))
	case err := <-errCh:
		return 0, mapErr("wait container", err)
	case resp := <-respCh:
		if resp.Error != nil {
			return 0, fmt.Errorf("wait container: %s", resp.Error.Message)
		}
		return int(resp.StatusCode), nil
	}
}

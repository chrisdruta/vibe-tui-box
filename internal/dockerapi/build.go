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
	sink.Emit(Progress{Stage: "build", Message: req.Tag, Done: true})
	return BuiltImage{Ref: ImageRef(req.Tag), Digest: digest}, nil
}

type buildMessage struct {
	Stream string `json:"stream"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
}

func forwardBuildProgress(r io.Reader, sink ProgressSink) error {
	dec := json.NewDecoder(r)
	for {
		var msg buildMessage
		if err := dec.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if msg.Error != nil {
			return errors.New(msg.Error.Message)
		}
		if msg.Stream != "" {
			sink.Emit(Progress{Stage: "build", Message: msg.Stream})
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

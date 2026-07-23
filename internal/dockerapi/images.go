package dockerapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/image"

	"github.com/chrisdruta/vibe-tui-box/internal/domain"
)

// ResolveImage pins a reference to a digest. A reference that already
// carries a digest is trusted as-is. Otherwise the local image is
// inspected (pulling once if absent) and its repository digest — or,
// for purely local images, its content ID — becomes the pin.
func (s *SDK) ResolveImage(ctx context.Context, ref ImageRef) (ResolvedImage, error) {
	if _, after, found := strings.Cut(string(ref), "@"); found {
		digest, err := domain.ParseDigest(after)
		if err != nil {
			return ResolvedImage{}, fmt.Errorf("image %s: %w", ref, err)
		}
		return ResolvedImage{Ref: ref, Digest: digest}, nil
	}

	inspect, err := s.cli.ImageInspect(ctx, string(ref))
	if cerrdefs.IsNotFound(err) {
		if pullErr := s.PullImage(ctx, ResolvedImage{Ref: ref}, nil); pullErr != nil {
			return ResolvedImage{}, pullErr
		}
		inspect, err = s.cli.ImageInspect(ctx, string(ref))
	}
	if err != nil {
		return ResolvedImage{}, mapErr("inspect image "+string(ref), err)
	}

	// Prefer the registry digest matching this reference's repository.
	repo := string(ref)
	if i := strings.LastIndex(repo, ":"); i > strings.LastIndex(repo, "/") {
		repo = repo[:i]
	}
	for _, rd := range inspect.RepoDigests {
		name, digestStr, found := strings.Cut(rd, "@")
		if !found || name != repo {
			continue
		}
		digest, err := domain.ParseDigest(digestStr)
		if err != nil {
			continue
		}
		return ResolvedImage{Ref: ref, Digest: digest}, nil
	}
	// Locally built or loaded image: pin by content ID.
	digest, err := domain.ParseDigest(inspect.ID)
	if err != nil {
		return ResolvedImage{}, fmt.Errorf("image %s has unparseable id %q", ref, inspect.ID)
	}
	return ResolvedImage{Ref: ref, Digest: digest}, nil
}

// PullImage pulls by digest when the image is pinned, by reference
// otherwise, forwarding normalized progress.
func (s *SDK) PullImage(ctx context.Context, img ResolvedImage, sink ProgressSink) error {
	pullRef := string(img.Ref)
	if !img.Digest.IsZero() {
		repo := pullRef
		if name, _, found := strings.Cut(pullRef, "@"); found {
			repo = name
		} else if i := strings.LastIndex(repo, ":"); i > strings.LastIndex(repo, "/") {
			repo = repo[:i]
		}
		pullRef = repo + "@" + img.Digest.String()
	}
	rc, err := s.cli.ImagePull(ctx, pullRef, image.PullOptions{})
	if err != nil {
		return mapErr("pull image "+pullRef, err)
	}
	defer rc.Close()
	if err := forwardPullProgress(rc, pullRef, sink); err != nil {
		return fmt.Errorf("pull image %s: %w", pullRef, err)
	}
	sink.Emit(Progress{Stage: "pull", Message: pullRef, Done: true})
	return nil
}

// pullMessage is the subset of the daemon's JSON progress stream the
// engine consumes. Errors travel in-band.
type pullMessage struct {
	Status         string `json:"status"`
	ID             string `json:"id"`
	ProgressDetail struct {
		Current int64 `json:"current"`
		Total   int64 `json:"total"`
	} `json:"progressDetail"`
	Error *struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
}

func forwardPullProgress(r io.Reader, ref string, sink ProgressSink) error {
	dec := json.NewDecoder(r)
	for {
		var msg pullMessage
		if err := dec.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if msg.Error != nil {
			return errors.New(msg.Error.Message)
		}
		sink.Emit(Progress{
			Stage:   "pull",
			Message: strings.TrimSpace(ref + " " + msg.Status),
			Current: msg.ProgressDetail.Current,
			Total:   msg.ProgressDetail.Total,
		})
	}
}

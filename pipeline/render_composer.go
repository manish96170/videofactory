package pipeline

import (
	"context"

	"github.com/manish96170/videofactory/artifact"
	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/render"
)

// VerifiedMediaResolver resolves an exact artifact identity only after checking
// its stored media and metadata. artifact.Store implements this boundary.
type VerifiedMediaResolver interface {
	ResolveMedia(context.Context, artifact.Identity) (string, artifact.Entry, bool, error)
}

// RenderTimelineComposer bridges verified preview logical names to their
// controlled artifact-root files before invoking the production FFmpeg timeline
// composer. It never joins a caller-provided name to a filesystem root.
type RenderTimelineComposer struct {
	media VerifiedMediaResolver
}

func NewRenderTimelineComposer(media VerifiedMediaResolver) (*RenderTimelineComposer, error) {
	if nilDependency(media) {
		return nil, ErrInvalid
	}
	return &RenderTimelineComposer{media: media}, nil
}

func (c *RenderTimelineComposer) Compose(ctx context.Context, timeline domain.Timeline, shots []Shot, output string) error {
	if ctx == nil || c == nil || nilDependency(c.media) {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	media := make(map[domain.ID]string, len(shots))
	for _, shot := range shots {
		if shot.ID.IsZero() || shot.View.Revision() == "" || shot.View.Identity().ShotID != shot.ID {
			return ErrArtifact
		}
		identity := shot.View.Identity()
		path, entry, found, err := c.media.ResolveMedia(ctx, identity)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return safeDependency(ErrArtifact, err)
		}
		if !found || entry.Identity != identity || entry.Media.Name != shot.View.MediaName() || entry.Media.Digest == (artifact.Digest{}) || entry.Media.Digest != shot.View.MediaDigest() || path == "" {
			return ErrArtifact
		}
		if _, duplicate := media[shot.ID]; duplicate {
			return ErrArtifact
		}
		media[shot.ID] = path
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return render.ComposeTimeline(ctx, timeline, media, output)
}

var _ TimelineComposer = (*RenderTimelineComposer)(nil)
var _ VerifiedMediaResolver = (*artifact.Store)(nil)
